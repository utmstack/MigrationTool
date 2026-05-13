package migration

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/utmstack/MigrationTool/config"
	"github.com/utmstack/MigrationTool/utils"
)

// atomicWriteFile writes data to a file atomically.
// It writes to a temp file first, syncs to disk, then renames to the target.
// This ensures the target file is never corrupted on crash or disk-full scenarios.
// Works on Linux, macOS, and Windows (all architectures).
func atomicWriteFile(filename string, data []byte, perm fs.FileMode) error {
	// Create temp file in the same directory (required for atomic rename on same filesystem)
	dir := filepath.Dir(filename)
	tempFile, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	// Cleanup temp file on any error
	success := false
	defer func() {
		if !success {
			tempFile.Close()
			os.Remove(tempPath)
		}
	}()

	// Write data to temp file
	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Sync to ensure data is on disk before rename
	// Critical for crash safety - without this, data might be in OS cache only
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	// Set permissions before rename
	if err := tempFile.Chmod(perm); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Close before rename (required on Windows)
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename - replaces target file
	// On Unix: atomic (single syscall)
	// On Windows: not guaranteed atomic, but safe (target is either old or new, never corrupted)
	if err := os.Rename(tempPath, filename); err != nil {
		return fmt.Errorf("failed to rename temp file to target: %w", err)
	}

	success = true
	return nil
}

// v10 to v11 integration key mapping
// BREAKING CHANGE: "cisco" is NOT migrated - user must reconfigure manually
var integrationKeyMapping = map[string]string{
	"antivirus_eset":           "antivirus-esmc-eset",
	"antivirus_kaspersky":      "antivirus-kaspersky",
	"antivirus_sentinel_one":   "antivirus-sentinel-one",
	"antivirus_deceptivebytes": "deceptive-bytes",
	"firewall_fortinet":        "firewall-fortigate-traffic",
	"firewall_fortiweb":        "firewall-fortiweb",
	"firewall_mikrotik":        "firewall-mikrotik",
	"firewall_paloalto":        "firewall-paloalto",
	"firewall_pfsense":         "firewall-pfsense",
	"firewall_sonicwall":       "firewall-sonicwall",
	"firewall_sophos":          "firewall-sophos-xg",
	"ibm_aix":                  "ibm-aix",
	"vmware":                   "vmware-esxi",
	// Keys that don't change
	"syslog":   "syslog",
	"suricata": "suricata",
	"netflow":  "netflow",
}

// PortConfig represents tcp_port or udp_port configuration
type PortConfig struct {
	Enabled bool   `json:"enabled"`
	Value   string `json:"value"`
}

// IntegrationConfig represents a single integration's port configuration
type IntegrationConfig struct {
	TCPPort *PortConfig `json:"tcp_port,omitempty"`
	UDPPort *PortConfig `json:"udp_port,omitempty"`
}

// FileIntegrationConfig represents file-based integration config (v11 only)
type FileIntegrationConfig struct {
	Enabled bool     `json:"enabled"`
	Paths   []string `json:"paths"`
}

// CollectorConfig represents the log-collector-config.json structure
type CollectorConfig struct {
	Integrations     map[string]IntegrationConfig     `json:"integrations"`
	FileIntegrations map[string]FileIntegrationConfig `json:"file_integrations,omitempty"`
}

