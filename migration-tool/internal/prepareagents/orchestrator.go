package prepareagents

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/agentmanager"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

// Defaults for the discovery loop and abort thresholds.
const (
	DefaultDiscoveryInterval = 60 * time.Second
	MaxConsecutiveListErrors = 5
	DeployConcurrency        = 5
	GracefulShutdownGrace    = 30 * time.Second
)

// ErrPermanentFailures is returned by Run when the deployment loop terminates
// with at least one agent stuck in StateFailed after exhausting retries. The
// loop did its job — it just could not deploy everywhere — and the caller
// must NOT proceed to `upgrade` until those agents are resolved or skipped.
var ErrPermanentFailures = errors.New("prepare-agents finished with permanently failed agents")

// Config controls orchestrator behavior. Zero values fall back to defaults.
type Config struct {
	DiscoveryInterval time.Duration // default: DefaultDiscoveryInterval
	DeployConcurrency int           // default: DeployConcurrency
}

func (c Config) discoveryInterval() time.Duration {
	if c.DiscoveryInterval <= 0 {
		return DefaultDiscoveryInterval
	}
	return c.DiscoveryInterval
}

func (c Config) deployConcurrency() int {
	if c.DeployConcurrency <= 0 {
		return DeployConcurrency
	}
	return c.DeployConcurrency
}

// Orchestrator drives the deployment loop: discovery + actionable selection + deploy + persist.
//
// The loop terminates when:
//   - All agents are in terminal states or have permanently failed
//     (Stats.AllDone() returns true), OR
//   - The context is cancelled (user pressed Ctrl+C, parent decided to stop), OR
//   - The AgentManager.ListAgents call fails for MaxConsecutiveListErrors cycles in a row.
type Orchestrator struct {
	cfg      Config
	store    *Store
	client   *agentmanager.Client
	deployer *agentmanager.Deployer
}

// NewOrchestrator wires the orchestrator with its collaborators.
func NewOrchestrator(cfg Config, store *Store, client *agentmanager.Client) *Orchestrator {
	deployer := agentmanager.NewDeployer(client)
	return &Orchestrator{
		cfg:      cfg,
		store:    store,
		client:   client,
		deployer: deployer,
	}
}

// Run drives the deployment loop until completion, context cancellation, or fatal error.
//
// Returns:
//   - nil on full success (Stats.WasSuccessful() returns true: every agent is
//     Deployed / Skipped / Gone)
//   - ErrPermanentFailures when the loop terminates naturally but at least one
//     agent stayed in StateFailed after exhausting retries
//   - context.Canceled / context.DeadlineExceeded on cancellation
//   - a wrapped error if the AgentManager is unreachable for MaxConsecutiveListErrors cycles
func (o *Orchestrator) Run(ctx context.Context) error {
	consecutiveListErrors := 0
	interval := o.cfg.discoveryInterval()

	ui.Info(fmt.Sprintf("Discovery interval: %s, max retries per agent: %d", interval, MaxAttempts))
	ui.Blank()

	for {
		// Check cancellation BEFORE doing any work.
		if err := ctx.Err(); err != nil {
			return err
		}

		// 1. Discovery: ListAgents from AgentManager.
		agents, listErr := o.client.ListAgents(ctx)
		if listErr != nil {
			consecutiveListErrors++
			ui.Warning(fmt.Sprintf("ListAgents failed (%d/%d): %v",
				consecutiveListErrors, MaxConsecutiveListErrors, listErr))
			if consecutiveListErrors >= MaxConsecutiveListErrors {
				return fmt.Errorf("AgentManager unreachable after %d consecutive failures: %w",
					consecutiveListErrors, listErr)
			}
			if err := sleepCtx(ctx, interval); err != nil {
				return err
			}
			continue
		}
		consecutiveListErrors = 0

		// 2. Persist discovered agents.
		presentKeys := make(map[string]struct{}, len(agents))
		for _, a := range agents {
			presentKeys[a.AgentKey] = struct{}{}
			online := a.Status == agentmanager.Status_ONLINE
			if err := o.store.UpsertDiscoveredAgent(ctx, a.AgentKey, a.Hostname, a.Ip, a.Os, online); err != nil {
				ui.Warning(fmt.Sprintf("Failed to persist agent %s: %v", a.AgentKey, err))
			}
		}

		// 3. Mark missing agents (and transition to gone after threshold).
		gone, err := o.store.IncrementMissingCycles(ctx, presentKeys)
		if err != nil {
			ui.Warning(fmt.Sprintf("Failed to update missing cycles: %v", err))
		} else if gone > 0 {
			ui.Info(fmt.Sprintf("%d agent(s) marked as gone (missing >%d cycles)",
				gone, MissingCyclesThreshold))
		}

		// 4. Select actionable agents and deploy.
		actionable, err := o.store.SelectActionable(ctx, time.Now())
		if err != nil {
			ui.Warning(fmt.Sprintf("Failed to select actionable agents: %v", err))
		} else if len(actionable) > 0 {
			o.deployBatch(ctx, actionable)
		}

		// 5. Compute and print stats.
		stats, err := o.store.ComputeStats(ctx)
		if err != nil {
			ui.Warning(fmt.Sprintf("Failed to compute stats: %v", err))
		} else {
			printProgress(stats)
			if stats.AllDone() {
				ui.Blank()
				if stats.HasPermanentFailures() {
					// Loop is done, but not in a good way: some agents are
					// permanently failed and the operator must intervene
					// before running `upgrade`.
					return ErrPermanentFailures
				}
				ui.Success("All agents reached a terminal state — deployment complete")
				return nil
			}
		}

		// 6. Sleep until next discovery cycle.
		if err := sleepCtx(ctx, interval); err != nil {
			return err
		}
	}
}

