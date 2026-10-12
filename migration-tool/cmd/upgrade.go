package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/prepareagents"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/upgrade"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

var (
	upgradeConfigFile     string
	upgradeBackupFile     string
	upgradeSkipAgentCheck bool
	upgradeWithData       bool
)

var errUserCancelled = errors.New("cancelled by user")

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "In-place upgrade from V11 to V12 (destructive)",
	Long: `Perform an in-place upgrade from UTMStack V11 to V12.

WARNING: This is a DESTRUCTIVE operation. V11 will be completely removed.

When run locally (without --config), the tool auto-detects PostgreSQL 
credentials from compose.yml and executes commands directly.

For remote upgrades, provide a config file with SSH credentials.`,
	Example: `  # Local upgrade (run this ON the V11 instance)
  utmstack_migration_tool upgrade

  # Remote upgrade (requires SSH credentials in config)
  utmstack_migration_tool upgrade --config config.yaml`,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().StringVarP(&upgradeConfigFile, "config", "c", "", "Configuration file (optional for local upgrade)")
	upgradeCmd.Flags().StringVar(&upgradeBackupFile, "data-file", "", "Custom data file name (default: utmstack-upgrade-YYYYMMDD-HHMMSS.yml)")
	upgradeCmd.Flags().BoolVar(&upgradeSkipAgentCheck, "skip-agent-check", false,
		"Skip the prepare-agents state DB check (use only if you know what you are doing)")
	upgradeCmd.Flags().BoolVar(&upgradeWithData, "with-data", false,
		"Also migrate V11 OpenSearch tenant data into V12 ClickHouse (dumped before cleanup, loaded after import)")
}

