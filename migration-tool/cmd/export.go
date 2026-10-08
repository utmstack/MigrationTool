package cmd

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/exporter"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/yaml"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

var (
	exportConfigFile string
	exportOutputFile string
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export V11 data to file",
	Long: `Export data from UTMStack V11 instance to a YAML file.

The exported file can be imported to a V12 instance using the import command.
SSH access is optional but enables security credential extraction.`,
	Example: `  # Export with default filename
  utmstack_migration_tool export --config config.yaml

  # Export with custom output file
  utmstack_migration_tool export --config config.yaml --output data.yml`,
	RunE: runExport,
}

func init() {
	exportCmd.Flags().StringVarP(&exportConfigFile, "config", "c", "", "Configuration file (required)")
	exportCmd.Flags().StringVarP(&exportOutputFile, "output", "o", "", "Output file (default: utmstack-export-YYYYMMDD-HHMMSS.yml)")

	exportCmd.MarkFlagRequired("config")
}

func runExport(cmd *cobra.Command, args []string) error {
	ui.Header("UTMStack Migration Tool - Export")

	// Load configuration
	ui.Step(1, 4, "Loading configuration")
	cfg, err := config.LoadConfig(exportConfigFile)
	if err != nil {
		ui.Error(fmt.Sprintf("Failed to load config: %v", err))
		return err
	}

	if err := config.ValidateConfigForExport(cfg); err != nil {
		ui.Error(fmt.Sprintf("Invalid configuration: %v", err))
		return err
	}
	ui.Success("Configuration loaded")

	// Prepare output file
	outputFile := exportOutputFile
	if outputFile == "" {
		outputFile = fmt.Sprintf("utmstack-export-%s.yml", time.Now().Format("20060102-150405"))
	}
	absOutputFile, err := filepath.Abs(outputFile)
	if err != nil {
		ui.Warning(fmt.Sprintf("Could not resolve absolute path: %v, using relative path", err))
		absOutputFile = outputFile
	}

	totalTables := 0
	for _, db := range cfg.Databases {
		totalTables += len(db.Tables)
	}

	ui.SummaryOrdered("Export Details",
		[]string{"Source", "Output", "Tables"},
		map[string]string{
			"Source": cfg.Source.Host,
			"Output": absOutputFile,
			"Tables": fmt.Sprintf("%d tables from %d databases", totalTables, len(cfg.Databases)),
		})

	// Connect to source via SSH to manage ports
	ui.Step(2, 4, "Connecting to source instance")
	var sshClient *remote.SSHClient
	var portOpened bool

	if cfg.Source.SSHUser != "" {
		var err error
		sshClient, err = remote.NewSSHClient(remote.SSHConfig{
			Host:       cfg.Source.Host,
			Port:       cfg.Source.SSHPort,
			User:       cfg.Source.SSHUser,
			Password:   cfg.Source.SSHPassword,
			PrivateKey: cfg.Source.SSHPrivateKey,
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

			// Open port for export
			ui.StartOperation("Opening database port for export")
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

	// Export data
	ui.Step(3, 4, "Exporting data from source database")
	exp := exporter.New(cfg.Source, totalTables)
	backup, err := exp.Export(cfg.Databases, cfg.Source.Host, outputFile)
	if err != nil {
		ui.Error(fmt.Sprintf("Export failed: %v", err))
		return err
	}

	// Extract internal key if SSH is configured
	ui.Step(4, 4, "Extracting security credentials")
	if sshClient != nil {
		content, err := sshClient.ReadFile(config.UTMStackYMLPath)
		if err == nil {
			internalKey, err := yaml.ExtractInternalKeyFromYAML(content)
			if err == nil {
				backup.InternalKey = internalKey
				if err := yaml.WriteBackupToFile(backup, outputFile); err != nil {
					ui.Warning(fmt.Sprintf("Could not save internal key to backup file: %v", err))
				} else {
					ui.Success("Internal key extracted and saved")
				}
			} else {
				ui.Warning("Could not extract internal key (non-critical)")
			}
		} else {
			ui.Warning("Could not read credentials file (non-critical)")
		}
	} else {
		ui.Info("No SSH configured, skipping credential extraction")
	}

	ui.FinalSuccess("Export completed successfully")

	ui.NextSteps([]string{
		fmt.Sprintf("Import to V12: utmstack_migration_tool import --config config.yaml --input %s", outputFile),
	})

	return nil
}
