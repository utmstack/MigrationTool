package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/agentmanager"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/prepareagents"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

// DefaultStateDBFile is the default location for the SQLite state database.
const DefaultStateDBFile = "agents-migration.db"

var (
	prepareConfigFile string
	prepareDBPath     string
	prepareAssumeYes  bool
)

var prepareAgentsCmd = &cobra.Command{
	Use:   "prepare-agents",
	Short: "Pre-deploy migrate_agent to every UTMStack agent before running upgrade",
	Long: `Connect to the UTMStack AgentManager and deploy the migrate_agent
binary to every agent managed by the source instance.

State is persisted in a SQLite database (./agents-migration.db by default) so the
command can be interrupted (Ctrl+C) and resumed without losing progress.

The loop polls the AgentManager every 60 seconds and:
  - Picks up agents that just came online
  - Retries failed deploys with exponential backoff (max 5 attempts)
  - Marks agents as 'gone' if they disappear from the AgentManager for several cycles

When every agent has reached a terminal state (deployed/skipped/gone) — or you
press Ctrl+C and accept skipping the rest — the command exits successfully.

After this command finishes, run 'upgrade' to migrate the backend itself.`,
	Example: `  utmstack_migration_tool prepare-agents --config config.yaml`,
	RunE:    runPrepareAgents,
}

func init() {
	prepareAgentsCmd.Flags().StringVarP(&prepareConfigFile, "config", "c", "",
		"Configuration file (required for remote, optional for local)")
	prepareAgentsCmd.Flags().StringVar(&prepareDBPath, "db-path", DefaultStateDBFile,
		"Path to the SQLite state database (default: ./agents-migration.db)")
	prepareAgentsCmd.Flags().BoolVarP(&prepareAssumeYes, "yes", "y", false,
		"Skip the confirmation prompt (useful for automation)")
}

func runPrepareAgents(cmd *cobra.Command, args []string) error {
	ui.Header("UTMStack — Prepare Agents")

	// 1. Load and validate configuration. Reuse the same loader used by `upgrade`.
	cfg, isLocal, err := loadPrepareConfig()
	if err != nil {
		return err
	}

	absDBPath, err := filepath.Abs(prepareDBPath)
	if err != nil {
		absDBPath = prepareDBPath
	}

	ui.SummaryOrdered("Configuration",
		[]string{"Source", "Mode", "State DB"},
		map[string]string{
			"Source":   cfg.Source.Host,
			"Mode":     localOrRemote(isLocal),
			"State DB": absDBPath,
		})

	// 2. Open the SQLite store. Validates schema and host binding.
	store, err := prepareagents.OpenStore(prepareDBPath, cfg.Source.Host)
	if err != nil {
		return explainStoreError(err)
	}
	defer func() {
		if cerr := store.Close(); cerr != nil {
			ui.Warning(fmt.Sprintf("Failed to close state DB: %v", cerr))
		}
	}()

	// 3. Read the internal_key (locally or via SSH) — needed for AgentManager auth.
	internalKey, err := readInternalKeyForPrepare(cfg.Source, isLocal)
	if err != nil {
		hintIfPermissionDenied(err, isLocal)
		return fmt.Errorf("read internal key: %w", err)
	}
	ui.Success("Internal key retrieved")

	// 4. Connect to the AgentManager.
	ui.StartOperation("Connecting to AgentManager")
	client, err := agentmanager.NewClient(cfg.Source.Host, internalKey, true)
	if err != nil {
		return fmt.Errorf("connect to AgentManager: %w", err)
	}
	defer client.Close()
	ui.Success("Connected to AgentManager")

	// 5. Build and show the deployment plan, then confirm.
	//
	// The plan-summary fetches the agent list once via ListAgents and shows the
	// operator what is about to happen: how many agents will be touched, how
	// many are reachable right now, and any prior state from a previous run
	// (so a resume is obvious instead of looking like a fresh start). The
	// upfront fetch is also a nice connectivity sanity check before the
	// orchestrator loop begins.
	if err := showPlanAndConfirm(client, store); err != nil {
		return err
	}

	// 6. Install the signal handler so Ctrl+C cancels the orchestrator gracefully.
	ctx, stopSignals := prepareagents.InstallSignalHandler(context.Background())
	defer stopSignals()

	// 7. Run the orchestrator loop. Returns when:
	//      - All agents reach a terminal state (success path)
	//      - Context cancelled (Ctrl+C)
	//      - AgentManager unreachable for too long (fatal error)
	orch := prepareagents.NewOrchestrator(prepareagents.Config{}, store, client)
	runErr := orch.Run(ctx)

	// 8. Print final summary regardless of how the loop exited.
	finalStats, statsErr := store.ComputeStats(context.Background())
	if statsErr != nil {
		ui.Warning(fmt.Sprintf("Could not compute final stats: %v", statsErr))
	}

	switch {
	case runErr == nil:
		// Natural completion.
		ui.FinalSuccess("All agents reached a terminal state")
		printFinalSummary(finalStats)
		printNextStep()
		return nil

	case errors.Is(runErr, context.Canceled), errors.Is(runErr, context.DeadlineExceeded):
		// User cancellation. Offer to mark non-terminal agents as skipped.
		ui.Blank()
		ui.Warning("Loop interrupted by user")
		printFinalSummary(finalStats)

		if prepareagents.PromptSkipPending(finalStats) {
			n, err := store.MarkSkipped(context.Background())
			if err != nil {
				ui.Error(fmt.Sprintf("Failed to mark agents as skipped: %v", err))
				return err
			}
			ui.Success(fmt.Sprintf("Marked %d agent(s) as skipped", n))

			finalStats, _ = store.ComputeStats(context.Background())
			printFinalSummary(finalStats)
			printNextStep()
			return nil
		}

		ui.Info("Pending agents preserved — run 'prepare-agents' again to resume")
		return nil

	default:
		// Fatal error (e.g., AgentManager unreachable).
		ui.FinalError("prepare-agents aborted")
		ui.Error(fmt.Sprintf("Reason: %v", runErr))
		printFinalSummary(finalStats)
		return runErr
	}
}

