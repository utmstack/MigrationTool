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

// DownloadURL returns the full URL to download the agent binary
func (c *AgentConfig) DownloadURL() string {
	return fmt.Sprintf("https://%s:9001/private/dependencies/agent/%s", c.Server, BinaryName())
}
