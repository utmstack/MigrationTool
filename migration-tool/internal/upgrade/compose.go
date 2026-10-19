package upgrade

import (
	"fmt"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/compose"
)

func (u *Upgrader) GetPostgresPasswordFromCompose() (string, error) {
	composeContent, err := compose.ReadComposeBytes(u.executor, u.sourceCfg.SSHUser)
	if err != nil {
		return "", err
	}
	return compose.FindComposeEnvVar(composeContent, compose.PostgresYAMLServiceName, "POSTGRES_PASSWORD")
}

// captureOS reads the V11 OpenSearch image and admin password from the live
// compose.yml. They are needed after cleanup and V12 install, when the
// transient OpenSearch node boots over the preserved data volume.
func (u *Upgrader) captureOS() (image, password string, err error) {
	composeContent, readErr := compose.ReadComposeBytes(u.executor, u.sourceCfg.SSHUser)
	if readErr != nil {
		return "", "", readErr
	}
	image, err = compose.FindComposeServiceImage(composeContent, compose.OpenSearchYAMLServiceName)
	if err != nil {
		return "", "", fmt.Errorf("OpenSearch image in V11 compose: %w", err)
	}
	for _, varName := range []string{"OPENSEARCH_INITIAL_ADMIN_PASSWORD", "ELASTICSEARCH_PASSWORD"} {
		password, err = compose.FindComposeEnvVar(composeContent, compose.OpenSearchYAMLServiceName, varName)
		if err == nil {
			return image, password, nil
		}
	}
	return "", "", fmt.Errorf("OpenSearch admin password in V11 compose: %w", err)
}