// loadPrepareConfig loads the same configuration shape used by `upgrade`.
// If --config is provided, parse the file. Otherwise, auto-detect the local
// instance (same path the local upgrade takes).
func loadPrepareConfig() (*domain.MigrationConfig, bool, error) {
	if prepareConfigFile != "" {
		ui.Step(1, 2, "Loading configuration from file")
		cfg, err := config.LoadConfig(prepareConfigFile)
		if err != nil {
			return nil, false, fmt.Errorf("load config: %w", err)
		}
		if err := config.ValidateConfigForUpgrade(cfg); err != nil {
			return nil, false, fmt.Errorf("invalid configuration: %w", err)
		}
		ui.Success("Configuration loaded")
		isLocal := remote.IsLocalhost(cfg.Source.Host)
		return cfg, isLocal, nil
	}

	ui.Step(1, 2, "Auto-detecting local configuration")
	cfg, err := config.LoadLocalUpgradeConfig()
	if err != nil {
		hintIfPermissionDenied(err, true)
		ui.Info("Make sure you're running this on a UTMStack V10 instance")
		ui.Info("Or provide a config file with --config")
		return nil, false, fmt.Errorf("auto-detect configuration: %w", err)
	}
	ui.Success("Configuration detected")
	return cfg, true, nil
}

// hintIfPermissionDenied surfaces a friendly hint when a local read fails because
// the process lacks privileges to read /root/utmstack.yml or /root/compose.yml.
//
// This is the most common failure mode for a first-time user running
// `prepare-agents` directly on a V10 instance without sudo. Without this hint,
// they see a raw "permission denied" trace and have no idea they need root.
//
// We only emit the hint in local mode — for SSH the failure has different causes
// (key not authorized, sudo not configured, etc.) and a generic root hint would
// mislead.
func hintIfPermissionDenied(err error, isLocal bool) {
	if !isLocal || err == nil {
		return
	}
	if !errors.Is(err, fs.ErrPermission) {
		return
	}

	bin := executableNameForHint()
	ui.Blank()
	ui.Warning("Permission denied while reading local UTMStack files")
	ui.Info(fmt.Sprintf("This command needs root access on V10 instances. Try: sudo %s prepare-agents",
		bin))
}

// executableNameForHint returns the basename of os.Args[0], or a sensible default
// so the suggested command in the hint is copy-paste friendly.
func executableNameForHint() string {
	if len(os.Args) > 0 && os.Args[0] != "" {
		return filepath.Base(os.Args[0])
	}
	return "utmstack_migration_tool"
}

// readInternalKeyForPrepare reads the internal_key from utmstack.yml either locally
// (if running on the source instance) or over SSH (for remote configurations).
func readInternalKeyForPrepare(src domain.InstanceConfig, isLocal bool) (string, error) {
	if isLocal {
		return config.ReadInternalKey()
	}

	sshClient, err := remote.NewSSHClient(remote.SSHConfig{
		Host:       src.Host,
		Port:       src.SSHPort,
		User:       src.SSHUser,
		Password:   src.SSHPassword,
		PrivateKey: src.SSHPrivateKey,
	})
	if err != nil {
		return "", fmt.Errorf("ssh dial: %w", err)
	}
	defer sshClient.Close()

	return config.ReadInternalKeyViaSSH(sshClient)
}

