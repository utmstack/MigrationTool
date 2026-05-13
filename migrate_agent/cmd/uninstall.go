package cmd

import (
	"fmt"
	"os"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/utmstack/MigrationTool/config"
	svc "github.com/utmstack/MigrationTool/service"
)

var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Uninstall the migration service",
	Long:  `Removes the UTMStack Migration Service from the system service manager and cleans up all files.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		prg := &svc.MigrationProgram{}
		s, err := svc.NewService(prg)
		if err != nil {
			return fmt.Errorf("failed to create service: %w", err)
		}

		// Stop service first (ignore errors, may not be running)
		_ = s.Stop()

		err = s.Uninstall()
		if err != nil {
			return fmt.Errorf("failed to uninstall service: %w", err)
		}

		fmt.Println("Migration service uninstalled successfully")

		// Clean up migration directory and logs
		migrationDir := config.MigrationPath()
		logDir := config.MigrationLogPath()

		if err := os.RemoveAll(logDir); err != nil {
			fmt.Printf("Warning: failed to remove log directory %s: %v\n", logDir, err)
		} else {
			fmt.Printf("Removed log directory: %s\n", logDir)
		}

		if err := os.RemoveAll(migrationDir); err != nil {
			fmt.Printf("Warning: failed to remove migration directory %s: %v\n", migrationDir, err)
		} else {
			fmt.Printf("Removed migration directory: %s\n", migrationDir)
		}

		return nil
	},
}

var _ = service.Interactive // Ensure import is used
