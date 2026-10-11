package config

import "github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"

const (
	DefaultPostgresPort = 5432
	DefaultPostgresUser = "postgres"
	DefaultSSHPort      = 22
	DefaultSSLMode      = "disable"
)

const (
	DefaultDatabase      = domain.DefaultDatabase
	AgentManagerDatabase = domain.AgentManagerDatabase
)

const (
	UTMStackYMLPath    = "/root/utmstack.yml"
	RootComposeYMLPath = "/usr/local/bin/compose.yml"
	// DefaultIntegrationsConfigDir is where V12 loads integration config
	// YAMLs from on the host (mounted into the backend as /workdir/pipeline).
	DefaultIntegrationsConfigDir = "/utmstack/events-engine-workdir/pipeline"
)

func GetHomeDir(user string) string {
	if user == "" || user == "root" {
		return "/root"
	}
	return "/home/" + user
}

func GetComposeYMLPath(user string) string {
	return GetHomeDir(user) + "/compose.yml"
}
