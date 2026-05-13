//go:build darwin

package config

// AgentPath returns the UTMStack agent installation directory
func AgentPath() string {
	return "/opt/utmstack-macos-agent/"
}

// BinaryName returns the agent binary name for this platform (macOS arm64 only)
func BinaryName() string {
	return "utmstack_agent_service_darwin_arm64"
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

// LaunchdPlistPath returns the path to the agent launchd plist file
func LaunchdPlistPath() string {
	return "/Library/LaunchDaemons/UTMStackAgent.plist"
}

// SystemdServicePath returns empty on macOS (not used, here for interface compatibility)
func SystemdServicePath() string {
	return ""
}
