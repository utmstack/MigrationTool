//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"migrate_agent/config"
	"migrate_agent/utils"
)

const (
	// launchd service label for UTMStack Agent (matches kardianos/service Name from serv/config.go)
	agentServiceLabel = "UTMStackAgent"
)

// StopAgentService stops the UTMStack Agent service using launchctl
func StopAgentService() error {
	if utils.Logger != nil {
		utils.Logger.Info("Stopping %s service", config.AgentServiceName)
	}

	plistPath := config.LaunchdPlistPath()

	// Use launchctl unload to stop the service
	cmd := exec.Command("launchctl", "unload", plistPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Service might already be unloaded
		if !strings.Contains(string(output), "Could not find specified service") {
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

// StartAgentService starts the UTMStack Agent service using launchctl
func StartAgentService() error {
	if utils.Logger != nil {
		utils.Logger.Info("Starting %s service", config.AgentServiceName)
	}

	plistPath := config.LaunchdPlistPath()

	// Use launchctl load to start the service
	cmd := exec.Command("launchctl", "load", plistPath)
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
	// Use launchctl list to check if service is loaded and running
	cmd := exec.Command("launchctl", "list", agentServiceLabel)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	// If the service is running, launchctl list will show the PID
	// Format: PID	Status	Label
	// If PID is "-" or missing, service is not running
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, agentServiceLabel) {
			fields := strings.Fields(line)
			if len(fields) >= 1 && fields[0] != "-" {
				// PID is present, service is running
				return true
			}
		}
	}
	return false
}

// RemoveLogsCollectorService is a no-op on macOS (filebeat/logs collector doesn't exist)
func RemoveLogsCollectorService() error {
	// No logs collector service on macOS - nothing to remove
	return nil
}

// UpdateServiceExecPath updates the ProgramArguments in the launchd plist file
func UpdateServiceExecPath(binaryPath string) error {
	if utils.Logger != nil {
		utils.Logger.Info("Updating launchd plist file")
	}

	plistPath := config.LaunchdPlistPath()

	// Read current plist file
	content, err := os.ReadFile(plistPath)
	if err != nil {
		return fmt.Errorf("failed to read plist file: %w", err)
	}

	// Parse and update the plist
	// The plist is XML, we need to find the ProgramArguments array and update it
	// For simplicity, we'll do string manipulation since the structure is predictable

	lines := strings.Split(string(content), "\n")
	var newLines []string
	inProgramArgs := false
	foundProgram := false
	skipNextString := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "<key>ProgramArguments</key>" {
			inProgramArgs = true
			newLines = append(newLines, line)
			continue
		}

		if inProgramArgs {
			if trimmed == "<array>" {
				newLines = append(newLines, line)
				continue
			}
			if trimmed == "</array>" {
				inProgramArgs = false
				newLines = append(newLines, line)
				continue
			}
			// First string in array is the program path
			if strings.HasPrefix(trimmed, "<string>") && !foundProgram {
				// Replace with new binary path
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				newLines = append(newLines, fmt.Sprintf("%s<string>%s</string>", indent, binaryPath))
				foundProgram = true
				continue
			}
			// Skip the "run" argument line and re-add it
			if strings.HasPrefix(trimmed, "<string>") && foundProgram && !skipNextString {
				// Keep the "run" argument as is
				newLines = append(newLines, line)
				skipNextString = true
				continue
			}
		}

		// Check for Program key (alternative format)
		if trimmed == "<key>Program</key>" {
			newLines = append(newLines, line)
			// Next line should be the string value
			if i+1 < len(lines) {
				nextTrimmed := strings.TrimSpace(lines[i+1])
				if strings.HasPrefix(nextTrimmed, "<string>") {
					indent := lines[i+1][:len(lines[i+1])-len(strings.TrimLeft(lines[i+1], " \t"))]
					newLines = append(newLines, fmt.Sprintf("%s<string>%s</string>", indent, binaryPath))
					// Skip the original line in next iteration
					continue
				}
			}
		}

		newLines = append(newLines, line)
	}

	// Write updated plist file
	newContent := strings.Join(newLines, "\n")
	if err := os.WriteFile(plistPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write plist file: %w", err)
	}

	if utils.Logger != nil {
		utils.Logger.Info("Plist file updated successfully")
	}
	return nil
}
