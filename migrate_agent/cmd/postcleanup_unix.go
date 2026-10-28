//go:build !windows

package cmd

import (
	"fmt"
	"os"

	"github.com/utmstack/MigrationTool/config"
)

func postCleanup() {
	logDir := config.MigrationLogPath()
	migrationDir := config.MigrationPath()

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
}
