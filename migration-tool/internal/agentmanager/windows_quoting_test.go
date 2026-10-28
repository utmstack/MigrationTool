package agentmanager

import (
	"strings"
	"testing"
)

// "Program Files" contains a space; every Windows command must quote its
// paths or cmd.exe splits them (mkdir creates cwd-relative Files\UTMStack\...).
func TestWindowsCommandsQuotePaths(t *testing.T) {
	pi := PlatformInfo{OS: "windows", Architecture: "amd64", IsWindows: true}

	dl := GetDownloadCommand(pi)
	for _, frag := range []string{
		`mkdir "C:\Program Files\UTMStack\Migration"`,
		`-o "C:\Program Files\UTMStack\Migration\migrate_agent_windows_amd64.exe"`,
		`"https://storage.googleapis.com/`,
	} {
		if !strings.Contains(dl, frag) {
			t.Errorf("download missing quoted fragment %q in:\n%s", frag, dl)
		}
	}

	ver := GetVerifyDownloadCommand(pi)
	if !strings.Contains(ver, `if exist "C:\Program Files\UTMStack\Migration\`) {
		t.Errorf("verify download not quoted:\n%s", ver)
	}

	inst := GetInstallCommand(pi)
	if !strings.Contains(inst, `"C:\Program Files\UTMStack\Migration\migrate_agent_windows_amd64.exe" install`) {
		t.Errorf("install not quoted:\n%s", inst)
	}

	cred := GetWriteCredentialsCommand(pi, "ZGF0YQ==")
	for _, frag := range []string{
		`if not exist "C:\Program Files\UTMStack\Migration" mkdir "C:\Program Files\UTMStack\Migration"`,
		`> "C:\Program Files\UTMStack\Migration\credentials.yml.b64"`,
		`certutil -decode "C:\Program Files\UTMStack\Migration\credentials.yml.b64" "C:\Program Files\UTMStack\Migration\credentials.yml"`,
	} {
		if !strings.Contains(cred, frag) {
			t.Errorf("credentials missing quoted fragment %q in:\n%s", frag, cred)
		}
	}
}

func TestUnixCommandsUnchanged(t *testing.T) {
	pi := PlatformInfo{OS: "linux", Architecture: "amd64"}
	dl := GetDownloadCommand(pi)
	for _, frag := range []string{
		`mkdir -p /opt/utmstack-migration`,
		`-o '/opt/utmstack-migration/migrate_agent_linux_amd64'`,
		`chmod +x '/opt/utmstack-migration/migrate_agent_linux_amd64'`,
	} {
		if !strings.Contains(dl, frag) {
			t.Errorf("linux download missing %q in:\n%s", frag, dl)
		}
	}
	if v := GetVerifyDownloadCommand(pi); !strings.Contains(v, `[ -f '/opt/utmstack-migration/migrate_agent_linux_amd64' ]`) {
		t.Errorf("linux verify changed:\n%s", v)
	}
	if i := GetInstallCommand(pi); i != `'/opt/utmstack-migration/migrate_agent_linux_amd64' install` {
		t.Errorf("linux install changed:\n%s", i)
	}
}
