package upgrade

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/state"
)

// TestRunUpgradeResumesFromCheckpoint proves the destructive steps are not
// re-executed when a previous run already completed them: the executor must
// see no cleanup/install commands and the state file must be deleted on the
// successful finish.
func TestRunUpgradeResumesFromCheckpoint(t *testing.T) {
	dir := t.TempDir()
	stPath := filepath.Join(dir, state.File)

	// The export produced a data file the import consumes.
	backup := filepath.Join(dir, "backup.yml")
	if err := os.WriteFile(backup, []byte("version: v11\ntables: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	st := state.New(stPath, "upgrade", "10.0.0.1", "10.0.0.1", backup, false)
	for _, s := range []string{
		state.StepExport, state.StepCleanup, state.StepInstall, state.StepImport,
	} {
		if err := st.Mark(s); err != nil {
			t.Fatal(err)
		}
	}

	ex := &dockerExecutor{staticExecutor: staticExecutor{readFile: func(string) ([]byte, error) {
		return []byte("services:\n  postgres:\n    environment:\n      - POSTGRES_PASSWORD=v12pw\n"), nil
	}}}
	u := &Upgrader{
		sourceCfg:  domain.InstanceConfig{Host: "10.0.0.1", SSHUser: "utmstack"},
		backupFile: backup,
		executor:   ex,
		withData:   false,
		st:         st,
		portWait:   time.Millisecond,
		confirmFn:  func() error { return nil },
	}

	if err := u.RunUpgrade(); err != nil {
		t.Fatalf("RunUpgrade resume failed: %v", err)
	}

	for _, c := range ex.commands {
		if strings.Contains(c, "docker service rm") {
			t.Fatalf("cleanup must be skipped on resume, saw: %s", c)
		}
		if strings.Contains(c, "utmstack_installer") {
			t.Fatalf("install must be skipped on resume, saw: %s", c)
		}
	}

	if _, err := os.Stat(stPath); !os.IsNotExist(err) {
		t.Fatalf("state file must be deleted after a successful resumed run")
	}
}
