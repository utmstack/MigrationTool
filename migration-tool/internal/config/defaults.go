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
	RootComposeYMLPath = "/root/compose.yml"
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
