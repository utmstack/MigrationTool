//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/utmstack/MigrationTool/utils"

	"github.com/utmstack/MigrationTool/config"
)

// StopAgentService stops the UTMStack Agent service
func StopAgentService() error {
	if utils.Logger != nil {
		utils.Logger.Info("Stopping %s service", config.AgentServiceName)
	}

	cmd := exec.Command("systemctl", "stop", config.AgentServiceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to stop service: %s: %w", string(output), err)
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

	// Reload systemd to pick up any service file changes
	cmd := exec.Command("systemctl", "daemon-reload")
	if output, err := cmd.CombinedOutput(); err != nil {
		if utils.Logger != nil {
			utils.Logger.ErrorF("daemon-reload failed: %s", string(output))
		}
		// Continue anyway, may not be needed
	}

	cmd = exec.Command("systemctl", "start", config.AgentServiceName)
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
	cmd := exec.Command("systemctl", "is-active", config.AgentServiceName)
	output, _ := cmd.Output()
	return strings.TrimSpace(string(output)) == "active"
}

// RemoveLogsCollectorService stops, disables and removes the v10 logs collector service
func RemoveLogsCollectorService() error {
	serviceName := config.LogsCollectorServiceName
	serviceFile := fmt.Sprintf("/etc/systemd/system/%s.service", serviceName)

	if utils.Logger != nil {
		utils.Logger.Info("Removing %s service", serviceName)
	}

	// Check if service exists
	if _, err := os.Stat(serviceFile); os.IsNotExist(err) {
		if utils.Logger != nil {
			utils.Logger.Info("Service %s does not exist, skipping", serviceName)
		}
		return nil
	}

	// Stop the service (ignore errors - may already be stopped)
	cmd := exec.Command("systemctl", "stop", serviceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		if utils.Logger != nil {
			utils.Logger.Info("Stop %s: %s (may already be stopped)", serviceName, strings.TrimSpace(string(output)))
		}
	}

	// Disable the service (ignore errors)
	cmd = exec.Command("systemctl", "disable", serviceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		if utils.Logger != nil {
			utils.Logger.Info("Disable %s: %s", serviceName, strings.TrimSpace(string(output)))
		}
	}

	// Remove the service file
	if err := os.Remove(serviceFile); err != nil && !os.IsNotExist(err) {
		if utils.Logger != nil {
			utils.Logger.ErrorF("Failed to remove service file %s: %v", serviceFile, err)
		}
		// Continue anyway
	}

	// Reload systemd
	cmd = exec.Command("systemctl", "daemon-reload")
	cmd.Run()

	if utils.Logger != nil {
		utils.Logger.Info("Service %s removed successfully", serviceName)
	}
	return nil
}

// UpdateServiceExecPath updates the ExecStart and ConditionFileIsExecutable lines in the systemd service file
func UpdateServiceExecPath(binaryPath string) error {
	if utils.Logger != nil {
		utils.Logger.Info("Updating systemd service file")
	}

	servicePath := config.SystemdServicePath()

	// Read current service file using os.ReadFile (no shell injection risk)
	content, err := os.ReadFile(servicePath)
	if err != nil {
		return fmt.Errorf("failed to read service file: %w", err)
	}

	// Find and replace ExecStart and ConditionFileIsExecutable lines
	lines := strings.Split(string(content), "\n")
	var newLines []string
	foundExecStart := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ExecStart=") {
			newLines = append(newLines, fmt.Sprintf("ExecStart=%s run", binaryPath))
			foundExecStart = true
		} else if strings.HasPrefix(trimmed, "ConditionFileIsExecutable=") {
			// Update ConditionFileIsExecutable to point to the new binary
			newLines = append(newLines, fmt.Sprintf("ConditionFileIsExecutable=%s", binaryPath))
		} else {
			newLines = append(newLines, line)
		}
	}

	if !foundExecStart {
		return fmt.Errorf("ExecStart line not found in service file")
	}

	// Write updated service file using os.WriteFile (no shell injection risk)
	newContent := strings.Join(newLines, "\n")
	if err := os.WriteFile(servicePath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}

	// Reload systemd
	cmd := exec.Command("systemctl", "daemon-reload")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload failed: %s: %w", string(output), err)
	}

	if utils.Logger != nil {
		utils.Logger.Info("Service file updated successfully")
	}
	return nil
}
