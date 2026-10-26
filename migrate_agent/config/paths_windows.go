//go:build windows

package config

import (
	"runtime"
)

// AgentPath returns the UTMStack agent installation directory
func AgentPath() string {
	return `C:\Program Files\UTMStack\UTMStack Agent\`
}

// BinaryName returns the agent binary name for this platform
func BinaryName() string {
	switch runtime.GOARCH {
	case "arm64":
		return "utmstack_agent_service_windows_arm64.exe"
	default:
		return "utmstack_agent_service_windows_amd64.exe"
	}
}

// MigrationPath returns the migration service installation directory
func MigrationPath() string {
	return `C:\Program Files\UTMStack\Migration\`
}

// MigrationLogPath returns the migration service log directory
func MigrationLogPath() string {
	return `C:\Program Files\UTMStack\Migration\logs\`
}

// MigrationStatePath returns the path to the migration state file
func MigrationStatePath() string {
	return `C:\Program Files\UTMStack\Migration\state.json`
}

// SystemdServicePath returns empty on Windows (not used)
func SystemdServicePath() string {
	return ""
}

// ForwarderPath is not used on Windows (forwarder is Linux-only).
func ForwarderPath() string {
	return ""
}
