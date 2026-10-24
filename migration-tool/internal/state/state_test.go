package state

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir string) *State {
	t.Helper()
	path := filepath.Join(dir, File)
	s := New(path, "upgrade", "10.0.0.1", "10.0.0.1", "backup.yml", true)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return s
}

func TestLoadMissingReturnsNil(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("Load on missing file must return nil,nil, got err=%v", err)
	}
	if s != nil {
		t.Fatalf("expected nil state, got %+v", s)
	}
}

func TestLoadCorruptedFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, File), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(dir, File)); err == nil {
		t.Fatal("corrupted file must be a hard error, got nil")
	}
}

func TestRoundTripAndDone(t *testing.T) {
	dir := t.TempDir()
	s := writeFile(t, dir)
	s.OS = &OSCapture{Image: "img", Password: "pw"}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if err := s.Mark(StepExport); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(filepath.Join(dir, File))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Done(StepExport) {
		t.Fatal("StepExport must be done after resume load")
	}
	if loaded.Done(StepInstall) {
		t.Fatal("StepInstall must not be done")
	}
	if loaded.OS == nil || loaded.OS.Image != "img" || loaded.OS.Password != "pw" {
		t.Fatalf("captured OS not persisted: %+v", loaded.OS)
	}
}

func TestMarkIsIdempotent(t *testing.T) {
	s := writeFile(t, t.TempDir())
	if err := s.Mark(StepExport); err != nil {
		t.Fatal(err)
	}
	if err := s.Mark(StepExport); err != nil {
		t.Fatal(err)
	}
	if len(s.CompletedSteps) != 1 {
		t.Fatalf("duplicate step recorded: %v", s.CompletedSteps)
	}
}

func TestSaveUses0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), File)
	if err := New(path, "migrate", "h", "d", "b.yml", false).Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("state file holds the OS password, want 0600 got %v", perm)
	}
}

func TestBindCoreRejectsMismatchedInputs(t *testing.T) {
	s := writeFile(t, t.TempDir()) // upgrade / 10.0.0.1 / 10.0.0.1 / withData

	cases := []struct {
		name     string
		flow     string
		host     string
		destHost string
		withData bool
	}{
		{"same inputs", "upgrade", "10.0.0.1", "10.0.0.1", true},
		{"different flow", "migrate", "10.0.0.1", "10.0.0.1", true},
		{"different host", "upgrade", "10.0.0.2", "10.0.0.2", true},
		{"different dest", "upgrade", "10.0.0.1", "10.0.0.3", true},
		{"different withData", "upgrade", "10.0.0.1", "10.0.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.BindCore(tc.flow, tc.host, tc.destHost, tc.withData)
			if tc.name == "same inputs" && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tc.name != "same inputs" && err == nil {
				t.Fatalf("expected binding error, got nil")
			}
		})
	}
}

func TestDeleteIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir)
	if err := Delete(filepath.Join(dir, File)); err != nil {
		t.Fatal(err)
	}
	if err := Delete(filepath.Join(dir, File)); err != nil {
		t.Fatalf("second delete must be idempotent, got %v", err)
	}
}
