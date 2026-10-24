package upgrade

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/state"
)

// TestRunUpgradeFailThenResumeFromStream proves the core recovery story: a run
// that fails during the data stream leaves a checkpoint; the re-run restores
// the captured OpenSearch configuration from the state file (the V11 compose
// is gone), re-executes only the stream, deletes the state on success, and
// never touches cleanup or install again.
func TestRunUpgradeFailThenResumeFromStream(t *testing.T) {
	dir := t.TempDir()
	stPath := filepath.Join(dir, state.File)
	backup := filepath.Join(dir, "backup.yml")
	if err := os.WriteFile(backup, []byte("version: v11\ntables: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	compose := `
services:
  node1:
    image: ghcr.io/utmstack/utmstack/opensearch:latest
    environment:
      - OPENSEARCH_INITIAL_ADMIN_PASSWORD=ospw
  postgres:
    image: postgres:16
    environment:
      - POSTGRES_PASSWORD=v12pw
`

	ex := &dockerExecutor{staticExecutor: staticExecutor{readFile: func(string) ([]byte, error) {
		return []byte(compose), nil
	}}}

	st := state.New(stPath, "upgrade", "10.0.0.1", "10.0.0.1", backup, true)
	// The previous run reached the stream: these steps already completed.
	for _, s := range []string{state.StepExport, state.StepCleanup, state.StepInstall, state.StepImport} {
		if err := st.Mark(s); err != nil {
			t.Fatal(err)
		}
	}

	streamCalls := 0
	firstErr := errors.New("stream boom")

	newUpgrader := func() *Upgrader {
		return &Upgrader{
			sourceCfg:  domain.InstanceConfig{Host: "10.0.0.1", SSHUser: "utmstack"},
			backupFile: backup,
			executor:   ex,
			withData:   true,
			st:         st,
			portWait:   time.Millisecond,
			confirmFn:  func() error { return nil },
			streamFn: func(string, string) error {
				streamCalls++
				if streamCalls == 1 {
					return firstErr
				}
				return nil
			},
		}
	}

	// First run: capture happens, stream fails.
	if err := newUpgrader().RunUpgrade(); !errors.Is(err, firstErr) {
		t.Fatalf("first run err = %v, want stream boom", err)
	}
	if !st.Done(state.StepCaptureOS) {
		t.Fatal("capture must be checkpointed before the stream fails")
	}
	if st.Done(state.StepStream) {
		t.Fatal("a failed stream must not be marked done")
	}
	if st.OS == nil || st.OS.Image != "ghcr.io/utmstack/utmstack/opensearch:latest" || st.OS.Password != "ospw" {
		t.Fatalf("captured OS values must be persisted in state: %+v", st.OS)
	}

	// Second run: resume; the captured values come from the state file, only
	// the stream executes, and the state file is deleted on success.
	if err := newUpgrader().RunUpgrade(); err != nil {
		t.Fatalf("resumed run failed: %v", err)
	}
	if streamCalls != 2 {
		t.Fatalf("stream calls = %d, want 2", streamCalls)
	}
	if _, err := os.Stat(stPath); !os.IsNotExist(err) {
		t.Fatal("state file must be deleted after the successful resumed run")
	}
	for _, c := range ex.commands {
		if strings.Contains(c, "docker service rm") || strings.Contains(c, "utmstack_installer") {
			t.Fatalf("destructive step re-executed: %s", c)
		}
	}
}
