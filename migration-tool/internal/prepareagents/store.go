package prepareagents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// CurrentSchemaVersion is the schema version this binary knows how to work with.
// Bump this when adding migrations below.
const CurrentSchemaVersion = 1

// Metadata keys persisted in the metadata table.
const (
	metaKeySchemaVersion    = "schema_version"
	metaKeyAgentManagerHost = "agentmanager_host"
	metaKeyCreatedAt        = "created_at"
	metaKeyLastRunAt        = "last_run_at"
)

// Sentinel errors returned by the Store. Callers can match these with errors.Is.
var (
	// ErrDBCorrupted — PRAGMA integrity_check did not return "ok".
	ErrDBCorrupted = errors.New("sqlite database is corrupted")

	// ErrSchemaTooNew — the on-disk schema_version is greater than CurrentSchemaVersion.
	// This means a newer version of the tool created this DB and we cannot safely use it.
	ErrSchemaTooNew = errors.New("sqlite database was created by a newer version of the tool")

	// ErrAgentManagerMismatch — the DB was created against a different AgentManager host.
	// User must move to a different cwd or delete the DB.
	ErrAgentManagerMismatch = errors.New("sqlite database belongs to a different agentmanager host")
)

// Store is the persistence layer for prepare-agents state.
// All methods are safe for concurrent use by the orchestrator's worker goroutines
// because SQLite (with WAL + busy_timeout) serializes writes internally.
type Store struct {
	db *sql.DB
}

// OpenStore opens or creates the SQLite database at path.
//
// The function:
//  1. Opens the database with WAL mode and a 5s busy timeout
//  2. Runs PRAGMA integrity_check — returns ErrDBCorrupted on failure
//  3. Creates tables if they do not exist
//  4. Migrates the schema if schema_version is older than CurrentSchemaVersion
//  5. Returns ErrSchemaTooNew if schema_version is newer than CurrentSchemaVersion
//  6. Validates that metadata.agentmanager_host matches the expected host
//     (or sets it on a fresh DB) — returns ErrAgentManagerMismatch otherwise
func OpenStore(path string, expectedAgentManagerHost string) (*Store, error) {
	// modernc.org/sqlite uses URI-style query params for pragmas.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Limit to a single writer connection — modernc.org/sqlite is goroutine-safe but
	// we want predictable serialization for our state machine.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	store := &Store{db: db}

	if err := store.integrityCheck(); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := store.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ensure schema: %w", err)
	}

	if err := store.validateOrSetAgentManagerHost(expectedAgentManagerHost); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := store.SetMetadata(metaKeyLastRunAt, time.Now().UTC().Format(time.RFC3339)); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set last_run_at: %w", err)
	}

	return store, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) integrityCheck() error {
	var result string
	row := s.db.QueryRow("PRAGMA integrity_check")
	if err := row.Scan(&result); err != nil {
		return fmt.Errorf("integrity_check: %w", err)
	}
	if !strings.EqualFold(result, "ok") {
		return fmt.Errorf("%w: %s", ErrDBCorrupted, result)
	}
	return nil
}

func (s *Store) ensureSchema() error {
	// metadata table first — schema_version lives here.
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS metadata (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create metadata: %w", err)
	}

	currentVersion, freshDB, err := s.readSchemaVersion()
	if err != nil {
		return err
	}

	if currentVersion > CurrentSchemaVersion {
		return fmt.Errorf("%w: on-disk version %d, binary supports up to %d",
			ErrSchemaTooNew, currentVersion, CurrentSchemaVersion)
	}

	// Apply migrations in order. Each migration takes the DB from version N to N+1.
	for v := currentVersion; v < CurrentSchemaVersion; v++ {
		if err := s.applyMigration(v + 1); err != nil {
			return fmt.Errorf("apply migration to v%d: %w", v+1, err)
		}
	}

	if freshDB {
		now := time.Now().UTC().Format(time.RFC3339)
		if err := s.SetMetadata(metaKeyCreatedAt, now); err != nil {
			return fmt.Errorf("set created_at: %w", err)
		}
	}

	return s.SetMetadata(metaKeySchemaVersion, fmt.Sprintf("%d", CurrentSchemaVersion))
}

