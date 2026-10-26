//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/utmstack/MigrationTool/config"
	"github.com/utmstack/MigrationTool/utils"
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

	exec.Command("systemctl", "daemon-reload").Run()

	cmd := exec.Command("systemctl", "start", config.AgentServiceName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to start service: %s: %w", string(output), err)
	}
	return nil
}

// IsAgentServiceRunning checks if the UTMStack Agent service is active
func IsAgentServiceRunning() bool {
	cmd := exec.Command("systemctl", "is-active", config.AgentServiceName)
	output, _ := cmd.Output()
	return strings.TrimSpace(string(output)) == "active"
}

// RemoveAgentService performs the local-only uninstall of the v11 agent:
// stops the agent and updater, disables and removes their unit files, and
// drops the legacy logs collector. It never talks to the panel (no
// DeleteAgent), so the migrated agent row stays intact.
func RemoveAgentService() error {
	for _, name := range []string{config.AgentServiceName, config.UpdaterServiceName} {
		stopSystemdService(name)
	}

	for _, name := range []string{config.AgentServiceName, config.UpdaterServiceName} {
		unit := fmt.Sprintf("/etc/systemd/system/%s.service", name)
		if _, err := os.Stat(unit); os.IsNotExist(err) {
			continue
		}
		exec.Command("systemctl", "disable", name).Run()
		if err := os.Remove(unit); err != nil && !os.IsNotExist(err) && utils.Logger != nil {
			utils.Logger.ErrorF("Failed to remove unit %s: %v", unit, err)
		}
	}

	exec.Command("systemctl", "daemon-reload").Run()
	return RemoveLogsCollectorService()
}

func stopSystemdService(name string) {
	if utils.Logger != nil {
		utils.Logger.Info("Stopping %s service", name)
	}
	cmd := exec.Command("systemctl", "stop", name)
	if output, err := cmd.CombinedOutput(); err != nil && utils.Logger != nil {
		utils.Logger.Info("Stop %s: %s (may already be stopped)", name, strings.TrimSpace(string(output)))
	}
}

// RemoveLogsCollectorService stops, disables and removes the v10 logs collector service
func RemoveLogsCollectorService() error {
	serviceName := config.LogsCollectorServiceName
	serviceFile := fmt.Sprintf("/etc/systemd/system/%s.service", serviceName)

	if _, err := os.Stat(serviceFile); os.IsNotExist(err) {
		if utils.Logger != nil {
			utils.Logger.Info("Service %s does not exist, skipping", serviceName)
		}
		return nil
	}

	stopSystemdService(serviceName)
	exec.Command("systemctl", "disable", serviceName).Run()
	if err := os.Remove(serviceFile); err != nil && !os.IsNotExist(err) && utils.Logger != nil {
		utils.Logger.ErrorF("Failed to remove service file %s: %v", serviceFile, err)
	}
	exec.Command("systemctl", "daemon-reload").Run()
	return nil
}

// UpdateServiceExecPath updates the ExecStart and ConditionFileIsExecutable lines in the systemd service file
func UpdateServiceExecPath(binaryPath string) error {
	if utils.Logger != nil {
		utils.Logger.Info("Updating systemd service file")
	}

	servicePath := config.SystemdServicePath()

	content, err := os.ReadFile(servicePath)
	if err != nil {
		return fmt.Errorf("failed to read service file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var newLines []string
	foundExecStart := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ExecStart=") {
			newLines = append(newLines, fmt.Sprintf("ExecStart=%s run", binaryPath))
			foundExecStart = true
		} else if strings.HasPrefix(trimmed, "ConditionFileIsExecutable=") {
			newLines = append(newLines, fmt.Sprintf("ConditionFileIsExecutable=%s", binaryPath))
		} else {
			newLines = append(newLines, line)
		}
	}

	if !foundExecStart {
		return fmt.Errorf("ExecStart line not found in service file")
	}

	newContent := strings.Join(newLines, "\n")
	if err := os.WriteFile(servicePath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write service file: %w", err)
	}

	cmd := exec.Command("systemctl", "daemon-reload")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("daemon-reload failed: %s: %w", string(output), err)
	}
	return nil
}
