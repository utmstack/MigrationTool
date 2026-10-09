package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/converters"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/exporter"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/postgres"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/postinstall"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/yaml"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

var (
	migrateConfigFile string
	migrateBackupFile string
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Full migration between two instances (export + import)",
	Long: `Migrate data from UTMStack V11 to V12 between two separate instances.

This command exports data from the source V11 instance and imports it 
into the destination V12 instance in a single operation.

Requires PostgreSQL connectivity to both instances. SSH is optional 
but recommended for security credential synchronization.`,
	Example: `  utmstack_migration_tool migrate --config config.yaml`,
	RunE:    runMigrate,
}

func init() {
	migrateCmd.Flags().StringVarP(&migrateConfigFile, "config", "c", "", "Configuration file (required)")
	migrateCmd.Flags().StringVar(&migrateBackupFile, "data-file", "", "Custom data file name (default: utmstack-migration-YYYYMMDD-HHMMSS.yml)")

	migrateCmd.MarkFlagRequired("config")
}

func runMigrate(cmd *cobra.Command, args []string) error {
	ui.Header("UTMStack Migration Tool - Full Migration")

	// Load configuration
	ui.Step(1, 8, "Loading configuration")
	cfg, err := config.LoadConfig(migrateConfigFile)
	if err != nil {
		ui.Error(fmt.Sprintf("Failed to load config: %v", err))
		return err
	}

	if err := config.ValidateConfigForMigrate(cfg); err != nil {
		ui.Error(fmt.Sprintf("Invalid configuration: %v", err))
		return err
	}
	ui.Success("Configuration loaded and validated")

	// Same prepare-agents check used by `upgrade`. The agents register against
	// the source AgentManager, so the state DB is keyed by cfg.Source.Host. If
	// they were not pre-deployed they will stay orphaned on the source V11
	// after the migration moves data to the destination V12.
	if err := ensureAgentsPrepared(cfg.Source.Host); err != nil {
		if errors.Is(err, errUserCancelled) {
			ui.Blank()
			ui.Info("Migration cancelled. Run 'utmstack_migration_tool prepare-agents' to")
			ui.Info("resume the deployment of the pending or failed agents, then re-run migrate.")
			return nil
		}
		return err
	}

	// Prepare data file
	backupFile := migrateBackupFile
	if backupFile == "" {
		backupFile = fmt.Sprintf("utmstack-migration-%s.yml", time.Now().Format("20060102-150405"))
	}
	absBackupFile, err := filepath.Abs(backupFile)
	if err != nil {
		ui.Warning(fmt.Sprintf("Could not resolve absolute path: %v, using relative path", err))
		absBackupFile = backupFile
	}

	totalTables := 0
	for _, db := range cfg.Databases {
		totalTables += len(db.Tables)
	}

	ui.SummaryOrdered("Migration Details",
		[]string{"Source", "Destination", "Tables", "Data File"},
		map[string]string{
			"Source":      cfg.Source.Host,
			"Destination": cfg.Destination.Host,
			"Tables":      fmt.Sprintf("%d tables from %d databases", totalTables, len(cfg.Databases)),
			"Data File":   absBackupFile,
		})

	// Connect to source via SSH to manage ports
	ui.Step(2, 8, "Connecting to source instance")
	var sourceSSH *remote.SSHClient
	var sourcePortOpened bool

	if cfg.Source.SSHUser != "" {
		var err error
		sourceSSH, err = remote.NewSSHClient(remote.SSHConfig{
			Host:       cfg.Source.Host,
			Port:       cfg.Source.SSHPort,
			User:       cfg.Source.SSHUser,
			Password:   cfg.Source.SSHPassword,
			PrivateKey: cfg.Source.SSHPrivateKey,
		})
		if err != nil {
			ui.Warning(fmt.Sprintf("SSH connection to source failed: %v", err))
			ui.Info("Continuing without port management (port must be open manually)")
		} else {
			defer func() {
				if sourcePortOpened {
					ui.StartOperation("Closing source database port")
					closeDone := make(chan error, 1)
					go func() {
						_, closeErr := sourceSSH.ExecuteCommandWithSudo("docker service update --publish-rm 5432:5432 utmstack_postgres")
						closeDone <- closeErr
					}()
					select {
					case closeErr := <-closeDone:
						if closeErr != nil {
							ui.Warning(fmt.Sprintf("Failed to close source port: %v", closeErr))
						} else {
							ui.Success("Source database port closed")
						}
					case <-time.After(30 * time.Second):
						ui.Warning("Timeout closing source database port (30s)")
					}
				}
				sourceSSH.Close()
			}()

			ui.Success("SSH connection to source established")

			// Open port for export
			ui.StartOperation("Opening source database port for export")
			openDone := make(chan error, 1)
			go func() {
				_, openErr := sourceSSH.ExecuteCommandWithSudo("docker service update --publish-add 5432:5432 utmstack_postgres")
				openDone <- openErr
			}()
			select {
			case openErr := <-openDone:
				if openErr != nil {
					ui.Warning(fmt.Sprintf("Failed to open source port: %v", openErr))
				} else {
					sourcePortOpened = true
					ui.Success("Source database port opened")
					time.Sleep(10 * time.Second)
				}
			case <-time.After(30 * time.Second):
				ui.Warning("Timeout opening source database port (30s)")
			}
		}
	} else {
		ui.Info("No SSH configured for source, skipping port management")
	}

	// Export data from source
	ui.Step(3, 8, "Exporting data from source database")
	exp := exporter.New(cfg.Source, totalTables)
	backup, err := exp.Export(cfg.Databases, cfg.Source.Host, backupFile)
	if err != nil {
		ui.Error(fmt.Sprintf("Export failed: %v", err))
		return err
	}

	// Extract internal key from source
	ui.Step(4, 8, "Extracting security credentials from source")
	if sourceSSH != nil {
		content, err := sourceSSH.ReadFile(config.UTMStackYMLPath)
		if err == nil {
			internalKey, err := yaml.ExtractInternalKeyFromYAML(content)
			if err == nil {
				backup.InternalKey = internalKey
				if err := yaml.WriteBackupToFile(backup, backupFile); err != nil {
					ui.Warning(fmt.Sprintf("Could not save internal key to data file: %v", err))
				} else {
					ui.Success("Internal key extracted and saved to data file")
				}
			} else {
				ui.Warning("Could not extract internal key")
			}
		} else {
			ui.Warning("Could not read credentials file from source")
		}
	} else {
		ui.Info("No SSH connection to source, skipping credential extraction")
	}

	// Note: Source port will be closed by defer when sourceSSH.Close() is called

	// Connect to destination via SSH to manage ports
	ui.Step(5, 8, "Connecting to destination instance")
	var destSSH *remote.SSHClient
	var destPortOpened bool

	if cfg.Destination.SSHUser != "" {
		var err error
		destSSH, err = remote.NewSSHClient(remote.SSHConfig{
			Host:       cfg.Destination.Host,
			Port:       cfg.Destination.SSHPort,
			User:       cfg.Destination.SSHUser,
			Password:   cfg.Destination.SSHPassword,
			PrivateKey: cfg.Destination.SSHPrivateKey,
		})
		if err != nil {
			ui.Warning(fmt.Sprintf("SSH connection to destination failed: %v", err))
			ui.Info("Continuing without port management (port must be open manually)")
		} else {
			defer func() {
				if destPortOpened {
					ui.StartOperation("Closing destination database port")
					closeDone := make(chan error, 1)
					go func() {
						_, closeErr := destSSH.ExecuteCommandWithSudo("docker service update --publish-rm 5432:5432 utmstack_postgres")
						closeDone <- closeErr
					}()
					select {
					case closeErr := <-closeDone:
						if closeErr != nil {
							ui.Warning(fmt.Sprintf("Failed to close destination port: %v", closeErr))
						} else {
							ui.Success("Destination database port closed")
						}
					case <-time.After(30 * time.Second):
						ui.Warning("Timeout closing destination database port (30s)")
					}
				}
				destSSH.Close()
			}()

			ui.Success("SSH connection to destination established")

			// Open port for import
			ui.StartOperation("Opening destination database port for import")
			destOpenDone := make(chan error, 1)
			go func() {
				_, openErr := destSSH.ExecuteCommandWithSudo("docker service update --publish-add 5432:5432 utmstack_postgres")
				destOpenDone <- openErr
			}()
			select {
			case openErr := <-destOpenDone:
				if openErr != nil {
					ui.Warning(fmt.Sprintf("Failed to open destination port: %v", openErr))
				} else {
					destPortOpened = true
					ui.Success("Destination database port opened")
					time.Sleep(10 * time.Second)
				}
			case <-time.After(30 * time.Second):
				ui.Warning("Timeout opening destination database port (30s)")
			}
		}
	} else {
		ui.Info("No SSH configured for destination, skipping port management")
	}

	// Import data to destination
	ui.Step(6, 8, "Importing data to destination database")
	converters.SetIntegrationsConfigDir(cfg.Destination.IntegrationsConfigDir)
	imp := importer.New(cfg.Destination, totalTables)

	result, err := imp.Import(backupFile)
	if err != nil {
		ui.Error(fmt.Sprintf("Import failed: %v", err))
		return err
	}
	result.PrintSummary()

	// Run post-install queries
	ui.Step(7, 8, "Running post-installation queries")
	dbConn, err := postgres.Connect(cfg.Destination, config.DefaultDatabase)
	if err == nil {
		if err := postinstall.RunPostInstallQueries(dbConn); err != nil {
			ui.Warning(fmt.Sprintf("Post-installation queries completed with errors: %v", err))
		} else {
			ui.Success("Post-installation queries completed")
		}
		dbConn.Close()
	} else {
		ui.Warning("Could not run post-installation queries")
	}

	// Sync internal key to destination
	ui.Step(8, 8, "Synchronizing security credentials to destination")
	if backup.InternalKey != "" && destSSH != nil {
		existingContent, err := destSSH.ReadFile(config.UTMStackYMLPath)
		var updatedContent []byte

		if err != nil {
			updatedContent = []byte(fmt.Sprintf("internal_key: %s\n", backup.InternalKey))
		} else {
			updatedContent, err = yaml.UpdateInternalKeyInYAML(existingContent, backup.InternalKey)
			if err != nil {
				ui.Warning(fmt.Sprintf("Could not update YAML: %v", err))
				updatedContent = []byte(fmt.Sprintf("internal_key: %s\n", backup.InternalKey))
			}
		}

		if updatedContent != nil {
			if err := destSSH.WriteFile(config.UTMStackYMLPath, updatedContent, 0600); err != nil {
				ui.Warning("Could not write credentials to destination")
			} else {
				ui.Success("Security credentials synchronized to destination")
			}
		}
	} else if backup.InternalKey == "" {
		ui.Info("No internal key to sync")
	} else {
		ui.Info("No SSH configured for destination, skipping credential sync")
	}

	ui.FinalSuccess("Migration completed successfully")

	ui.NextSteps([]string{
		"Restart UTMStack services: docker stack deploy -c compose.yml utmstack",
		"Verify services are running: docker service ls",
		"Test the destination instance",
		fmt.Sprintf("Data file saved at: %s", absBackupFile),
	})

	// Ask about data file deletion
	if ui.Confirm("Delete data file? (keeping it allows for retry if needed)") {
		if err := os.Remove(backupFile); err != nil {
			ui.Error(fmt.Sprintf("Failed to delete data file: %v", err))
		} else {
			ui.Success("Data file deleted")
		}
	} else {
		ui.Info(fmt.Sprintf("Data file kept at: %s", absBackupFile))
	}

	return nil
}