// readSchemaVersion returns the current schema version on disk and whether
// this is a fresh database (no metadata row yet).
func (s *Store) readSchemaVersion() (int, bool, error) {
	var raw string
	row := s.db.QueryRow("SELECT value FROM metadata WHERE key = ?", metaKeySchemaVersion)
	err := row.Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read schema_version: %w", err)
	}
	var v int
	if _, err := fmt.Sscanf(raw, "%d", &v); err != nil {
		return 0, false, fmt.Errorf("parse schema_version %q: %w", raw, err)
	}
	return v, false, nil
}

// applyMigration applies the migration that takes the DB from version target-1 to target.
func (s *Store) applyMigration(target int) error {
	switch target {
	case 1:
		return s.migrateV1()
	default:
		return fmt.Errorf("unknown migration target version %d", target)
	}
}

// migrateV1 creates the initial agents table.
func (s *Store) migrateV1() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agents (
			agent_key      TEXT PRIMARY KEY,
			id             INTEGER NOT NULL DEFAULT 0,
			hostname       TEXT NOT NULL,
			ip             TEXT NOT NULL,
			os             TEXT NOT NULL,
			architecture   TEXT NOT NULL DEFAULT '',
			state          TEXT NOT NULL CHECK(state IN ('pending','deployed','failed','offline','skipped','gone')),
			attempts       INTEGER NOT NULL DEFAULT 0,
			missing_cycles INTEGER NOT NULL DEFAULT 0,
			last_attempt   DATETIME,
			next_retry     DATETIME,
			last_error     TEXT NOT NULL DEFAULT '',
			deployed_at    DATETIME,
			created_at     DATETIME NOT NULL,
			updated_at     DATETIME NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agents_state ON agents(state)`,
		`CREATE INDEX IF NOT EXISTS idx_agents_next_retry ON agents(next_retry)`,
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", firstLine(stmt), err)
		}
	}
	return tx.Commit()
}

func (s *Store) validateOrSetAgentManagerHost(host string) error {
	stored, err := s.GetMetadata(metaKeyAgentManagerHost)
	if err != nil {
		return fmt.Errorf("read agentmanager_host: %w", err)
	}

	if stored == "" {
		// Fresh DB — set it.
		return s.SetMetadata(metaKeyAgentManagerHost, host)
	}

	if !strings.EqualFold(stored, host) {
		return fmt.Errorf("%w: stored=%s, expected=%s", ErrAgentManagerMismatch, stored, host)
	}
	return nil
}

// GetMetadata returns the value of a metadata key, or empty string if not set.
func (s *Store) GetMetadata(key string) (string, error) {
	var v string
	row := s.db.QueryRow("SELECT value FROM metadata WHERE key = ?", key)
	err := row.Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetMetadata upserts a metadata key/value pair.
func (s *Store) SetMetadata(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO metadata(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

// UpsertDiscoveredAgent inserts an agent if it does not exist, or updates the mutable
// discovery fields (hostname, ip, os) and resets missing_cycles to 0.
//
// Important: this method does NOT change the state of an existing agent unless the agent
// was previously OFFLINE. If the AgentManager now reports the agent as ONLINE, an OFFLINE
// agent transitions back to PENDING so the orchestrator picks it up.
//
// `online` indicates whether the AgentManager currently reports this agent as ONLINE.
func (s *Store) UpsertDiscoveredAgent(ctx context.Context, agentKey string, agentId uint32, hostname, ip, agentOS string, online bool) error {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var existingState State
	row := tx.QueryRowContext(ctx, "SELECT state FROM agents WHERE agent_key = ?", agentKey)
	err = row.Scan(&existingState)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		// New agent.
		initialState := StatePending
		if !online {
			initialState = StateOffline
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO agents(
				agent_key, id, hostname, ip, os, architecture, state,
				attempts, missing_cycles, last_error, created_at, updated_at
			) VALUES(?, ?, ?, ?, ?, '', ?, 0, 0, '', ?, ?)
		`, agentKey, agentId, hostname, ip, agentOS, initialState, now, now)
		if err != nil {
			return fmt.Errorf("insert agent: %w", err)
		}

	case err != nil:
		return fmt.Errorf("read existing state: %w", err)

	default:
		// Existing agent — update mutable fields, reset missing_cycles.
		newState := existingState

		// State transitions driven by discovery:
		//   - OFFLINE + online=true  -> PENDING (pick up next cycle)
		//   - PENDING + online=false -> OFFLINE
		//   - everything else        -> unchanged (deployed/skipped/gone are terminal,
		//                                          failed keeps its retry schedule)
		if existingState == StateOffline && online {
			newState = StatePending
		} else if existingState == StatePending && !online {
			newState = StateOffline
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE agents
			SET hostname = ?, ip = ?, os = ?, id = ?, state = ?,
			    missing_cycles = 0, updated_at = ?
			WHERE agent_key = ?
		`, hostname, ip, agentOS, agentId, newState, now, agentKey)
		if err != nil {
			return fmt.Errorf("update agent: %w", err)
		}
	}

	return tx.Commit()
}

// IncrementMissingCycles bumps missing_cycles by 1 for every non-terminal agent whose
// agent_key is NOT in the provided set. Agents that reach MissingCyclesThreshold are
// transitioned to StateGone. Returns the number of agents transitioned to gone.
func (s *Store) IncrementMissingCycles(ctx context.Context, presentKeys map[string]struct{}) (int, error) {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		SELECT agent_key, state, missing_cycles
		FROM agents
		WHERE state NOT IN ('deployed','skipped','gone')
	`)
	if err != nil {
		return 0, fmt.Errorf("query non-terminal agents: %w", err)
	}

	type pendingUpdate struct {
		key      string
		newState State
		missing  int
	}
	var updates []pendingUpdate

	for rows.Next() {
		var key string
		var state State
		var missing int
		if err := rows.Scan(&key, &state, &missing); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan: %w", err)
		}
		if _, present := presentKeys[key]; present {
			continue
		}
		missing++
		newState := state
		if missing >= MissingCyclesThreshold {
			newState = StateGone
		}
		updates = append(updates, pendingUpdate{key: key, newState: newState, missing: missing})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	goneCount := 0
	for _, u := range updates {
		if _, err := tx.ExecContext(ctx, `
			UPDATE agents
			SET missing_cycles = ?, state = ?, updated_at = ?
			WHERE agent_key = ?
		`, u.missing, u.newState, now, u.key); err != nil {
			return 0, fmt.Errorf("update missing for %s: %w", u.key, err)
		}
		if u.newState == StateGone {
			goneCount++
		}
	}

	return goneCount, tx.Commit()
}

