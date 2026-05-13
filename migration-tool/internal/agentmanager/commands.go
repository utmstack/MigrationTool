package agentmanager

import (
	"fmt"
	"strings"
)

const (
	// Base URL for migrate_agent binaries in Google Cloud Storage
	MigrateAgentBaseURL = "https://storage.googleapis.com/utmstack-updates/migration-tool-v10-to-v11"

	migrateAgentDirUnix    = "/opt/utmstack-migration"
	migrateAgentDirWindows = `C:\UTMStack\UTMStackMigrateAgent`
)

type PlatformInfo struct {
	OS           string // linux, windows, darwin
	Architecture string // amd64, arm64
	IsWindows    bool
}

func ParsePlatformBasic(agent *Agent) PlatformInfo {
	raw := strings.ToLower(strings.TrimSpace(agent.Os))

	// Normalize OS variants reported by agents (e.g. "macOS", "Mac OS X",
	// "darwin", "ubuntu", "win32") into the three canonical values used by
	// the migrate_agent binary naming scheme.
	var os string
	switch {
	case strings.Contains(raw, "windows"), strings.Contains(raw, "win"):
		os = "windows"
	case strings.Contains(raw, "darwin"),
		strings.Contains(raw, "mac"),
		strings.Contains(raw, "osx"):
		os = "darwin"
	default:
		os = "linux"
	}

	return PlatformInfo{
		OS:           os,
		IsWindows:    os == "windows",
		Architecture: "", // Will be detected via command
	}
}

func ParseArchitectureFromOutput(output string) string {
	output = strings.ToLower(strings.TrimSpace(output))

	switch {
	case strings.Contains(output, "aarch64"),
		strings.Contains(output, "arm64"),
		strings.Contains(output, "arm"):
		return "arm64"
	case strings.Contains(output, "x86_64"),
		strings.Contains(output, "amd64"),
		strings.Contains(output, "x64"),
		strings.Contains(output, "64-bit"),
		strings.Contains(output, "64"):
		return "amd64"
	default:
		return "amd64"
	}
}

func GetArchDetectCommand(info PlatformInfo) string {
	if info.IsWindows {
		return `wmic os get osarchitecture /value`
	}
	return `uname -m`
}

func GetMigrateAgentURL(info PlatformInfo) string {
	return fmt.Sprintf("%s/%s", MigrateAgentBaseURL, GetMigrateAgentFilename(info))
}

func GetMigrateAgentFilename(info PlatformInfo) string {
	switch info.OS {
	case "windows":
		return fmt.Sprintf("migrate_agent_windows_%s.exe", info.Architecture)
	case "darwin":
		return fmt.Sprintf("migrate_agent_darwin_%s", info.Architecture)
	default:
		return fmt.Sprintf("migrate_agent_linux_%s", info.Architecture)
	}
}

// GetMigrateAgentPath returns the absolute path where the migrate_agent
// binary is stored on the target platform. The binary lives in the same
// directory that migrate_agent itself uses for its installation (see
// migrate_agent/config.MigrationPath), so logs, state and binary stay
// together instead of leaking into /tmp.
func GetMigrateAgentPath(info PlatformInfo) string {
	filename := GetMigrateAgentFilename(info)
	if info.IsWindows {
		return fmt.Sprintf(`%s\%s`, migrateAgentDirWindows, filename)
	}
	return fmt.Sprintf("%s/%s", migrateAgentDirUnix, filename)
}

// GetDownloadCommand returns the shell command executed on the remote agent
// to download the migrate_agent binary into its final installation directory.
// The directory is created (idempotent) before downloading, and the binary
// is marked executable on Unix systems. Requires root/admin on the remote.
func GetDownloadCommand(info PlatformInfo) string {
	url := GetMigrateAgentURL(info)
	path := GetMigrateAgentPath(info)

	if info.IsWindows {
		return fmt.Sprintf(
			`mkdir %s 2>nul & curl.exe -sSL -o %s %s`,
			migrateAgentDirWindows, path, url,
		)
	}

	return fmt.Sprintf(
		`mkdir -p %s && curl -sSL -o '%s' '%s' && chmod +x '%s'`,
		migrateAgentDirUnix, path, url, path,
	)
}

// GetVerifyDownloadCommand returns the command used to confirm the binary
// landed where we expected it (final install directory, not /tmp).
func GetVerifyDownloadCommand(info PlatformInfo) string {
	path := GetMigrateAgentPath(info)

	if info.IsWindows {
		return fmt.Sprintf(`if exist %s (echo OK) else (echo ERROR: File not found)`, path)
	}

	return fmt.Sprintf(`if [ -f '%s' ]; then echo "OK"; else echo "ERROR: File not found"; fi`, path)
}

// GetInstallCommand executes the migrate_agent binary's `install` subcommand
// from its final location. The binary will register the system service and
// create the log directory itself.
func GetInstallCommand(info PlatformInfo) string {
	path := GetMigrateAgentPath(info)

	if info.IsWindows {
		return fmt.Sprintf(`%s install`, path)
	}

	return fmt.Sprintf(`'%s' install`, path)
}

// GetVerifyInstallCommand returns a per-platform command that confirms the
// migrate_agent service was registered. The service name MUST match
// migrate_agent/config.MigrationServiceName ("UTMStackMigrateAgent"). If you
// rename it there, rename it here too — they live in separate Go modules and
// cannot import each other.
func GetVerifyInstallCommand(info PlatformInfo) string {
	if info.IsWindows {
		return `sc query UTMStackMigrateAgent >nul 2>&1 && echo OK || echo ERROR: Service not installed`
	}

	if info.OS == "darwin" {
		return `if launchctl list | grep -q UTMStackMigrateAgent 2>/dev/null || [ -f /Library/LaunchDaemons/UTMStackMigrateAgent.plist ]; then echo "OK"; else echo "ERROR: Service not installed"; fi`
	}

	return `if systemctl is-enabled UTMStackMigrateAgent 2>/dev/null || [ -f /etc/systemd/system/UTMStackMigrateAgent.service ]; then echo "OK"; else echo "ERROR: Service not installed"; fi`
}
