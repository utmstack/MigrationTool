package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// AgentConfig holds the parsed agent configuration
type AgentConfig struct {
	Server   string `yaml:"server"`
	Insecure bool   `yaml:"insecure"`
	AgentID  uint32 `yaml:"agent-id"`
}

// ReadConfig reads and parses the agent's config.yml
func ReadConfig() (*AgentConfig, error) {
	configPath := filepath.Join(AgentPath(), "config.yml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config.yml: %w", err)
	}

	var cfg AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config.yml: %w", err)
	}

	if cfg.Server == "" {
		return nil, fmt.Errorf("server not found in config.yml")
	}

	return &cfg, nil
}

// ReadAgentID returns the numeric agent id from the local config.yml.
func ReadAgentID() (uint32, error) {
	data, err := os.ReadFile(filepath.Join(AgentPath(), "config.yml"))
	if err != nil {
		return 0, fmt.Errorf("failed to read config.yml: %w", err)
	}

	var cfg AgentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return 0, fmt.Errorf("failed to parse config.yml: %w", err)
	}
	return cfg.AgentID, nil
}

// Credentials are baked by the migration tool at deploy time. The panel
// internal key authenticates the bridge against V12 InternalKeyRoutes.
type Credentials struct {
	Server      string `yaml:"server"`
	Insecure    bool   `yaml:"insecure"`
	InternalKey string `yaml:"internal_key"`
}

// CredentialsPath is where the tool writes credentials.yml (MigrationPath()).
func CredentialsPath() string {
	return filepath.Join(MigrationPath(), "credentials.yml")
}

// ReadCredentials loads the tool-provided panel credentials.
func ReadCredentials() (*Credentials, error) {
	data, err := os.ReadFile(CredentialsPath())
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}

	var c Credentials
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}
	if c.Server == "" || c.InternalKey == "" {
		return nil, fmt.Errorf("credentials.yml incomplete (need server and internal_key)")
	}
	return &c, nil
}

// VersionURL returns the panel-served version.json URL.
func VersionURL(c *Credentials) string {
	return fmt.Sprintf("https://%s/private/dependencies/agent/version.json", c.Server)
}

// BinaryDownloadURL returns the panel-served V12 agent binary URL.
func BinaryDownloadURL(c *Credentials) string {
	return fmt.Sprintf("https://%s/private/dependencies/agent/%s", c.Server, BinaryName())
}