// SelectActionable returns the agents the next loop iteration should attempt to deploy:
//   - StatePending agents (always)
//   - StateFailed agents whose next_retry has elapsed AND attempts < MaxAttempts
//
// StateOffline agents are NOT included; they need to come back via UpsertDiscoveredAgent first.
func (s *Store) SelectActionable(ctx context.Context, now time.Time) ([]AgentState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_key, id, hostname, ip, os, architecture, state, attempts, missing_cycles,
		       COALESCE(last_attempt, ''), COALESCE(next_retry, ''), last_error,
		       COALESCE(deployed_at, ''), created_at, updated_at
		FROM agents
		WHERE
		    state = 'pending'
		    OR (state = 'failed' AND attempts < ? AND (next_retry IS NULL OR next_retry <= ?))
		ORDER BY
		    CASE state WHEN 'pending' THEN 0 ELSE 1 END,
		    next_retry ASC NULLS FIRST
	`, MaxAttempts, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("select actionable: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []AgentState
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MarkDeployed transitions an agent to StateDeployed.
func (s *Store) MarkDeployed(ctx context.Context, agentKey, architecture string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE agents
		SET state = ?, architecture = ?, attempts = attempts + 1,
		    last_attempt = ?, next_retry = NULL, last_error = '',
		    deployed_at = ?, updated_at = ?
		WHERE agent_key = ?
	`, StateDeployed, architecture, now, now, now, agentKey)
	return err
}