// deployBatch runs DeployToAgent for a slice of agents with bounded concurrency
// and persists each result.
func (o *Orchestrator) deployBatch(ctx context.Context, batch []AgentState) {
	concurrency := o.cfg.deployConcurrency()
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for _, agent := range batch {
		// Respect cancellation between dispatches.
		if err := ctx.Err(); err != nil {
			return
		}

		wg.Add(1)
		go func(a AgentState) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			// Build the minimal Agent the Deployer needs.
			amAgent := &agentmanager.Agent{
				AgentKey: a.AgentKey,
				Hostname: a.Hostname,
				Ip:       a.IP,
				Os:       a.OS,
				Status:   agentmanager.Status_ONLINE, // we only deploy to online ones
			}

			result := o.deployer.DeployToAgent(ctx, amAgent)
			o.persistResult(ctx, a, result)
		}(agent)
	}

	wg.Wait()
}

// persistResult records a deploy attempt's outcome in the store.
//
// If ctx was cancelled mid-deploy, the agent stays in StatePending (we increment attempts
// but do not transition to failed) so the next cycle picks it up cleanly.
func (o *Orchestrator) persistResult(ctx context.Context, prior AgentState, result *agentmanager.DeployResult) {
	// Use a fresh background context for persistence so a cancelled parent ctx
	// does NOT prevent us from saving the outcome of work that already happened.
	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if result == nil {
		// Defensive: should not happen, but log and bail.
		ui.Warning(fmt.Sprintf("nil deploy result for %s", prior.AgentKey))
		return
	}

	if result.Success {
		if err := o.store.MarkDeployed(persistCtx, prior.AgentKey, result.Architecture); err != nil {
			ui.Warning(fmt.Sprintf("Failed to mark %s as deployed: %v", prior.Hostname, err))
		} else {
			ui.Detail(fmt.Sprintf("  ✓ %s (%s)", prior.Hostname, prior.IP),
				fmt.Sprintf("%s/%s in %s", prior.OS, result.Architecture, result.Duration.Round(time.Second)))
		}
		return
	}

	// Treat context cancellation specially: do not consume a retry attempt.
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return
	}

	errMsg := "unknown error"
	if result.Error != nil {
		errMsg = result.Error.Error()
	}
	nextAttempts := prior.Attempts + 1
	nextRetry := NextRetryAt(time.Now(), nextAttempts)

	if err := o.store.MarkFailed(persistCtx, prior.AgentKey, result.Architecture, errMsg, nextRetry); err != nil {
		ui.Warning(fmt.Sprintf("Failed to mark %s as failed: %v", prior.Hostname, err))
		return
	}

	if nextAttempts >= MaxAttempts {
		ui.Warning(fmt.Sprintf("  ✗ %s (%s) — permanent failure after %d attempts: %s",
			prior.Hostname, prior.IP, nextAttempts, errMsg))
	} else {
		ui.Detail(fmt.Sprintf("  ✗ %s (%s)", prior.Hostname, prior.IP),
			fmt.Sprintf("attempt %d/%d failed: %s — next retry around %s",
				nextAttempts, MaxAttempts, errMsg, nextRetry.Format(time.Kitchen)))
	}
}

// printProgress emits a single-line summary of the current state.
func printProgress(s Stats) {
	ui.Info(fmt.Sprintf(
		"Progress: %d deployed | %d pending | %d offline | %d failed | %d skipped | %d gone | total %d",
		s.Deployed, s.Pending, s.Offline, s.Failed, s.Skipped, s.Gone, s.Total,
	))
}

// sleepCtx sleeps for d, returning early with ctx.Err() if the context is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
