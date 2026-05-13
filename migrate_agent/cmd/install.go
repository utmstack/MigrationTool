package cmd

import (
	"fmt"
	"os"
	"runtime"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/utmstack/MigrationTool/config"
	svc "github.com/utmstack/MigrationTool/service"
)

func manualStartHint() string {
	switch runtime.GOOS {
	case "windows":
		return fmt.Sprintf("sc start %s", config.MigrationServiceName)
	case "darwin":
		return fmt.Sprintf("launchctl load /Library/LaunchDaemons/%s.plist", config.MigrationServiceName)
	default:
		return fmt.Sprintf("systemctl start %s", config.MigrationServiceName)
	}
}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the migration service",
	Long:  `Registers the UTMStack Migration Service with the system service manager.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Create log directory before installing
		logDir := config.MigrationLogPath()
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return fmt.Errorf("failed to create log directory %s: %w", logDir, err)
		}
		fmt.Printf("Created log directory: %s\n", logDir)

		// Create migration service directory
		migrationDir := config.MigrationPath()
		if err := os.MkdirAll(migrationDir, 0755); err != nil {
			return fmt.Errorf("failed to create migration directory %s: %w", migrationDir, err)
		}
		fmt.Printf("Created migration directory: %s\n", migrationDir)

		prg := &svc.MigrationProgram{}
		s, err := svc.NewService(prg)
		if err != nil {
			return fmt.Errorf("failed to create service: %w", err)
		}

		err = s.Install()
		if err != nil {
			return fmt.Errorf("failed to install service: %w", err)
		}

		// Start the service immediately
		err = s.Start()
		if err != nil {
			fmt.Printf("Warning: failed to start service: %v\n", err)
			fmt.Printf("You can start it manually with: %s\n", manualStartHint())
		} else {
			fmt.Println("Migration service started successfully")
		}

		fmt.Println("Migration service installed successfully")
		return nil
	},
}

var _ = service.Interactive // Ensure import is used