// MarkFailed transitions an agent to StateFailed and schedules the next retry.
// If attempts (after this failure) reaches MaxAttempts, next_retry is set to NULL,
// effectively pausing the agent until the user manually intervenes.
func (s *Store) MarkFailed(ctx context.Context, agentKey, architecture, errMsg string, nextRetry time.Time) error {
	now := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var attempts int
	if err := tx.QueryRowContext(ctx, "SELECT attempts FROM agents WHERE agent_key = ?", agentKey).Scan(&attempts); err != nil {
		return fmt.Errorf("read attempts: %w", err)
	}
	attempts++

	var nextRetryArg interface{} = nextRetry.UTC()
	if attempts >= MaxAttempts {
		nextRetryArg = nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE agents
		SET state = ?, architecture = COALESCE(NULLIF(?, ''), architecture),
		    attempts = ?, last_attempt = ?, next_retry = ?,
		    last_error = ?, updated_at = ?
		WHERE agent_key = ?
	`, StateFailed, architecture, attempts, now, nextRetryArg, errMsg, now, agentKey); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	return tx.Commit()
}

// MarkSkipped sets all non-terminal agents (pending, offline, failed) to StateSkipped.
// Returns the number of rows affected. Used when the user aborts the loop.
func (s *Store) MarkSkipped(ctx context.Context) (int64, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `
		UPDATE agents
		SET state = ?, updated_at = ?, last_error = COALESCE(NULLIF(last_error, ''), 'skipped by user')
		WHERE state IN ('pending','offline','failed')
	`, StateSkipped, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ListByState returns agents currently in the given state.
func (s *Store) ListByState(ctx context.Context, state State) ([]AgentState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_key, id, hostname, ip, os, architecture, state, attempts, missing_cycles,
		       COALESCE(last_attempt, ''), COALESCE(next_retry, ''), last_error,
		       COALESCE(deployed_at, ''), created_at, updated_at
		FROM agents
		WHERE state = ?
		ORDER BY hostname
	`, state)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []AgentState
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ComputeStats returns counts by state across all agents.
func (s *Store) ComputeStats(ctx context.Context) (Stats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT state, COUNT(*) FROM agents GROUP BY state`)
	if err != nil {
		return Stats{}, err
	}
	defer func() { _ = rows.Close() }()

	var stats Stats
	for rows.Next() {
		var state State
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return Stats{}, err
		}
		stats.Total += count
		switch state {
		case StatePending:
			stats.Pending = count
		case StateDeployed:
			stats.Deployed = count
		case StateFailed:
			stats.Failed = count
		case StateOffline:
			stats.Offline = count
		case StateSkipped:
			stats.Skipped = count
		case StateGone:
			stats.Gone = count
		}
	}
	return stats, rows.Err()
}

// rowScanner is the minimal interface satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanAgent(r rowScanner) (AgentState, error) {
	var (
		a                                    AgentState
		lastAttemptStr, nextRetryStr, depStr string
	)
	err := r.Scan(
		&a.AgentKey, &a.ID, &a.Hostname, &a.IP, &a.OS, &a.Architecture, &a.State,
		&a.Attempts, &a.MissingCycles,
		&lastAttemptStr, &nextRetryStr, &a.LastError,
		&depStr, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return AgentState{}, err
	}
	a.LastAttempt = parseOptionalTime(lastAttemptStr)
	a.NextRetry = parseOptionalTime(nextRetryStr)
	a.DeployedAt = parseOptionalTime(depStr)
	return a, nil
}

func parseOptionalTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	// SQLite stores DATETIME values in formats compatible with these layouts.
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
