//go:build windows

package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/utmstack/MigrationTool/config"
)

// postCleanup removes the bridge's log dir and then its migration dir. On
// Windows the migration dir holds this running binary, which cannot be
// deleted in place: when the direct removal fails, a detached powershell
// removes it after this process exits and the file handle is released.
func postCleanup() {
	// The child was started from inside the migration dir; release the cwd
	// before removing it so the directory itself is free to be deleted.
	if err := os.Chdir(`C:\`); err != nil {
		fmt.Printf("Warning: could not change to C:\\ before cleanup: %v\n", err)
	}

	logDir := config.MigrationLogPath()
	migrationDir := config.MigrationPath()

	if err := os.RemoveAll(logDir); err != nil {
		fmt.Printf("Warning: failed to remove log directory %s: %v\n", logDir, err)
	} else {
		fmt.Printf("Removed log directory: %s\n", logDir)
	}

	if err := os.RemoveAll(migrationDir); err != nil {
		fmt.Printf("Warning: could not remove %s yet (%v); scheduling deferred removal\n", migrationDir, err)
		deferred := exec.Command("powershell.exe", "-NoProfile", "-Command",
			"Start-Sleep -Seconds 3; Remove-Item "+migrationDir+" -Recurse -Force")
		if derr := deferred.Start(); derr != nil {
			fmt.Printf("Warning: deferred removal failed to start: %v\n", derr)
		}
	} else {
		fmt.Printf("Removed migration directory: %s\n", migrationDir)
	}
}
