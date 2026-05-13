package agentmanager

import (
	"fmt"
	"strings"
)

const (
	// Base URL for migrate_agent binaries in Google Cloud Storage
	MigrateAgentBaseURL = "https://storage.googleapis.com/utmstack-updates/migration-tool-v10-to-v11"
)

type PlatformInfo struct {
	OS           string // linux, windows, darwin
	Architecture string // amd64, arm64
	IsWindows    bool
}

func ParsePlatformBasic(agent *Agent) PlatformInfo {
	os := strings.ToLower(agent.Os)

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

func GetMigrateAgentPath(info PlatformInfo) string {
	if info.IsWindows {
		return fmt.Sprintf("C:\\Windows\\Temp\\%s", GetMigrateAgentFilename(info))
	}
	return fmt.Sprintf("/tmp/%s", GetMigrateAgentFilename(info))
}

func GetDownloadCommand(info PlatformInfo) string {
	url := GetMigrateAgentURL(info)
	filename := GetMigrateAgentFilename(info)

	if info.IsWindows {
		return fmt.Sprintf(
			`curl.exe -sSL -o C:\Windows\Temp\%s %s`,
			filename, url,
		)
	}

	return fmt.Sprintf(
		`curl -sSL -o /tmp/%s '%s' && chmod +x /tmp/%s`,
		filename, url, filename,
	)
}

func GetVerifyDownloadCommand(info PlatformInfo) string {
	filename := GetMigrateAgentFilename(info)

	if info.IsWindows {
		return fmt.Sprintf(`if exist C:\Windows\Temp\%s (echo OK) else (echo ERROR: File not found)`, filename)
	}

	return fmt.Sprintf(`if [ -f /tmp/%s ]; then echo "OK"; else echo "ERROR: File not found"; fi`, filename)
}

func GetInstallCommand(info PlatformInfo) string {
	filename := GetMigrateAgentFilename(info)

	if info.IsWindows {
		return fmt.Sprintf(`C:\Windows\Temp\%s install`, filename)
	}

	return fmt.Sprintf(`/tmp/%s install`, filename)
}

func GetVerifyInstallCommand(info PlatformInfo) string {
	if info.IsWindows {
		return `sc query UTMStackMigration >nul 2>&1 && echo OK || echo ERROR: Service not installed`
	}

	if info.OS == "darwin" {
		return `if launchctl list | grep -q UTMStackMigration 2>/dev/null || [ -f /Library/LaunchDaemons/UTMStackMigration.plist ]; then echo "OK"; else echo "ERROR: Service not installed"; fi`
	}

	return `if systemctl is-enabled UTMStackMigration 2>/dev/null || [ -f /etc/systemd/system/UTMStackMigration.service ]; then echo "OK"; else echo "ERROR: Service not installed"; fi`
}
