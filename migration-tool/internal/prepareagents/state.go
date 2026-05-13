package prepareagents

import "time"

// State represents the deployment state of a single agent.
//
// State machine:
//
//	pending  -> deployed | failed | offline | skipped | gone
//	offline  -> pending  | deployed | failed | skipped | gone
//	failed   -> deployed | failed (retry) | skipped | gone
//	deployed -> deployed (terminal, ignored on rediscovery)
//	skipped  -> skipped  (terminal, set when user aborts the loop)
//	gone     -> gone     (terminal, set when agent disappears from AgentManager)
type State string

const (
	// StatePending — agent known to the AgentManager, never attempted (or just rediscovered).
	StatePending State = "pending"

	// StateDeployed — migrate_agent installed and verified on the agent.
	// Terminal: once deployed, the orchestrator never re-attempts.
	StateDeployed State = "deployed"

	// StateFailed — last attempt failed. May be retried until attempts >= MaxAttempts.
	StateFailed State = "failed"

	// StateOffline — agent is currently OFFLINE in the AgentManager.
	// Becomes Pending again as soon as the agent is reported ONLINE.
	StateOffline State = "offline"

	// StateSkipped — user aborted the loop and accepted leaving this agent unprocessed.
	StateSkipped State = "skipped"

	// StateGone — agent did not appear in ListAgents for MissingCyclesThreshold consecutive cycles.
	// Probably deleted from the AgentManager. Does not block the "all done" condition.
	StateGone State = "gone"
)

// MaxAttempts is the maximum number of retry attempts before an agent is considered permanently failed.
// After this, the agent stays in StateFailed but is no longer scheduled for retries.
const MaxAttempts = 5

// MissingCyclesThreshold — number of consecutive discovery cycles an agent can be missing
// from ListAgents before it is marked as StateGone.
const MissingCyclesThreshold = 3

// AgentState is the persisted state for one agent.
type AgentState struct {
	AgentKey      string    // PRIMARY KEY — stable identifier from the AgentManager
	Hostname      string    // Updated on every sync
	IP            string    // Updated on every sync
	OS            string    // linux, windows, darwin
	Architecture  string    // amd64 / arm64 — populated on first deploy attempt
	State         State     // Current state in the FSM
	Attempts      int       // Number of deploy attempts so far
	MissingCycles int       // Consecutive cycles missing from ListAgents
	LastAttempt   time.Time // Zero if never attempted
	NextRetry     time.Time // Zero if no retry scheduled
	LastError     string    // Empty if last attempt succeeded
	DeployedAt    time.Time // Zero if not yet deployed
	CreatedAt     time.Time // First time this agent was seen
	UpdatedAt     time.Time // Last write to this row
}

// IsTerminal returns true if the agent is in a terminal state and should not be retried.
func (s State) IsTerminal() bool {
	switch s {
	case StateDeployed, StateSkipped, StateGone:
		return true
	default:
		return false
	}
}

// IsRetriable returns true if an agent in this state can be picked up by the next loop iteration.
func (s State) IsRetriable() bool {
	switch s {
	case StatePending, StateOffline, StateFailed:
		return true
	default:
		return false
	}
}

// Stats summarizes the deployment state across all known agents.
type Stats struct {
	Total    int
	Pending  int
	Deployed int
	Failed   int
	Offline  int
	Skipped  int
	Gone     int
}

// AllDone returns true when the discovery/deploy loop has nothing left to do
// this run. Pending and Offline agents will still be picked up, so they keep
// the loop alive. Failed agents that exhausted retries (attempts >= MaxAttempts)
// no longer get scheduled, so they DO count as "loop is done with them" — but
// "done" does not mean "successful". Use WasSuccessful / HasPermanentFailures
// to interpret the outcome.
func (s Stats) AllDone() bool {
	return s.Pending == 0 && s.Offline == 0
}

// WasSuccessful returns true only when every known agent reached a non-failed
// terminal state (Deployed / Skipped / Gone). This is the precondition that
// must hold before recommending the operator continue with `upgrade`.
func (s Stats) WasSuccessful() bool {
	return s.Pending == 0 && s.Offline == 0 && s.Failed == 0
}

// HasPermanentFailures returns true when at least one agent is in StateFailed.
// At the moment AllDone() also returns true, those failures are permanent
// (no further retries scheduled), because actionable agents would have been
// re-selected by SelectActionable before reaching this point.
func (s Stats) HasPermanentFailures() bool {
	return s.Failed > 0
}

// Active returns the count of agents that still need work from the loop.
func (s Stats) Active() int {
	return s.Pending + s.Offline
}
