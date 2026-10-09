package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
	"gopkg.in/yaml.v3"
)

func LoadConfig(path string) (*domain.MigrationConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg domain.MigrationConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if len(cfg.Databases) == 0 {
		cfg.Databases = domain.GetDefaultMigrationTables()
	}
	if cfg.OutputFile == "" {
		cfg.OutputFile = "migration-backup.yml"
	}

	return &cfg, nil
}

func CompleteInstanceConfig(ic *domain.InstanceConfig, name string) error {
	if ic.Port == 0 {
		ic.Port = DefaultPostgresPort
	}
	if ic.User == "" {
		ic.User = DefaultPostgresUser
	}
	if ic.SSLMode == "" {
		ic.SSLMode = DefaultSSLMode
	}
	if ic.SSHPort == 0 {
		ic.SSHPort = DefaultSSHPort
	}
	if ic.IntegrationsConfigDir == "" {
		ic.IntegrationsConfigDir = DefaultIntegrationsConfigDir
	}

	if ic.Password != "" {
		return nil
	}

	if remote.IsLocalhost(ic.Host) {
		password, err := readPostgresPasswordFromCompose()
		if err != nil {
			return fmt.Errorf("failed to auto-detect PostgreSQL password: %w", err)
		}
		ic.Password = password
		ui.Success(fmt.Sprintf("%s: PostgreSQL password auto-detected (local)", name))
		return nil
	}

	if ic.SSHUser == "" {
		return fmt.Errorf("%s: ssh_user is required for remote instances", name)
	}
	if ic.SSHPassword == "" && ic.SSHPrivateKey == "" {
		return fmt.Errorf("%s: ssh_password or ssh_private_key is required for remote instances", name)
	}

	ui.StartOperation(fmt.Sprintf("Connecting to %s via SSH to retrieve PostgreSQL password", name))
	sshClient, err := remote.NewSSHClient(remote.SSHConfig{
		Host:       ic.Host,
		Port:       ic.SSHPort,
		User:       ic.SSHUser,
		Password:   ic.SSHPassword,
		PrivateKey: ic.SSHPrivateKey,
	})
	if err != nil {
		return fmt.Errorf("%s: failed to connect via SSH: %w", name, err)
	}
	defer sshClient.Close()

	password, err := readPostgresPasswordViaSSH(sshClient, ic.SSHUser)
	if err != nil {
		return fmt.Errorf("%s: failed to read PostgreSQL password: %w", name, err)
	}

	ic.Password = password
	ui.Success(fmt.Sprintf("%s: PostgreSQL password retrieved via SSH", name))
	return nil
}

func readPostgresPasswordViaSSH(sshClient *remote.SSHClient, sshUser string) (string, error) {
	homeDir := GetHomeDir(sshUser)

	paths := []string{
		homeDir + "/compose.yml",
		RootComposeYMLPath,
	}

	var composeContent []byte
	var triedPaths []string
	for _, path := range paths {
		composeContent, _ = sshClient.ReadFile(path)
		if composeContent != nil {
			break
		}
		triedPaths = append(triedPaths, path)
	}

	if composeContent == nil {
		return "", fmt.Errorf("compose.yml not found in: %s", strings.Join(triedPaths, ", "))
	}

	return extractPostgresPassword(composeContent)
}

func extractPostgresPassword(composeContent []byte) (string, error) {
	var compose struct {
		Services map[string]struct {
			Environment []string `yaml:"environment"`
		} `yaml:"services"`
	}

	if err := yaml.Unmarshal(composeContent, &compose); err != nil {
		return "", fmt.Errorf("failed to parse compose.yml: %w", err)
	}

	postgresService, ok := compose.Services["postgres"]
	if !ok {
		return "", fmt.Errorf("postgres service not found in compose.yml")
	}

	for _, env := range postgresService.Environment {
		if strings.HasPrefix(env, "POSTGRES_PASSWORD=") {
			return strings.TrimPrefix(env, "POSTGRES_PASSWORD="), nil
		}
	}

	return "", fmt.Errorf("POSTGRES_PASSWORD not found in compose.yml")
}

