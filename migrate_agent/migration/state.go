package migration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/utmstack/MigrationTool/config"
)

// State represents the current migration state
type State int

const (
	StateIdle State = iota
	StateDetected
	StateUninstalling
	StateDownloading
	StateInstalling
	StateVerifying
	StateSelfUninstall
	StateDone
	StateFailed
)

// String returns a human-readable state name
func (s State) String() string {
	names := []string{
		"idle",
		"detected",
		"uninstalling",
		"downloading",
		"installing",
		"verifying",
		"self_uninstall",
		"done",
		"failed",
	}
	if int(s) < len(names) {
		return names[s]
	}
	return "unknown"
}

// StateFile holds the persisted migration state
type StateFile struct {
	State      State   `json:"state"`
	Version    string  `json:"version,omitempty"`
	AgentID    uint32  `json:"agent_id,omitempty"`
	LastError  string  `json:"last_error,omitempty"`
	RetryCount int     `json:"retry_count,omitempty"`
	Connection string  `json:"connection,omitempty"`
}

// LoadState reads the state file from disk
func LoadState() (*StateFile, error) {
	statePath := config.MigrationStatePath()

	data, err := os.ReadFile(statePath)
	if os.IsNotExist(err) {
		return &StateFile{State: StateIdle}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var sf StateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		// Corrupted state file, start fresh
		return &StateFile{State: StateIdle}, nil
	}

	return &sf, nil
}

// Save persists the state file to disk
func (sf *StateFile) Save() error {
	statePath := config.MigrationStatePath()

	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if err := os.WriteFile(statePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	return nil
}

// SetState updates the state and saves to disk
func (sf *StateFile) SetState(s State) error {
	sf.State = s
	sf.LastError = ""
	return sf.Save()
}

// SetError records an error and saves to disk
func (sf *StateFile) SetError(err error) error {
	sf.LastError = err.Error()
	sf.RetryCount++
	return sf.Save()
}
