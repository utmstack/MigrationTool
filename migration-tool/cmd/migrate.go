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
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/converters"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/exporter"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/keysync"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/searchdata"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/state"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/yaml"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

var (
	migrateConfigFile   string
	migrateBackupFile   string
	migrateWithData     bool
	migrateSearchPhases *searchdata.Phases
	migrateSt           *state.State
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
	migrateCmd.Flags().BoolVar(&upgradeSkipAgentCheck, "skip-agent-check", false,
		"Skip the prepare-agents state DB check (use only if you know what you are doing)")
	migrateCmd.Flags().BoolVar(&migrateWithData, "with-data", false,
		"Also migrate search data: V11 OpenSearch tenant data -> V12 ClickHouse")

	migrateCmd.MarkFlagRequired("config")
}

// loadMigrationState resolves the checkpoint for a migrate run: it loads an
// existing state file and binds it to this run's inputs, or creates a fresh
// one. A bound-but-mismatching file or a corrupted file is a hard error.
func loadMigrationState(flow, generatedBackupFile, suppliedBackupFile, srcHost, dstHost string, withData bool, cwd string) (*state.State, error) {
	stPath := filepath.Join(cwd, state.File)
	loaded, err := state.Load(stPath)
	if err != nil {
		return nil, err
	}
	if loaded != nil {
		if err := loaded.BindCore(flow, srcHost, dstHost, withData); err != nil {
			return nil, err
		}
		if suppliedBackupFile != "" && suppliedBackupFile != loaded.BackupFile {
			return nil, fmt.Errorf("state file %s belongs to data file %q, this run uses %q (delete the file to start over)", stPath, loaded.BackupFile, suppliedBackupFile)
		}
		if suppliedBackupFile == "" {
			_ = generatedBackupFile // the checkpoint's file wins
		}
		return loaded, nil
	}
	st := state.New(stPath, flow, srcHost, dstHost, generatedBackupFile, withData)
	if err := st.Save(); err != nil {
		return nil, fmt.Errorf("create state file: %w", err)
	}
	return st, nil
}