// ensureAgentsPrepared verifies that the operator has run `prepare-agents` before
// `upgrade`. It opens the SQLite state database, reads its stats, and:
//
//   - Returns silently on success if every agent reached a non-failed terminal
//     state (deployed / skipped / gone).
//   - Returns a hard error if the database is missing, empty, corrupted, belongs
//     to a different AgentManager, or has an unknown schema.
//   - Prints a detailed summary and asks the operator for confirmation when
//     agents are still pending/offline or permanently failed — letting them
//     either proceed (and accept orphaned agents on V11) or abort and re-run
//     `prepare-agents` first.
//
// The bypass flag --skip-agent-check exists for legitimate edge cases:
//   - Test environments without UTMStack agents
//   - Operators who deployed migrate_agent through external tooling
//   - Recovery scenarios where the state DB is unavailable but the deploy is known done
//
// Bypassing this check means accepting that any agent that did NOT receive the
// migrate_agent will be left orphaned on V11 after the upgrade completes.
func ensureAgentsPrepared(agentManagerHost string) error {
	if upgradeSkipAgentCheck {
		ui.Warning("Skipping prepare-agents verification (--skip-agent-check)")
		ui.Info("Agents that did not receive migrate_agent will remain on V11 after upgrade")
		return nil
	}

	dbPath := prepareDBPath
	if dbPath == "" {
		dbPath = DefaultStateDBFile
	}

	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ui.Error(fmt.Sprintf("State database '%s' not found.", dbPath))
			ui.Info("Run 'utmstack_migration_tool prepare-agents' before this command,")
			ui.Info("or pass --skip-agent-check if you have already deployed migrate_agent")
			ui.Info("through other means and accept the consequences.")
			return fmt.Errorf("prepare-agents has not been run (missing %s)", dbPath)
		}
		return fmt.Errorf("stat %s: %w", dbPath, err)
	}

	// Open the store — this also validates schema integrity, schema version,
	// and that the DB was created against the same AgentManager host.
	store, err := prepareagents.OpenStore(dbPath, agentManagerHost)
	if err != nil {
		switch {
		case errors.Is(err, prepareagents.ErrDBCorrupted):
			ui.Error("State database is corrupted.")
			ui.Info("Move it aside and re-run 'prepare-agents' to rebuild the state.")
		case errors.Is(err, prepareagents.ErrSchemaTooNew):
			ui.Error("State database was created by a newer version of the tool.")
			ui.Info("Update this binary or use the version that created the DB.")
		case errors.Is(err, prepareagents.ErrAgentManagerMismatch):
			ui.Error("State database belongs to a different AgentManager.")
			ui.Info(fmt.Sprintf("Expected host: %s", agentManagerHost))
			ui.Info("Move the DB aside or run 'upgrade' from the correct directory.")
		default:
			ui.Error(fmt.Sprintf("Failed to open state database: %v", err))
		}
		return err
	}
	defer func() {
		if cerr := store.Close(); cerr != nil {
			ui.Warning(fmt.Sprintf("Failed to close state DB: %v", cerr))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stats, err := store.ComputeStats(ctx)
	if err != nil {
		return fmt.Errorf("read prepare-agents stats: %w", err)
	}

	if stats.Total == 0 {
		ui.Error("State database exists but contains no agents.")
		ui.Info("This usually means 'prepare-agents' was interrupted before discovery.")
		ui.Info("Run 'utmstack_migration_tool prepare-agents' to populate the database.")
		return fmt.Errorf("prepare-agents DB is empty")
	}

	if stats.WasSuccessful() {
		ui.Success(fmt.Sprintf(
			"prepare-agents complete: %d deployed, %d skipped, %d gone (total %d)",
			stats.Deployed, stats.Skipped, stats.Gone, stats.Total,
		))
		return nil
	}

	// Something is unfinished. Show the operator what's pending and let them decide.
	ui.Blank()
	ui.Warning("prepare-agents did NOT finish cleanly:")
	ui.Detail("Total agents", fmt.Sprintf("%d", stats.Total))
	ui.Detail("Deployed", fmt.Sprintf("%d", stats.Deployed))
	if stats.Pending > 0 {
		ui.Detail("Pending (never attempted)", fmt.Sprintf("%d", stats.Pending))
	}
	if stats.Offline > 0 {
		ui.Detail("Offline (not reachable)", fmt.Sprintf("%d", stats.Offline))
	}
	if stats.Failed > 0 {
		ui.Detail("Failed (retries exhausted)", fmt.Sprintf("%d", stats.Failed))
		// List the failed agents so the operator sees who they are.
		if failed, ferr := store.ListByState(ctx, prepareagents.StateFailed); ferr == nil {
			for _, a := range failed {
				ui.Detail(fmt.Sprintf("  - %s (%s)", a.Hostname, a.IP), a.LastError)
			}
		}
	}
	if stats.Skipped > 0 {
		ui.Detail("Skipped", fmt.Sprintf("%d", stats.Skipped))
	}
	if stats.Gone > 0 {
		ui.Detail("Gone", fmt.Sprintf("%d", stats.Gone))
	}

	ui.Blank()
	ui.Info("If you continue, agents NOT in 'deployed' state will be orphaned on V11")
	ui.Info("after the upgrade completes. They will keep running V11 until the")
	ui.Info("migrate_agent is installed on them manually.")
	ui.Info("")
	ui.Info("Recommended: run 'utmstack_migration_tool prepare-agents' again to")
	ui.Info("resume the deployment for the pending/failed agents before upgrading.")
	ui.Blank()

	if !ui.Confirm("Continue with the upgrade anyway?") {
		return errUserCancelled
	}
	ui.Warning("Proceeding with incomplete agent deployment — orphaned agents will remain on V11")
	return nil
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	ui.Header("UTMStack Upgrade — V11 to V12")

	// Load config FIRST — ensureAgentsPrepared needs the AgentManager host to
	// validate that the state DB belongs to this installation and to inspect
	// per-agent state.
	var cfg *domain.MigrationConfig
	var err error

	if upgradeConfigFile != "" {
		ui.Step(1, 2, "Loading configuration from file")
		cfg, err = config.LoadConfig(upgradeConfigFile)
		if err != nil {
			ui.Error(fmt.Sprintf("Failed to load config: %v", err))
			return err
		}
		if err := config.ValidateConfigForUpgrade(cfg); err != nil {
			ui.Error(fmt.Sprintf("Invalid configuration: %v", err))
			return err
		}
		ui.Success("Configuration loaded")
	} else {
		ui.Step(1, 2, "Auto-detecting local configuration")
		cfg, err = config.LoadLocalUpgradeConfig()
		if err != nil {
			ui.Error(fmt.Sprintf("Failed to auto-detect configuration: %v", err))
			ui.Info("Make sure you're running this on a UTMStack V11 instance")
			ui.Info("Or provide a config file with --config")
			return err
		}
		ui.Success("Configuration detected")
	}

	if err := ensureAgentsPrepared(cfg.Source.Host); err != nil {
		if errors.Is(err, errUserCancelled) {
			// User chose to abort — not a program error.
			ui.Blank()
			ui.Info("Upgrade cancelled. Run 'utmstack_migration_tool prepare-agents' to")
			ui.Info("resume the deployment of the pending or failed agents, then re-run upgrade.")
			return nil
		}
		return err
	}

	backupFile := upgradeBackupFile
	if backupFile == "" {
		backupFile = fmt.Sprintf("utmstack-upgrade-%s.yml", time.Now().Format("20060102-150405"))
	}
	absBackupFile, err := filepath.Abs(backupFile)
	if err != nil {
		ui.Warning(fmt.Sprintf("Could not resolve absolute path: %v, using relative path", err))
		absBackupFile = backupFile
	}

	mode := "local"
	if upgradeConfigFile != "" {
		mode = "remote"
	}

	ui.SummaryOrdered("Configuration",
		[]string{"Instance", "Mode", "User", "Data File"},
		map[string]string{
			"Instance":  cfg.Source.Host,
			"Mode":      mode,
			"User":      cfg.Source.SSHUser,
			"Data File": absBackupFile,
		})

	ui.Step(2, 2, "Starting upgrade process")
	upgrader := upgrade.New(cfg.Source, cfg.Databases, backupFile, upgradeWithData)

	if err := upgrader.RunUpgrade(); err != nil {
		ui.FinalError("Upgrade failed")
		ui.Blank()
		ui.Error(fmt.Sprintf("Error: %v", err))
		ui.Blank()
		ui.Info(fmt.Sprintf("Data file saved at: %s", absBackupFile))
		ui.Info("You may need to manually restore the instance or retry")
		return err
	}

	return nil
}