// MigrateCollectorConfig migrates log-collector-config.json from v10 to v11 format
// Returns true if migration was performed, false if skipped (already v11 or not found)
func MigrateCollectorConfig() (bool, error) {
	configPath := filepath.Join(config.AgentPath(), "log-collector-config.json")

	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if utils.Logger != nil {
			utils.Logger.Info("log-collector-config.json not found, skipping migration")
		}
		return false, nil
	}

	// Read current config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, fmt.Errorf("failed to read collector config: %w", err)
	}

	var oldConfig CollectorConfig
	if err := json.Unmarshal(data, &oldConfig); err != nil {
		return false, fmt.Errorf("failed to parse collector config: %w", err)
	}

	// Check if already v11 format (has file_integrations or uses kebab-case keys)
	if oldConfig.FileIntegrations != nil {
		if utils.Logger != nil {
			utils.Logger.Info("Collector config already in v11 format, skipping migration")
		}
		return false, nil
	}

	// Check for v11 keys (kebab-case)
	for key := range oldConfig.Integrations {
		if key == "antivirus-esmc-eset" || key == "firewall-fortigate-traffic" || key == "vmware-esxi" {
			if utils.Logger != nil {
				utils.Logger.Info("Collector config already in v11 format (detected v11 keys), skipping migration")
			}
			return false, nil
		}
	}

	// Perform migration
	newConfig := CollectorConfig{
		Integrations:     make(map[string]IntegrationConfig),
		FileIntegrations: make(map[string]FileIntegrationConfig),
	}

	// Initialize v11 default file integrations
	newConfig.FileIntegrations["nginx"] = FileIntegrationConfig{
		Enabled: false,
		Paths:   []string{"/var/log/nginx/access.log", "/var/log/nginx/error.log"},
	}
	newConfig.FileIntegrations["postgresql"] = FileIntegrationConfig{
		Enabled: false,
		Paths:   []string{"/var/log/postgresql/postgresql-*-main.log"},
	}

	var skippedKeys []string
	var migratedKeys []string

	for oldKey, integrationCfg := range oldConfig.Integrations {
		// BREAKING CHANGE: cisco is not migrated
		if oldKey == "cisco" {
			skippedKeys = append(skippedKeys, oldKey)
			if utils.Logger != nil {
				utils.Logger.Info("BREAKING CHANGE: 'cisco' integration skipped - user must reconfigure as cisco-switch, firewall-cisco-asa, or firewall-cisco-firepower")
			}
			continue
		}

		newKey, exists := integrationKeyMapping[oldKey]
		if !exists {
			// Unknown key, preserve as-is (might be custom)
			newKey = oldKey
			if utils.Logger != nil {
				utils.Logger.Info("Unknown integration key '%s' preserved as-is", oldKey)
			}
		}

		newConfig.Integrations[newKey] = integrationCfg
		if oldKey != newKey {
			migratedKeys = append(migratedKeys, fmt.Sprintf("%s -> %s", oldKey, newKey))
		}
	}

	// Add new v11 integrations that didn't exist in v10 (disabled by default)
	newV11Integrations := map[string]IntegrationConfig{
		"cisco-switch": {
			TCPPort: &PortConfig{Enabled: false, Value: "1470"},
			UDPPort: &PortConfig{Enabled: false, Value: "514"},
		},
		"firewall-cisco-asa": {
			TCPPort: &PortConfig{Enabled: false, Value: "1470"},
			UDPPort: &PortConfig{Enabled: false, Value: "514"},
		},
		"firewall-cisco-firepower": {
			TCPPort: &PortConfig{Enabled: false, Value: "1470"},
			UDPPort: &PortConfig{Enabled: false, Value: "514"},
		},
		"firewall-meraki": {
			TCPPort: &PortConfig{Enabled: false, Value: "1470"},
			UDPPort: &PortConfig{Enabled: false, Value: "514"},
		},
	}

	for key, cfg := range newV11Integrations {
		if _, exists := newConfig.Integrations[key]; !exists {
			newConfig.Integrations[key] = cfg
		}
	}

	// Backup old config (MANDATORY - if this fails, abort migration)
	backupPath := configPath + ".v10.bak"
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return false, fmt.Errorf("failed to backup config to %s: %w (aborting migration to prevent data loss)", backupPath, err)
	}
	if utils.Logger != nil {
		utils.Logger.Info("Backed up old config to %s", backupPath)
	}

	// Marshal new config
	newData, err := json.MarshalIndent(newConfig, "", "    ")
	if err != nil {
		return false, fmt.Errorf("failed to marshal new config: %w", err)
	}

	// Atomic write: write to temp file, sync, then rename
	// This ensures we never corrupt the original config on crash/disk-full
	if err := atomicWriteFile(configPath, newData, 0644); err != nil {
		return false, fmt.Errorf("failed to write new config: %w", err)
	}

	if utils.Logger != nil {
		utils.Logger.Info("Collector config migrated successfully")
		if len(migratedKeys) > 0 {
			utils.Logger.Info("Migrated keys: %v", migratedKeys)
		}
		if len(skippedKeys) > 0 {
			utils.Logger.Info("Skipped keys (BREAKING CHANGE): %v", skippedKeys)
		}
	}

	return true, nil
}