func ValidateConfigForMigrate(cfg *domain.MigrationConfig) error {
	if cfg.Source.Host == "" {
		return fmt.Errorf("source: host is required")
	}
	if cfg.Destination.Host == "" {
		return fmt.Errorf("destination: host is required")
	}

	ui.StartOperation("Auto-detecting PostgreSQL credentials")
	if err := CompleteInstanceConfig(&cfg.Source, "source"); err != nil {
		return err
	}
	if err := CompleteInstanceConfig(&cfg.Destination, "destination"); err != nil {
		return err
	}

	return nil
}

func ValidateConfigForUpgrade(cfg *domain.MigrationConfig) error {
	if remote.IsLocalhost(cfg.Source.Host) {
		if cfg.Source.Port == 0 {
			cfg.Source.Port = DefaultPostgresPort
		}
		if cfg.Source.User == "" {
			cfg.Source.User = DefaultPostgresUser
		}
		if cfg.Source.SSLMode == "" {
			cfg.Source.SSLMode = DefaultSSLMode
		}
		if cfg.Source.Password == "" {
			return fmt.Errorf("source: password is required")
		}
		return nil
	}

	if cfg.Source.Host == "" {
		return fmt.Errorf("source: host is required")
	}

	ui.StartOperation("Auto-detecting PostgreSQL credentials")
	if err := CompleteInstanceConfig(&cfg.Source, "source"); err != nil {
		return err
	}

	return nil
}

func ValidateConfigForExport(cfg *domain.MigrationConfig) error {
	if cfg.Source.Host == "" {
		return fmt.Errorf("source: host is required")
	}

	ui.StartOperation("Auto-detecting PostgreSQL credentials")
	if err := CompleteInstanceConfig(&cfg.Source, "source"); err != nil {
		return err
	}

	return nil
}

func ValidateConfigForImport(cfg *domain.MigrationConfig) error {
	if cfg.Destination.Host == "" {
		return fmt.Errorf("destination: host is required")
	}

	ui.StartOperation("Auto-detecting PostgreSQL credentials")
	if err := CompleteInstanceConfig(&cfg.Destination, "destination"); err != nil {
		return err
	}

	return nil
}

func LoadLocalUpgradeConfig() (*domain.MigrationConfig, error) {
	password, err := readPostgresPasswordFromCompose()
	if err != nil {
		return nil, fmt.Errorf("failed to auto-detect PostgreSQL password: %w", err)
	}

	sshUser := detectLocalUser()

	cfg := &domain.MigrationConfig{
		Source: domain.InstanceConfig{
			Host:     "127.0.0.1",
			Port:     DefaultPostgresPort,
			User:     DefaultPostgresUser,
			Password: password,
			SSLMode:  DefaultSSLMode,
			SSHUser:  sshUser,
			SSHPort:  DefaultSSHPort,
		},
		Databases: domain.GetDefaultMigrationTables(),
	}

	return cfg, nil
}

func readPostgresPasswordFromCompose() (string, error) {
	paths := []string{
		RootComposeYMLPath,
		"/home/utmstack/compose.yml",
	}

	if homeDir, err := os.UserHomeDir(); err == nil {
		paths = append([]string{homeDir + "/compose.yml"}, paths...)
	}

	var composeContent []byte
	var err error
	for _, path := range paths {
		composeContent, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}

	if composeContent == nil {
		return "", fmt.Errorf("compose.yml not found in any of: %v", paths)
	}

	return extractPostgresPassword(composeContent)
}

func detectLocalUser() string {
	if user := os.Getenv("SUDO_USER"); user != "" {
		return user
	}
	if user := os.Getenv("USER"); user != "" {
		return user
	}
	return ""
}

func ReadInternalKey() (string, error) {
	content, err := os.ReadFile(UTMStackYMLPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", UTMStackYMLPath, err)
	}

	return extractInternalKey(content)
}

func ReadInternalKeyViaSSH(sshClient *remote.SSHClient) (string, error) {
	content, err := sshClient.ReadFile(UTMStackYMLPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s via SSH: %w", UTMStackYMLPath, err)
	}

	return extractInternalKey(content)
}

func extractInternalKey(content []byte) (string, error) {
	var config map[string]any
	if err := yaml.Unmarshal(content, &config); err != nil {
		return "", fmt.Errorf("failed to parse YAML: %w", err)
	}

	key, ok := config["internal_key"].(string)
	if !ok || key == "" {
		return "", fmt.Errorf("internal_key not found in utmstack.yml")
	}

	return strings.TrimSpace(key), nil
}
