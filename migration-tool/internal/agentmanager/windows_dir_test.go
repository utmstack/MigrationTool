package agentmanager

import (
	"strings"
	"testing"
)

// The bridge install dir must avoid spaces: a quoted `C:\Program Files\...`
// passed through `cmd.exe /C` is quote-stripped and mangled. C:\UTMStack\
// Migration has no spaces, so every Windows command stays one token.
func TestWindowsBridgeDirNoSpaces(t *testing.T) {
	pi := PlatformInfo{OS: "windows", Architecture: "amd64", IsWindows: true}

	if migrateAgentDirWindows != `C:\UTMStack\Migration` {
		t.Fatalf("migrateAgentDirWindows = %q, want C:\\UTMStack\\Migration", migrateAgentDirWindows)
	}

	for name, c := range map[string]string{
		"download":      GetDownloadCommand(pi),
		"verifyDownload": GetVerifyDownloadCommand(pi),
		"install":       GetInstallCommand(pi),
		"writeCreds":    GetWriteCredentialsCommand(pi, "ZGF0YQ=="),
		"verifyCreds":   GetVerifyCredentialsCommand(pi),
	} {
		if strings.Contains(c, "Program Files") {
			t.Errorf("%s still references Program Files:\n%s", name, c)
		}
		if strings.Contains(c, `C:\UTMStack\Migration`) {
			continue
		}
		t.Errorf("%s missing C:\\UTMStack\\Migration:\n%s", name, c)
	}

	// No quotes are needed now that the path has no spaces; a lone double
	// quote in a Windows command is the classic cmd /C mangle trigger.
	for name, c := range map[string]string{
		"download": GetDownloadCommand(pi),
		"install":  GetInstallCommand(pi),
	} {
		if strings.Contains(c, `"`) {
			t.Errorf("%s must not contain quotes for a space-free path:\n%s", name, c)
		}
	}
}
