// Package state persists per-step checkpoints so that migrate and upgrade
// can resume after a failure without repeating completed work.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// File is the checkpoint file name, created in the working directory the tool
// is executed from.
const File = "migration-state.json"

// Step names shared by both flows.
const (
	StepCaptureOS = "capture-os"
	StepExport    = "export"
	StepCleanup   = "cleanup"
	StepInstall   = "install"
	StepImport    = "import"
	StepStream    = "stream"
)

type OSCapture struct {
	Image    string `json:"image"`
	Password string `json:"password"`
}

type State struct {
	Path           string     `json:"-"`
	Flow           string     `json:"flow"`
	Host           string     `json:"host"`
	DestHost       string     `json:"dest_host,omitempty"`
	BackupFile     string     `json:"backup_file"`
	WithData       bool       `json:"with_data"`
	CompletedSteps []string   `json:"completed_steps"`
	OS             *OSCapture `json:"captured_os,omitempty"`
}

// New returns an in-memory state for a fresh run. Nothing is written until
// Save or Mark.
func New(path, flow, host, destHost, backupFile string, withData bool) *State {
	return &State{
		Path:       path,
		Flow:       flow,
		Host:       host,
		DestHost:   destHost,
		BackupFile: backupFile,
		WithData:   withData,
	}
}

// Load reads the state file. A missing file returns (nil, nil): the run has
// no checkpoint yet. A corrupted file is a hard error — silently dropping it
// would risk skipping real work.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("corrupted state file %s: %w (delete the file to start over)", path, err)
	}
	s.Path = path
	return &s, nil
}

// BindCore verifies that a loaded state matches this run's inputs. Any
// mismatch is a hard error asking the operator to delete the file, never a
// silent skip. The backup file is not part of the core binding: it is
// generated with a timestamp per run, so a resume adopts the checkpoint's
// file instead (the data exported on the previous run is what later steps
// consume). An operator-supplied --data-file that differs from the
// checkpoint's is still a hard error at the command layer.
func (s *State) BindCore(flow, host, destHost string, withData bool) error {
	switch {
	case s.Flow != flow:
		return fmt.Errorf("state file %s belongs to flow %q, this run is %q (delete the file to start over)", s.Path, s.Flow, flow)
	case s.Host != host:
		return fmt.Errorf("state file %s belongs to host %q, this run targets %q (delete the file to start over)", s.Path, s.Host, host)
	case s.DestHost != destHost:
		return fmt.Errorf("state file %s belongs to destination %q, this run targets %q (delete the file to start over)", s.Path, s.DestHost, destHost)
	case s.WithData != withData:
		return fmt.Errorf("state file %s was created with --with-data=%v, this run uses %v (delete the file to start over)", s.Path, s.WithData, withData)
	}
	return nil
}

// Done reports whether the step completed in a previous run.
func (s *State) Done(step string) bool {
	for _, c := range s.CompletedSteps {
		if c == step {
			return true
		}
	}
	return false
}

// Mark records the step as completed and persists immediately.
func (s *State) Mark(step string) error {
	if s.Done(step) {
		return nil
	}
	s.CompletedSteps = append(s.CompletedSteps, step)
	return s.Save()
}

// Save writes the state atomically (temp file + rename). The file holds the
// OpenSearch admin password, so it is 0600.
func (s *State) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.Path+".tmp", data, 0o600); err != nil {
		return err
	}
	return os.Rename(s.Path+".tmp", s.Path)
}

// Delete removes the state file. Idempotent.
func Delete(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