func explainStoreError(err error) error {
	switch {
	case errors.Is(err, prepareagents.ErrDBCorrupted):
		ui.Error("The state database is corrupted.")
		ui.Info(fmt.Sprintf("Delete %s and run 'prepare-agents' again to start fresh.", prepareDBPath))
	case errors.Is(err, prepareagents.ErrSchemaTooNew):
		ui.Error("The state database was created by a newer version of this tool.")
		ui.Info("Update the migration tool, or delete the DB and start fresh.")
	case errors.Is(err, prepareagents.ErrAgentManagerMismatch):
		ui.Error("The state database in this directory belongs to a different AgentManager host.")
		ui.Info("Move to a different working directory or delete the DB to start fresh.")
	default:
		ui.Error(fmt.Sprintf("Failed to open state database: %v", err))
	}
	return err
}

func printFinalSummary(s prepareagents.Stats) {
	ui.Blank()
	ui.SubHeader("Deployment State")
	ui.SummaryOrdered("Counts",
		[]string{"Total", "Deployed", "Pending", "Offline", "Failed", "Skipped", "Gone"},
		map[string]string{
			"Total":    fmt.Sprintf("%d", s.Total),
			"Deployed": fmt.Sprintf("%d", s.Deployed),
			"Pending":  fmt.Sprintf("%d", s.Pending),
			"Offline":  fmt.Sprintf("%d", s.Offline),
			"Failed":   fmt.Sprintf("%d", s.Failed),
			"Skipped":  fmt.Sprintf("%d", s.Skipped),
			"Gone":     fmt.Sprintf("%d", s.Gone),
		})
}

func printNextStep() {
	ui.NextSteps([]string{
		"Run 'utmstack_migration_tool upgrade' to upgrade the instance (V10 → V11)",
	})
}

func localOrRemote(isLocal bool) string {
	if isLocal {
		return "local"
	}
	return "remote"
}

// showPlanAndConfirm fetches the agent list from the AgentManager, surfaces a
// plan-summary (counts of online/offline and any prior-run state from the DB),
// and asks the operator to confirm before the orchestrator starts deploying.
//
// Skipped entirely when --yes is set.
//
// On a fresh run, "Prior state" rows are zero and the operator sees what a
// first deploy will look like. On a resume, the prior state makes it obvious
// that this is a continuation and not a re-run from scratch.
//
// If the operator declines, returns a sentinel error so the caller exits
// cleanly without falling through to the orchestrator loop.
func showPlanAndConfirm(client *agentmanager.Client, store *prepareagents.Store) error {
	ui.Blank()
	ui.StartOperation("Fetching agent inventory from AgentManager")

	// Bounded timeout: the orchestrator has its own timeouts once running,
	// but here we want to fail fast if the AgentManager is unreachable so
	// the operator gets a clear error instead of a long hang.
	listCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	agents, err := client.ListAgents(listCtx)
	if err != nil {
		return fmt.Errorf("list agents from AgentManager: %w", err)
	}

	online, offline := 0, 0
	for _, a := range agents {
		if a.Status == agentmanager.Status_ONLINE {
			online++
		} else {
			offline++
		}
	}
	ui.Success(fmt.Sprintf("Inventory ready (%d agents)", len(agents)))

	// Prior-run state — informational, may be all zeros on a fresh run.
	prior, statsErr := store.ComputeStats(context.Background())
	if statsErr != nil {
		// Non-fatal: we can still show the plan, just without prior counts.
		ui.Warning(fmt.Sprintf("Could not read prior state from DB: %v", statsErr))
	}

	ui.Blank()
	ui.SubHeader("Deployment Plan")
	ui.SummaryOrdered("Agents discovered",
		[]string{"Total", "Online (deploy now)", "Offline (retry every 60s)"},
		map[string]string{
			"Total":                     fmt.Sprintf("%d", len(agents)),
			"Online (deploy now)":       fmt.Sprintf("%d", online),
			"Offline (retry every 60s)": fmt.Sprintf("%d", offline),
		})

	if statsErr == nil && (prior.Deployed > 0 || prior.Failed > 0 || prior.Skipped > 0 || prior.Gone > 0) {
		ui.Blank()
		ui.Info("Prior state found in DB — this run will resume from where it left off")
		ui.SummaryOrdered("Prior state",
			[]string{"Deployed", "Failed", "Skipped", "Gone"},
			map[string]string{
				"Deployed": fmt.Sprintf("%d", prior.Deployed),
				"Failed":   fmt.Sprintf("%d", prior.Failed),
				"Skipped":  fmt.Sprintf("%d", prior.Skipped),
				"Gone":     fmt.Sprintf("%d", prior.Gone),
			})
	}

	ui.Blank()

	if prepareAssumeYes {
		ui.Info("Skipping confirmation (--yes)")
		return nil
	}

	if !ui.Confirm("Proceed with deployment?") {
		ui.Info("Aborted by user — no agents were touched")
		return fmt.Errorf("prepare-agents cancelled by user")
	}

	return nil
}
