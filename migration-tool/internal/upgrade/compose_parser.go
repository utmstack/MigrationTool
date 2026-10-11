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

// PostgresYAMLServiceName is the service key in the compose.yml written by the
// UTMStack installer ("postgres" in both V11 and V12). The Docker Swarm
// service name is prefixed with the stack: utmstack_postgres.
const PostgresYAMLServiceName = "postgres"

// composeYMLPaths returns the candidate locations of the live compose.yml,
// ordered by likelihood. The V11 installer and a V12 first install write it
// next to the installer binary (in the operator's home dir); after an updater
// self-copy the live one can sit next to /usr/local/bin/utmstack_installer.
// The last entry covers the case where the installer ran as root in local mode.
func (u *Upgrader) composeYMLPaths() []string {
	homePath := config.GetHomeDir(u.sourceCfg.SSHUser) + "/compose.yml"
	paths := []string{homePath, config.RootComposeYMLPath}
	if homePath != "/root/compose.yml" {
		paths = append(paths, "/root/compose.yml")
	}
	return paths
}

func (u *Upgrader) GetPostgresPasswordFromCompose() (string, error) {
	var composeContent []byte
	var lastErr error
	for _, path := range u.composeYMLPaths() {
		composeContent, lastErr = u.executor.ReadFile(path)
		if composeContent != nil {
			break
		}
	}
	if composeContent == nil {
		return "", fmt.Errorf("failed to read compose.yml in %s: %w", strings.Join(u.composeYMLPaths(), ", "), lastErr)
	}

	var compose ComposeFile
	if err := yaml.Unmarshal(composeContent, &compose); err != nil {
		return "", fmt.Errorf("failed to parse compose.yml: %w", err)
	}

	postgresService, ok := compose.Services[PostgresYAMLServiceName]
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
