package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/upgrade"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

var (
	upgradeConfigFile     string
	upgradeBackupFile     string
	upgradeSkipAgentCheck bool
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "In-place upgrade from V10 to V11 (destructive)",
	Long: `Perform an in-place upgrade from UTMStack V10 to V11.

WARNING: This is a DESTRUCTIVE operation. V10 will be completely removed.

When run locally (without --config), the tool auto-detects PostgreSQL 
credentials from compose.yml and executes commands directly.

For remote upgrades, provide a config file with SSH credentials.`,
	Example: `  # Local upgrade (run this ON the V10 instance)
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
}

// ensureAgentsPrepared verifies that the operator has run `prepare-agents` before
// `upgrade`. The check is satisfied by the presence of agents-migration.db in the
// current working directory (created and managed by the prepare-agents command).
//
// The bypass flag --skip-agent-check exists for legitimate edge cases:
//   - Test environments without UTMStack agents
//   - Operators who deployed migrate_agent through external tooling
//   - Recovery scenarios where the state DB is unavailable but the deploy is known done
//
// Bypassing this check means accepting that any agent that did NOT receive the
// migrate_agent will be left orphaned on V10 after the upgrade completes.
func ensureAgentsPrepared() error {
	if upgradeSkipAgentCheck {
		ui.Warning("Skipping prepare-agents verification (--skip-agent-check)")
		ui.Info("Agents that did not receive migrate_agent will remain on V10 after upgrade")
		return nil
	}

	if _, err := os.Stat(prepareDBPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			ui.Error(fmt.Sprintf("State database '%s' not found.", prepareDBPath))
			ui.Info("Run 'utmstack_migration_tool prepare-agents' before this command,")
			ui.Info("or pass --skip-agent-check if you have already deployed migrate_agent")
			ui.Info("through other means and accept the consequences.")
			return fmt.Errorf("prepare-agents has not been run (missing %s)", prepareDBPath)
		}
		return fmt.Errorf("stat %s: %w", prepareDBPath, err)
	}
	ui.Success("prepare-agents state database found")
	return nil
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	ui.Header("UTMStack Upgrade — V10 to V11")

	if err := ensureAgentsPrepared(); err != nil {
		return err
	}

	var cfg *domain.MigrationConfig
	var err error

	if upgradeConfigFile != "" {
		// Load from file if provided
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
		// Auto-detect for local upgrade
		ui.Step(1, 2, "Auto-detecting local configuration")
		cfg, err = config.LoadLocalUpgradeConfig()
		if err != nil {
			ui.Error(fmt.Sprintf("Failed to auto-detect configuration: %v", err))
			ui.Info("Make sure you're running this on a UTMStack V10 instance")
			ui.Info("Or provide a config file with --config")
			return err
		}
		ui.Success("Configuration detected")
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
	upgrader := upgrade.New(cfg.Source, cfg.Databases, backupFile)

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
