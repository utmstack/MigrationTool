//go:build darwin

package migration

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"migrate_agent/utils"
)

// SelfUninstall spawns a detached process that runs the same binary with "uninstall" command.
// This function does NOT return - it blocks until the uninstall process kills the service.
func SelfUninstall() error {
	if utils.Logger != nil {
		utils.Logger.Info("Spawning self-uninstall process")
	}

	// Get the path to the current executable
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Spawn the same binary with "uninstall" command, detached
	cmd := exec.Command(execPath, "uninstall")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.Dir = "/tmp"

	// Properly detach the process - create new session (same as Linux)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to spawn uninstall process: %w", err)
	}

	if utils.Logger != nil {
		utils.Logger.Info("Self-uninstall process spawned (PID: %d), waiting for SIGTERM", cmd.Process.Pid)
	}

	// Wait for SIGTERM from the uninstall process (via launchctl unload)
	// This allows clean shutdown instead of waiting for launchd's timeout + SIGKILL
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
	<-sigChan

	if utils.Logger != nil {
		utils.Logger.Info("Received termination signal, exiting cleanly")
	}
	os.Exit(0)
	return nil // unreachable, but keeps compiler happy
}
