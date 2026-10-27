package os2ch

import (
	"context"
	"fmt"
	"strings"
)

// VerifyResult reports whether one dataset's rows made it to ClickHouse.
type VerifyResult struct {
	Dataset  Dataset
	OSDocs   int64
	CHDocs   int64
	SampleOK int
	SampleID string
	Error    string
}

func (v *VerifyResult) Pass() bool { return v.Error == "" }

// verifyer is the ClickHouse surface Verify needs, so the pure logic can be
// tested without a live database.
type verifyer interface {
	CountRows(ctx context.Context, ds Dataset) (int64, error)
	SamplePresent(ctx context.Context, ds Dataset, id string) (bool, error)
}

// VerifyOne runs the count + sample check for a single dataset. osCount is
// the number of source docs for that dataset; sampleID is one doc id that
// must exist on the destination (empty skips the sample check).
func VerifyOne(ctx context.Context, v verifyer, ds Dataset, osCount int64, sampleID string) VerifyResult {
	res := VerifyResult{Dataset: ds, OSDocs: osCount, SampleID: sampleID}

	ch, err := v.CountRows(ctx, ds)
	if err != nil {
		res.Error = fmt.Sprintf("clickhouse count: %v", err)
		return res
	}
	res.CHDocs = ch

	if osCount > 0 && ch < osCount {
		res.Error = fmt.Sprintf("clickhouse has %d rows, source had %d docs (%d lost)", ch, osCount, osCount-ch)
		return res
	}

	if sampleID == "" {
		return res
	}
	found, err := v.SamplePresent(ctx, ds, sampleID)
	if err != nil {
		res.Error = fmt.Sprintf("sample query: %v", err)
		return res
	}
	if !found {
		res.Error = fmt.Sprintf("sample id %s not found in %s", sampleID, TableFor(ds))
		return res
	}
	res.SampleOK = 1
	return res
}

// SamplePresent implements verifyer on top of the HTTP interface.
func (w *CHWriter) SamplePresent(ctx context.Context, ds Dataset, id string) (bool, error) {
	table := TableFor(ds)
	if table == "" {
		return false, fmt.Errorf("unknown dataset %q", ds)
	}
	body, err := w.httpQuery(ctx, fmt.Sprintf("SELECT count() FROM %s WHERE id = '%s' LIMIT 1", table, strings.ReplaceAll(id, "'", "''")))
	if err != nil {
		return false, err
	}
	var n int64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(body)), "%d", &n); err != nil {
		return false, err
	}
	return n > 0, nil
}
