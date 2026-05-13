//go:build windows

package service

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"migrate_agent/config"
	"migrate_agent/utils"
)

// StopAgentService stops the UTMStack Agent service
func StopAgentService() error {
	if utils.Logger != nil {
		utils.Logger.Info("Stopping %s service", config.AgentServiceName)
	}

	cmd := exec.Command("sc", "stop", config.AgentServiceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Service might already be stopped
		if !strings.Contains(string(output), "not started") {
			return fmt.Errorf("failed to stop service: %s: %w", string(output), err)
		}
	}

	// Wait for service to fully stop
	deadline := time.Now().Add(config.ServiceStopTimeout)
	for time.Now().Before(deadline) {
		if !IsAgentServiceRunning() {
			if utils.Logger != nil {
				utils.Logger.Info("Service stopped successfully")
			}
			return nil
		}
		time.Sleep(1 * time.Second)
	}

	return fmt.Errorf("service did not stop within %v", config.ServiceStopTimeout)
}

// StartAgentService starts the UTMStack Agent service
func StartAgentService() error {
	if utils.Logger != nil {
		utils.Logger.Info("Starting %s service", config.AgentServiceName)
	}

	cmd := exec.Command("sc", "start", config.AgentServiceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start service: %s: %w", string(output), err)
	}

	if utils.Logger != nil {
		utils.Logger.Info("Service start command issued")
	}
	return nil
}

// IsAgentServiceRunning checks if the UTMStack Agent service is active
func IsAgentServiceRunning() bool {
	cmd := exec.Command("sc", "query", config.AgentServiceName)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), "RUNNING")
}

// UpdateServiceExecPath updates the service binary path using sc config
func UpdateServiceExecPath(binaryPath string) error {
	if utils.Logger != nil {
		utils.Logger.Info("Updating Windows service binary path")
	}

	// Normalize path separators for Windows
	binaryPath = filepath.Clean(binaryPath)

	// sc config expects: binpath= "path\to\binary run"
	binPath := fmt.Sprintf("\"%s\" run", binaryPath)

	cmd := exec.Command("sc", "config", config.AgentServiceName, "binpath=", binPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to update service: %s: %w", string(output), err)
	}

	if utils.Logger != nil {
		utils.Logger.Info("Service binary path updated successfully")
	}
	return nil
}

// RemoveLogsCollectorService stops and removes the v10 logs collector services
func RemoveLogsCollectorService() error {
	// Remove both v10 logs collector services on Windows
	services := []string{
		config.LogsCollectorServiceName,        // UTMStackModulesLogsCollector
		config.WindowsLogsCollectorServiceName, // UTMStackWindowsLogsCollector
	}

	for _, serviceName := range services {
		if err := removeWindowsService(serviceName); err != nil {
			if utils.Logger != nil {
				utils.Logger.ErrorF("Failed to remove %s: %v (continuing)", serviceName, err)
			}
			// Continue with next service - best effort
		}
	}

	return nil
}

// removeWindowsService stops and removes a single Windows service
func removeWindowsService(serviceName string) error {
	if utils.Logger != nil {
		utils.Logger.Info("Removing %s service", serviceName)
	}

	// Check if service exists
	cmd := exec.Command("sc", "query", serviceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(output), "does not exist") || strings.Contains(string(output), "1060") {
			if utils.Logger != nil {
				utils.Logger.Info("Service %s does not exist, skipping", serviceName)
			}
			return nil
		}
	}

	// Stop the service (ignore errors - may already be stopped)
	cmd = exec.Command("sc", "stop", serviceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		if utils.Logger != nil {
			utils.Logger.Info("Stop %s: %s (may already be stopped)", serviceName, strings.TrimSpace(string(output)))
		}
	}

	// Wait a bit for service to stop
	time.Sleep(2 * time.Second)

	// Delete the service
	cmd = exec.Command("sc", "delete", serviceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		if utils.Logger != nil {
			utils.Logger.ErrorF("Failed to delete service %s: %s", serviceName, strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("failed to delete service: %s", strings.TrimSpace(string(output)))
	}

	if utils.Logger != nil {
		utils.Logger.Info("Service %s removed successfully", serviceName)
	}
	return nil
}
