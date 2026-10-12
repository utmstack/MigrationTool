package upgrade

import (
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/compose"
)

func (u *Upgrader) GetPostgresPasswordFromCompose() (string, error) {
	composeContent, err := compose.ReadComposeBytes(u.executor, u.sourceCfg.SSHUser)
	if err != nil {
		return "", err
	}
	return compose.FindComposeEnvVar(composeContent, compose.PostgresYAMLServiceName, "POSTGRES_PASSWORD")
}
