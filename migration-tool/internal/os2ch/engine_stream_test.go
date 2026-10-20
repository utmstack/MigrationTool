package os2ch

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// fakeCH is an in-memory CHClient. InsertTable records every JSONEachRow line
// it receives and counts one insert request per table. It never touches the
// network, so the test exercises StreamAll's pipe/mapping, not the driver.
type fakeCH struct {
	tables  map[string]int
	lines   map[string][]string
	failAll bool
}

func newFakeCH() *fakeCH {
	return &fakeCH{
		tables: map[string]int{},
		lines:  map[string][]string{},
	}
}

func (f *fakeCH) InsertTable(ctx context.Context, table string, r io.Reader) error {
	if f.failAll {
		// The real driver streams the full body before reporting an error;
		// drain so the pipe writer does not deadlock.
		io.Copy(io.Discard, r)
		return errors.New("insert failed")
	}
	f.tables[table]++
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		f.lines[table] = append(f.lines[table], sc.Text())
	}
	return sc.Err()
}

func (f *fakeCH) CountRows(context.Context, Dataset) (int64, error) { return 0, nil }

func (f *fakeCH) SamplePresent(context.Context, Dataset, string) (bool, error) { return false, nil }

func (f *fakeCH) Close() error { return nil }

// TestStreamAllPipesMappedRowsToCH proves the migrate path: StreamAll reads
// every in-scope index from OpenSearch and pushes mapped JSONEachRow rows to
// ClickHouse, with no NDJSON file written to disk.
func TestStreamAllPipesMappedRowsToCH(t *testing.T) {
	osSrv := fakeOS(t)
	defer osSrv.Close()

	fc := newFakeCH()
	e := &Engine{
		os:    NewOSClient(OSConfig{Host: osSrv.URL}),
		ch:    fc,
		batch: 2,
		log:   slog.New(slog.DiscardHandler),
	}

	res, err := e.StreamAll(context.Background(), "")
	if err != nil {
		t.Fatalf("StreamAll: %v", err)
	}
	if res.Total != 3 {
		t.Fatalf("streamed = %d docs, want 3", res.Total)
	}
	if res.PerDataset[DatasetLogs] != 3 {
		t.Fatalf("logs dataset = %d, want 3", res.PerDataset[DatasetLogs])
	}
	if res.SampleID(DatasetLogs) == "" {
		t.Fatal("no sample id recorded for logs")
	}
	if fc.tables["utmstack.logs"] != 1 {
		t.Fatalf("one InsertFormat per index expected, got %d for utmstack.logs", fc.tables["utmstack.logs"])
	}
	if len(fc.lines["utmstack.logs"]) != 3 {
		t.Fatalf("streamed lines = %d, want 3", len(fc.lines["utmstack.logs"]))
	}
	hasLog := false
	for _, line := range fc.lines["utmstack.logs"] {
		var doc map[string]interface{}
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			t.Fatalf("bad JSONEachRow line %q: %v", line, err)
		}
		// Mapping moved the V11 `event` key into the `log` column; it must
		// never survive.
		if _, has := doc["event"]; has {
			t.Fatalf("unmapped line still carries `event`: %s", line)
		}
		if _, has := doc["log"]; has {
			hasLog = true
		}
	}
	if !hasLog {
		t.Fatal("the `event` doc was not mapped into the `log` column")
	}
}

// TestStreamAllResumable proves StreamAll skips indices the manifest already
// records as completed, so a retry does not re-stream (and duplicate) them in
// the non-replacing utmstack.logs.
func TestStreamAllResumable(t *testing.T) {
	var scrolls int
	osSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_cat/indices" && r.URL.Query().Get("format") == "json":
			_ = json.NewEncoder(w).Encode([]interface{}{
				map[string]interface{}{"index": "v11-log-a-2026-10-01"},
				map[string]interface{}{"index": "v11-log-a-2026-10-02"},
			})
		default:
			// One doc on the first scroll page, empty on the second so the
			// scroll terminates.
			scrolls++
			hits := []map[string]interface{}{}
			if scrolls == 1 {
				hits = []map[string]interface{}{
					{"_id": "1", "_source": map[string]interface{}{"id": "1", "dataType": "linux"}},
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"_scroll_id": "s", "hits": map[string]interface{}{"hits": hits}})
		}
	}))
	defer osSrv.Close()

	// Pre-seed the manifest: index -01 already streamed by an earlier run.
	state := t.TempDir()
	if err := os.WriteFile(filepath.Join(state, "stream.yaml"),
		[]byte("done:\n  v11-log-a-2026-10-01: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fc := newFakeCH()
	e := &Engine{
		os:    NewOSClient(OSConfig{Host: osSrv.URL}),
		ch:    fc,
		batch: 2,
		log:   slog.New(slog.DiscardHandler),
	}

	res, err := e.StreamAll(context.Background(), state)
	if err != nil {
		t.Fatalf("StreamAll: %v", err)
	}
	// Only -02 was streamed; -01 was skipped from the manifest.
	if res.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", res.Skipped)
	}
	if res.Total != 1 {
		t.Fatalf("streamed = %d docs, want 1 (index -02 only)", res.Total)
	}
	// The manifest must now mark both as done.
	s := &streamState{}
	if in, err := os.ReadFile(filepath.Join(state, "stream.yaml")); err == nil {
		_ = yaml.Unmarshal(in, s)
	}
	if !s.Done["v11-log-a-2026-10-01"] || !s.Done["v11-log-a-2026-10-02"] {
		t.Fatalf("manifest incomplete after run: %+v", s.Done)
	}
}

// TestStreamAllSurfacesInsertFailure proves a failed ClickHouse insert aborts
// StreamAll with an error rather than dropping the index silently.
func TestStreamAllSurfacesInsertFailure(t *testing.T) {
	osSrv := fakeOS(t)
	defer osSrv.Close()

	fc := newFakeCH()
	fc.failAll = true
	e := &Engine{
		os:    NewOSClient(OSConfig{Host: osSrv.URL}),
		ch:    fc,
		batch: 2,
		log:   slog.New(slog.DiscardHandler),
	}

	if _, err := e.StreamAll(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected StreamAll to fail when the insert fails")
	}
}
