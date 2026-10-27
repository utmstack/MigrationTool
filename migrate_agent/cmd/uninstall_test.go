package cmd

import (
	"fmt"
	"testing"
	"time"

	"github.com/kardianos/service"
)

// fakeStatusService returns the scripted status sequence, then the last one.
type fakeStatusService struct {
	sequence []service.Status
	err      error
}

func (f *fakeStatusService) Status() (service.Status, error) {
	if f.err != nil {
		return service.StatusUnknown, f.err
	}
	last := service.StatusUnknown
	if len(f.sequence) > 0 {
		last = f.sequence[len(f.sequence)-1]
	}
	if len(f.sequence) > 0 {
		next := f.sequence[0]
		f.sequence = f.sequence[1:]
		return next, nil
	}
	return last, nil
}

func TestWaitUnitGoneStoppedImmediately(t *testing.T) {
	s := &fakeStatusService{sequence: []service.Status{service.StatusStopped}}
	if err := waitUnitGone(s, 500*time.Millisecond); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitUnitGoneStopsAfterRunning(t *testing.T) {
	s := &fakeStatusService{sequence: []service.Status{
		service.StatusRunning, service.StatusRunning, service.StatusStopped,
	}}
	if err := waitUnitGone(s, 2*time.Second); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitUnitGoneStillRunningErrors(t *testing.T) {
	s := &fakeStatusService{sequence: []service.Status{service.StatusRunning}}
	err := waitUnitGone(s, 300*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestWaitUnitGoneStatusError(t *testing.T) {
	s := &fakeStatusService{err: fmt.Errorf("boom")}
	if err := waitUnitGone(s, 300*time.Millisecond); err == nil {
		t.Fatal("expected status error, got nil")
	}
}
