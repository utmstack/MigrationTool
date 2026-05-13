//go:build linux

package config

import (
	"runtime"
)

// AgentPath returns the UTMStack agent installation directory
func AgentPath() string {
	return "/opt/utmstack-linux-agent/"
}

// BinaryName returns the agent binary name for this platform
func BinaryName() string {
	switch runtime.GOARCH {
	case "arm64":
		return "utmstack_agent_service_linux_arm64"
	default:
		return "utmstack_agent_service_linux_amd64"
	}
}

// MigrationPath returns the migration service installation directory
func MigrationPath() string {
	return "/opt/utmstack-migration/"
}

// MigrationLogPath returns the migration service log directory
func MigrationLogPath() string {
	return "/opt/utmstack-migration/logs/"
}

// MigrationStatePath returns the path to the migration state file
func MigrationStatePath() string {
	return "/opt/utmstack-migration/state.json"
}

// SystemdServicePath returns the path to the agent systemd service file
func SystemdServicePath() string {
	return "/etc/systemd/system/UTMStackAgent.service"
}
