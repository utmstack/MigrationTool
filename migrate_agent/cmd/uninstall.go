package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/utmstack/MigrationTool/config"
	svc "github.com/utmstack/MigrationTool/service"
)

// The parent migrator is already stopped by the time this child runs: systemd
// kills the whole unit cgroup on stop, so a child Stop() call would terminate
// itself before Uninstall() executes. Only wait for the unit to leave.
type unitStatuser interface {
	Status() (service.Status, error)
}

func waitUnitGone(s unitStatuser, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		status, err := s.Status()
		if err != nil {
			return err
		}
		if status == service.StatusStopped {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("unit still not stopped after %v", timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

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

		if err := waitUnitGone(s, 20*time.Second); err != nil {
			return fmt.Errorf("wait unit stopped: %w", err)
		}

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
