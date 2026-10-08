package upgrade

import (
	"fmt"
	"strings"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"gopkg.in/yaml.v3"
)

type ComposeFile struct {
	Services map[string]Service `yaml:"services"`
}

type Service struct {
	Environment []string `yaml:"environment"`
}

// PostgresServiceName is the name of the PostgreSQL service in the UTMStack
// V12 compose file. Confirmed by the `docker service update ... utmstack_postgres`
// port-management commands used throughout the tool.
const PostgresServiceName = "utmstack_postgres"

func (u *Upgrader) GetPostgresPasswordFromCompose() (string, error) {
	composePath := config.GetComposeYMLPath(u.sourceCfg.SSHUser)
	composeContent, err := u.executor.ReadFile(composePath)
	if err != nil {
		return "", fmt.Errorf("failed to read compose.yml at %s: %w", composePath, err)
	}

	var compose ComposeFile
	if err := yaml.Unmarshal(composeContent, &compose); err != nil {
		return "", fmt.Errorf("failed to parse compose.yml: %w", err)
	}

	postgresService, ok := compose.Services[PostgresServiceName]
	if !ok {
		return "", fmt.Errorf("postgres service not found in compose.yml")
	}

	for _, env := range postgresService.Environment {
		if strings.HasPrefix(env, "POSTGRES_PASSWORD=") {
			password := strings.TrimPrefix(env, "POSTGRES_PASSWORD=")
			return password, nil
		}
	}

	return "", fmt.Errorf("POSTGRES_PASSWORD not found in compose.yml")
}
