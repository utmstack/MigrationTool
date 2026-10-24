package cmd

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/state"
)

func TestRunStepMarksOnlyAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	st := state.New(filepath.Join(dir, state.File), "migrate", "s", "d", "b.yml", false)
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	if err := runStep(st, "Step", state.StepExport, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if st.Done(state.StepExport) {
		t.Fatal("a failed step must not be marked done")
	}

	if err := runStep(st, "Step", state.StepExport, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !st.Done(state.StepExport) {
		t.Fatal("a successful step must be marked done")
	}
}

func TestRunStepSkipsWhenDone(t *testing.T) {
	dir := t.TempDir()
	st := state.New(filepath.Join(dir, state.File), "migrate", "s", "d", "b.yml", false)
	st.Mark(state.StepImport)
	called := false
	err := runStep(st, "Step", state.StepImport, func() error { called = true; return nil })
	if err != nil || called {
		t.Fatalf("done step must be skipped: err=%v called=%v", err, called)
	}
}
