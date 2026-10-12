package compose

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
// ordered by likelihood. After an updater self-copy the live one can sit
// next to /usr/local/bin/utmstack_installer; the last entry covers the case
// where the installer ran as root in local mode.
func composeYMLPaths(sshUser string) []string {
	homePath := config.GetComposeYMLPath(sshUser)
	paths := []string{homePath, config.RootComposeYMLPath}
	if homePath != "/root/compose.yml" {
		paths = append(paths, "/root/compose.yml")
	}
	return paths
}

func CandidatePathForUser(sshUser string) string {
	return config.GetComposeYMLPath(sshUser)
}

func ReadComposeBytes(exec ReadFileExecutor, sshUser string) ([]byte, error) {
	var lastErr error
	for _, p := range composeYMLPaths(sshUser) {
		b, err := exec.ReadFile(p)
		if err == nil && len(b) > 0 {
			return b, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("could not read compose.yml (tried %v): %w", composeYMLPaths(sshUser), lastErr)
}

// FindComposeEnvVar returns the value of varName from the environment of
// serviceKey in the given compose file. Environment entries are "KEY=VALUE"
// strings.
func FindComposeEnvVar(content []byte, serviceKey, varName string) (string, error) {
	var compose ComposeFile
	if err := yaml.Unmarshal(content, &compose); err != nil {
		return "", fmt.Errorf("failed to parse compose.yml: %w", err)
	}
	svc, ok := compose.Services[serviceKey]
	if !ok {
		return "", fmt.Errorf("service %q not found in compose.yml", serviceKey)
	}
	for _, env := range svc.Environment {
		if key, val, found := strings.Cut(env, "="); found && key == varName {
			return val, nil
		}
	}
	return "", fmt.Errorf("%s not found in service %q", varName, serviceKey)
}

// ReadFileExecutor is the subset of remote.CommandExecutor used to pull
// files off a host, so the compose helpers work with both SSH and local
// executors.
type ReadFileExecutor interface {
	ReadFile(path string) ([]byte, error)
}