func runMigrate(cmd *cobra.Command, args []string) error {
	ui.Header("UTMStack Migration Tool - Full Migration")

	// Load configuration
	ui.Step(1, 7, "Loading configuration")
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

	// Checkpoint state: a failed run resumes from the last completed step.
	if cwd, werr := os.Getwd(); werr != nil {
		ui.Warning(fmt.Sprintf("Could not resolve working directory: %v, checkpointing disabled", werr))
	} else {
		hadState, _ := state.Load(filepath.Join(cwd, state.File))
		migrateSt, err = loadMigrationState("migrate", absBackupFile, migrateBackupFile, cfg.Source.Host, cfg.Destination.Host, migrateWithData, cwd)
		if err != nil {
			ui.Error(err.Error())
			return err
		}
		if migrateSt.Done(state.StepExport) {
			backupFile = migrateSt.BackupFile
			absBackupFile, _ = filepath.Abs(backupFile)
			ui.Warning("Resuming from checkpoint; reusing the data file from the previous run:")
			ui.Info(fmt.Sprintf("  %s", absBackupFile))
		}
		if hadState != nil {
			ui.Warning(fmt.Sprintf("Resuming from checkpoint: %s", migrateSt.Path))
		}
	}

	totalTables := 0
	for _, db := range cfg.Databases {
		totalTables += len(db.Tables)
	}

	searchData := "no"
	if migrateWithData {
		searchData = "yes (OpenSearch -> ClickHouse)"
	}
	ui.SummaryOrdered("Migration Details",
		[]string{"Source", "Destination", "Tables", "Search Data", "Data File"},
		map[string]string{
			"Source":      cfg.Source.Host,
			"Destination": cfg.Destination.Host,
			"Tables":      fmt.Sprintf("%d tables from %d databases", totalTables, len(cfg.Databases)),
			"Search Data": searchData,
			"Data File":   absBackupFile,
		})

	// Connect to source via SSH to manage ports
	ui.Step(2, 7, "Connecting to source instance")
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
	ui.Step(3, 7, "Exporting data from source database")
	var backup *domain.BackupData
	err = runStep(migrateSt, "Data export", state.StepExport, func() error {
		exp := exporter.New(cfg.Source, totalTables)
		b, e := exp.Export(cfg.Databases, cfg.Source.Host, backupFile)
		if e != nil {
			return e
		}
		backup = b
		return nil
	})
	if err != nil {
		ui.Error(fmt.Sprintf("Export failed: %v", err))
		return migrateFailed(migrateSt, err)
	}
	if backup == nil {
		// Export was skipped: the data file from the previous run must still
		// exist, or the later steps have nothing to import.
		backup, err = importer.ReadBackupFile(absBackupFile)
		if err != nil {
			ui.Error(fmt.Sprintf("Export marked done but data file is unreadable: %v", err))
			return migrateFailed(migrateSt, err)
		}
		ui.Info("Reusing data file from the previous run")
	}

	// Stream the V11 OpenSearch tenant data into the V12 ClickHouse and verify
	// it. Both endpoints are live at this point, so no disk dump is involved.
	if migrateWithData {
		phases, err := newSearchPhases(cfg, sourceSSH)
		if err != nil {
			ui.Error(err.Error())
			return err
		}
		migrateSearchPhases = phases
	}

	// Extract internal key from source
	ui.Step(4, 7, "Extracting security credentials from source")
	if migrateSt != nil && migrateSt.Done(state.StepExport) {
		ui.Info("Internal key already saved in the data file from the previous run")
	} else if sourceSSH != nil {
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
	ui.Step(5, 7, "Connecting to destination instance")
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
		if migrateSearchPhases != nil {
			migrateSearchPhases.SetDestExec(destSSH)
		}
	} else {
		ui.Info("No SSH configured for destination, skipping port management")
	}

	// Import data to destination
	ui.Step(6, 7, "Importing data to destination database")
	converters.SetIntegrationsConfigDir(cfg.Destination.IntegrationsConfigDir)
	if destSSH != nil {
		// Plugin configs are YAML on the destination host; route writes over SSH.
		converters.SetIntegrationsFileWriter(destSSH)
	}
	imp := importer.New(cfg.Destination, totalTables)

	var result importer.Result
	if err := runStep(migrateSt, "Data import", state.StepImport, func() error {
		r, e := imp.Import(absBackupFile)
		if e != nil {
			return e
		}
		result = r
		return nil
	}); err != nil {
		ui.Error(fmt.Sprintf("Import failed: %v", err))
		return migrateFailed(migrateSt, err)
	}
	if !migrateSt.Done(state.StepImport) {
		result.PrintSummary()
	}

	ui.Step(7, 7, "Synchronizing security credentials to destination")
	if destSSH != nil {
		if err := keysync.Sync(destSSH, cfg.Destination.SSHUser, backup.InternalKey); err != nil {
			ui.Error(fmt.Sprintf("Internal key sync failed: %v", err))
			return err
		}
	} else if backup.InternalKey == "" {
		ui.Info("No internal key to sync")
	} else {
		ui.Info("No SSH configured for destination, skipping credential sync")
	}

	// Stream the V11 OpenSearch tenant data into the V12 ClickHouse and verify
	// it. Both endpoints are live at this point, so no disk dump is involved.
	if migrateSearchPhases != nil {
		if err := runStep(migrateSt, "Search data migration", state.StepStream, func() error {
			return migrateSearchPhases.RunStream(context.Background())
		}); err != nil {
			ui.Error(fmt.Sprintf("Search data migration failed: %v", err))
			return migrateFailed(migrateSt, err)
		}
	}

	ui.FinalSuccess("Migration completed successfully")
	migrateSuccess(migrateSt)

	ui.NextSteps([]string{
		"Verify services are running: docker service ls",
		"Test the destination instance",
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

	// Run one checkpointed step of the migrate flow: skip if a previous run
	// already completed it, mark only after success.
	return nil
}

func migrateFailed(st *state.State, err error) error {
	if st != nil {
		ui.Blank()
		ui.Info(fmt.Sprintf("Checkpoint saved with %d step(s) completed: %s", len(st.CompletedSteps), st.Path))
		ui.Info("Re-run the same command to resume from where it stopped.")
	}
	return err
}

// migrateSuccess deletes the checkpoint file: a completed run leaves no
// artifact behind.
func migrateSuccess(st *state.State) {
	if st == nil {
		return
	}
	if err := state.Delete(st.Path); err != nil {
		ui.Warning(fmt.Sprintf("Could not delete state file %s: %v", st.Path, err))
		ui.Warning("Delete it manually; the migration is complete.")
	}
}

// runStep executes one checkpointed step: skip if a previous run already
// completed it, mark only after success so a failed step re-runs on resume.
func runStep(st *state.State, desc, name string, fn func() error) error {
	if st != nil && st.Done(name) {
		ui.Info(desc + " already done, skipping")
		return nil
	}
	if err := fn(); err != nil {
		return err
	}
	if st != nil {
		return st.Mark(name)
	}
	return nil
}
