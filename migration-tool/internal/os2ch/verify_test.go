package os2ch

import (
	"context"
	"fmt"
	"testing"
)

// fakeVerifyer is an in-memory verifyer for testing VerifyOne's pure logic.
type fakeVerifyer struct {
	counts   map[Dataset]int64
	present  map[string]bool
	countErr error
}

func (f *fakeVerifyer) CountRows(ctx context.Context, ds Dataset) (int64, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.counts[ds], nil
}

func (f *fakeVerifyer) SamplePresent(ctx context.Context, ds Dataset, id string) (bool, error) {
	return f.present[fmt.Sprintf("%s/%s", ds, id)], nil
}

func TestVerifyOnePass(t *testing.T) {
	v := &fakeVerifyer{
		counts:  map[Dataset]int64{DatasetLogs: 100},
		present: map[string]bool{"logs/id1": true},
	}
	res := VerifyOne(context.Background(), v, DatasetLogs, 100, "id1")
	if !res.Pass() {
		t.Fatalf("expected pass, got error: %s", res.Error)
	}
	if res.SampleOK != 1 {
		t.Errorf("SampleOK = %d, want 1", res.SampleOK)
	}
}

func TestVerifyOneCountMismatch(t *testing.T) {
	v := &fakeVerifyer{counts: map[Dataset]int64{DatasetLogs: 90}}
	res := VerifyOne(context.Background(), v, DatasetLogs, 100, "")
	if res.Pass() {
		t.Fatal("expected count mismatch to fail")
	}
}

func TestVerifyOneMissingSample(t *testing.T) {
	v := &fakeVerifyer{counts: map[Dataset]int64{DatasetAlerts: 5}}
	res := VerifyOne(context.Background(), v, DatasetAlerts, 5, "a1")
	if res.Pass() {
		t.Fatal("expected missing sample id to fail")
	}
}

func TestVerifyOneExtraCHRowsIsOK(t *testing.T) {
	// CH having MORE rows than the source count is not a data loss; it can
	// happen when the source index rolled between list and count.
	v := &fakeVerifyer{counts: map[Dataset]int64{DatasetLogs: 110}}
	res := VerifyOne(context.Background(), v, DatasetLogs, 100, "")
	if !res.Pass() {
		t.Fatalf("extra rows should pass, got: %s", res.Error)
	}
}

func TestVerifyOneCountError(t *testing.T) {
	v := &fakeVerifyer{countErr: fmt.Errorf("boom")}
	res := VerifyOne(context.Background(), v, DatasetLogs, 100, "")
	if res.Pass() {
		t.Fatal("expected count error to fail")
	}
}
