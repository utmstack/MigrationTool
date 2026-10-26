package agentmanager

import (
	"encoding/base64"
	"testing"
)

func TestGetWriteCredentialsCommand(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("server: '10.0.0.5'\ninternal_key: 'secret'\n"))

	tests := []struct {
		name string
		info PlatformInfo
		want string
	}{
		{
			name: "linux decodes into migration dir",
			info: PlatformInfo{OS: "linux", Architecture: "amd64"},
			want: "mkdir -p /opt/utmstack-migration && echo '" + b64 + "' | base64 -d > /opt/utmstack-migration/credentials.yml",
		},
		{
			name: "windows decodes via certutil",
			info: PlatformInfo{OS: "windows", Architecture: "amd64", IsWindows: true},
			want: `if not exist C:\Program Files\UTMStack\Migration mkdir C:\Program Files\UTMStack\Migration & echo ` + b64 + ` > C:\Program Files\UTMStack\Migration\credentials.yml.b64 & certutil -decode C:\Program Files\UTMStack\Migration\credentials.yml.b64 C:\Program Files\UTMStack\Migration\credentials.yml`,
		},
		{
			name: "darwin decodes into migration dir",
			info: PlatformInfo{OS: "darwin", Architecture: "arm64"},
			want: "mkdir -p /opt/utmstack-migration && echo '" + b64 + "' | base64 -d > /opt/utmstack-migration/credentials.yml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetWriteCredentialsCommand(tt.info, b64); got != tt.want {
				t.Errorf("GetWriteCredentialsCommand() =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestGetVerifyCredentialsCommand(t *testing.T) {
	tests := []struct {
		name string
		info PlatformInfo
		want string
	}{
		{
			name: "unix file check",
			info: PlatformInfo{OS: "linux"},
			want: `if [ -f '/opt/utmstack-migration/credentials.yml' ]; then echo "OK"; else echo "ERROR: File not found"; fi`,
		},
		{
			name: "windows exist check",
			info: PlatformInfo{OS: "windows", IsWindows: true},
			want: `if exist C:\Program Files\UTMStack\Migration\credentials.yml (echo OK) else (echo ERROR: File not found)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetVerifyCredentialsCommand(tt.info); got != tt.want {
				t.Errorf("GetVerifyCredentialsCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMigrateAgentCredentialsDir(t *testing.T) {
	if got := MigrateAgentCredentialsDir(PlatformInfo{OS: "linux"}); got != "/opt/utmstack-migration" {
		t.Errorf("linux dir = %q, want /opt/utmstack-migration", got)
	}
	if got := MigrateAgentCredentialsDir(PlatformInfo{OS: "windows", IsWindows: true}); got != `C:\Program Files\UTMStack\Migration` {
		t.Errorf("windows dir = %q, want C:\\Program Files\\UTMStack\\Migration", got)
	}
}
