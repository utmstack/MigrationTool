package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/postgres"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/postinstall"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/yaml"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
	goyaml "gopkg.in/yaml.v3"
)

var (
	importConfigFile string
	importInputFile  string
)

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import data file into V11",
	Long: `Import data from a YAML file into UTMStack V11 instance.

The data file should be created using the export command.
SSH access is optional but enables security credential synchronization.`,
	Example: `  utmstack_migration_tool import --config config.yaml --input data.yml`,
	RunE:    runImport,
}

func init() {
	importCmd.Flags().StringVarP(&importConfigFile, "config", "c", "", "Configuration file (required)")
	importCmd.Flags().StringVarP(&importInputFile, "input", "i", "", "Input data file (required)")

	importCmd.MarkFlagRequired("config")
	importCmd.MarkFlagRequired("input")
}

func runImport(cmd *cobra.Command, args []string) error {
	ui.Header("UTMStack Migration Tool - Import")

	// Load configuration
	ui.Step(1, 6, "Loading configuration")
	cfg, err := config.LoadConfig(importConfigFile)
	if err != nil {
		ui.Error(fmt.Sprintf("Failed to load config: %v", err))
		return err
	}

	if err := config.ValidateConfigForImport(cfg); err != nil {
		ui.Error(fmt.Sprintf("Invalid configuration: %v", err))
		return err
	}
	ui.Success("Configuration loaded")

	// Validate data file
	ui.Step(2, 6, "Validating data file")
	absInputFile, err := filepath.Abs(importInputFile)
	if err != nil {
		ui.Warning(fmt.Sprintf("Could not resolve absolute path: %v, using relative path", err))
		absInputFile = importInputFile
	}

	if _, err := os.Stat(importInputFile); os.IsNotExist(err) {
		ui.Error(fmt.Sprintf("Data file not found: %s", absInputFile))
		return fmt.Errorf("data file does not exist: %s", absInputFile)
	}

	data, err := os.ReadFile(importInputFile)
	if err != nil {
		ui.Error(fmt.Sprintf("Failed to read data file: %v", err))
		return err
	}

	var backup domain.BackupData
	if err := goyaml.Unmarshal(data, &backup); err != nil {
		ui.Error(fmt.Sprintf("Invalid data file format: %v", err))
		return err
	}

	totalTables := 0
	for _, db := range backup.Databases {
		totalTables += len(db.Tables)
	}
	ui.Success(fmt.Sprintf("Data file validated (%d tables)", totalTables))

	ui.SummaryOrdered("Import Details",
		[]string{"Destination", "Input", "Tables", "Source"},
		map[string]string{
			"Destination": cfg.Destination.Host,
			"Input":       absInputFile,
			"Tables":      fmt.Sprintf("%d tables from %d databases", totalTables, len(backup.Databases)),
			"Source":      backup.SourceHost,
		})

	// Connect to destination via SSH to manage ports
	ui.Step(3, 6, "Connecting to destination instance")
	var sshClient *remote.SSHClient
	var portOpened bool

	if cfg.Destination.SSHUser != "" {
		var err error
		sshClient, err = remote.NewSSHClient(remote.SSHConfig{
			Host:       cfg.Destination.Host,
			Port:       cfg.Destination.SSHPort,
			User:       cfg.Destination.SSHUser,
			Password:   cfg.Destination.SSHPassword,
			PrivateKey: cfg.Destination.SSHPrivateKey,
		})
		if err != nil {
			ui.Warning(fmt.Sprintf("SSH connection failed: %v", err))
			ui.Info("Continuing without port management (port must be open manually)")
		} else {
			defer func() {
				if portOpened {
					ui.StartOperation("Closing database port for security")
					closeDone := make(chan error, 1)
					go func() {
						_, closeErr := sshClient.ExecuteCommandWithSudo("docker service update --publish-rm 5432:5432 utmstack_postgres")
						closeDone <- closeErr
					}()
					select {
					case closeErr := <-closeDone:
						if closeErr != nil {
							ui.Warning(fmt.Sprintf("Failed to close port: %v", closeErr))
						} else {
							ui.Success("Database port closed")
						}
					case <-time.After(30 * time.Second):
						ui.Warning("Timeout closing database port (30s)")
					}
				}
				sshClient.Close()
			}()

			ui.Success("SSH connection established")

			// Open port for import
			ui.StartOperation("Opening database port for import")
			openDone := make(chan error, 1)
			go func() {
				_, openErr := sshClient.ExecuteCommandWithSudo("docker service update --publish-add 5432:5432 utmstack_postgres")
				openDone <- openErr
			}()
			select {
			case openErr := <-openDone:
				if openErr != nil {
					ui.Warning(fmt.Sprintf("Failed to open port: %v", openErr))
				} else {
					portOpened = true
					ui.Success("Database port opened")
					time.Sleep(10 * time.Second)
				}
			case <-time.After(30 * time.Second):
				ui.Warning("Timeout opening database port (30s)")
			}
		}
	} else {
		ui.Info("No SSH configured, skipping port management")
	}

	// Import data
	ui.Step(4, 6, "Importing data to destination database")
	imp := importer.New(cfg.Destination, totalTables)

	if err := imp.Import(importInputFile); err != nil {
		ui.Error(fmt.Sprintf("Import failed: %v", err))
		return err
	}

	// Run post-install queries
	ui.Step(5, 6, "Running post-installation queries")
	dbConn, err := postgres.Connect(cfg.Destination, config.DefaultDatabase)
	if err == nil {
		if err := postinstall.RunPostInstallQueries(dbConn); err != nil {
			ui.Warning(fmt.Sprintf("Post-installation queries completed with errors: %v", err))
		} else {
			ui.Success("Post-installation queries completed")
		}
		dbConn.Close()
	} else {
		ui.Warning("Could not run post-installation queries (non-critical)")
	}

	// Sync internal key
	ui.Step(6, 6, "Synchronizing security credentials")
	if backup.InternalKey != "" && sshClient != nil {
		existingContent, err := sshClient.ReadFile(config.UTMStackYMLPath)
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
			if err := sshClient.WriteFile(config.UTMStackYMLPath, updatedContent, 0600); err != nil {
				ui.Warning("Could not write credentials file")
			} else {
				ui.Success("Security credentials synchronized")
			}
		}
	} else if backup.InternalKey == "" {
		ui.Info("No credentials in data file, skipping sync")
	} else {
		ui.Info("No SSH configured, skipping credential sync")
	}

	ui.FinalSuccess("Import completed successfully")

	ui.NextSteps([]string{
		"Restart UTMStack services: docker stack deploy -c compose.yml utmstack",
		"Verify services are running: docker service ls",
		"Test the V11 instance",
	})

	return nil
}
