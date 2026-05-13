package yaml

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"gopkg.in/yaml.v3"
)

func ExtractInternalKeyFromYAML(content []byte) (string, error) {
	var config map[string]any
	if err := yaml.Unmarshal(content, &config); err != nil {
		return "", fmt.Errorf("failed to parse YAML: %w", err)
	}

	key, ok := config["internal_key"].(string)
	if !ok {
		return "", fmt.Errorf("internal_key not found in YAML")
	}

	return strings.TrimSpace(key), nil
}

func UpdateInternalKeyInYAML(content []byte, internalKey string) ([]byte, error) {
	var config map[string]any

	if err := yaml.Unmarshal(content, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	config["internal_key"] = internalKey

	output, err := yaml.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal YAML: %w", err)
	}

	return output, nil
}

func WriteBackupToFile(backup *domain.BackupData, filename string) error {
	// Use atomic write pattern: write to temp file, then rename on success
	dir := filepath.Dir(filename)
	if dir == "" {
		dir = "."
	}

	tempFile, err := os.CreateTemp(dir, ".utmstack-backup-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	// Clean up temp file on any error
	success := false
	defer func() {
		if !success {
			os.Remove(tempPath)
		}
	}()

	encoder := yaml.NewEncoder(tempFile)
	encoder.SetIndent(2)

	if err := encoder.Encode(backup); err != nil {
		encoder.Close()
		tempFile.Close()
		return fmt.Errorf("failed to encode YAML: %w", err)
	}

	if err := encoder.Close(); err != nil {
		tempFile.Close()
		return fmt.Errorf("failed to close YAML encoder: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename to target file
	if err := os.Rename(tempPath, filename); err != nil {
		return fmt.Errorf("failed to rename temp file to target: %w", err)
	}

	success = true
	return nil
}

func WriteInternalKey(filePath, internalKey string) error {
	var config map[string]any

	data, err := os.ReadFile(filePath)
	if err == nil {
		if err := yaml.Unmarshal(data, &config); err != nil {
			return fmt.Errorf("failed to parse existing YAML: %w", err)
		}
	} else {
		config = make(map[string]any)
	}

	config["internal_key"] = internalKey

	output, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	// Use atomic write pattern: write to temp file, then rename on success
	dir := filepath.Dir(filePath)
	if dir == "" {
		dir = "."
	}

	tempFile, err := os.CreateTemp(dir, ".utmstack-key-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	// Clean up temp file on any error
	success := false
	defer func() {
		if !success {
			os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(output); err != nil {
		tempFile.Close()
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	if err := tempFile.Chmod(0600); err != nil {
		tempFile.Close()
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename to target file
	if err := os.Rename(tempPath, filePath); err != nil {
		return fmt.Errorf("failed to rename temp file to target: %w", err)
	}

	success = true
	return nil
}
