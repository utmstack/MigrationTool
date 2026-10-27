package os2ch

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

// fakeOSIndices serves the given index->docs map: one doc per page for
// /_search (all hits fit the large batch, so no scroll continuation occurs).
func fakeOSIndices(t *testing.T, indexed map[string][]map[string]interface{}) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_cat/indices" && r.URL.Query().Get("format") == "json":
			out := []interface{}{}
			for name := range indexed {
				out = append(out, map[string]interface{}{"index": name})
			}
			_ = json.NewEncoder(w).Encode(out)
		case regexp.MustCompile(`^/v11-[^/]+/_search$`).MatchString(r.URL.Path):
			name := r.URL.Path[len("/") : len(r.URL.Path)-len("/_search")]
			hits := []map[string]interface{}{}
			for i, d := range indexed[name] {
				id := d["id"]
				if id == nil {
					id = itoa(i)
				}
				hits = append(hits, map[string]interface{}{"_id": id, "_source": d})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"_scroll_id": "s", "hits": map[string]interface{}{"hits": hits}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv
}

// TestStreamAllRetentionWindow proves the 7-day window: old log indices are
// excluded, while recent log and alert indices are streamed, and the summary
// reports the exclusions.
func TestStreamAllRetentionWindow(t *testing.T) {
	recent := time.Now().Format("2006-01-02")
	old := time.Now().AddDate(0, 0, -30).Format("2006-01-02")

	srv := fakeOSIndices(t, map[string][]map[string]interface{}{
		"v11-log-linux-" + recent: {map[string]interface{}{"id": "new1", "dataType": "linux"}},
		"v11-log-linux-" + old:    {map[string]interface{}{"id": "old1", "dataType": "linux"}},
		"v11-alert-" + recent:     {map[string]interface{}{"id": "al1", "dataType": "linux"}},
	})
	defer srv.Close()

	fc := newFakeCH()
	e := &Engine{os: NewOSClient(OSConfig{Host: srv.URL}), ch: fc, batch: 100, log: slog.New(slog.DiscardHandler), windowDays: 7}
	res, err := e.StreamAll(context.Background(), "")
	if err != nil {
		t.Fatalf("StreamAll: %v", err)
	}
	if res.WindowDays != 7 {
		t.Fatalf("WindowDays = %d, want 7", res.WindowDays)
	}
	if res.Total != 2 {
		t.Fatalf("streamed = %d docs, want 2 (recent log + recent alert)", res.Total)
	}
	if res.PerDataset[DatasetLogs] != 1 || res.PerDataset[DatasetAlerts] != 1 {
		t.Fatalf("per-dataset = %+v, want logs 1, alerts 1", res.PerDataset)
	}
	if len(fc.lines["utmstack.statistics"]) != 0 {
		t.Fatalf("statistics rows = %d, want 0", len(fc.lines["utmstack.statistics"]))
	}
	if len(res.ExcludedOld) != 1 || res.ExcludedOld[0] != "v11-log-linux-"+old {
		t.Fatalf("ExcludedOld = %v, want the 30-day-old log index", res.ExcludedOld)
	}
}

// TestStreamAllWindowIncludesUndatableIndex proves a log/alert index whose
// name has no parseable date is INCLUDED (fail-safe), so an unknown naming
// scheme never silently drops data.
func TestStreamAllWindowIncludesUndatableIndex(t *testing.T) {
	srv := fakeOSIndices(t, map[string][]map[string]interface{}{
		"v11-log-unknown": {map[string]interface{}{"id": "x1", "dataType": "linux"}},
	})
	defer srv.Close()

	fc := newFakeCH()
	e := &Engine{os: NewOSClient(OSConfig{Host: srv.URL}), ch: fc, batch: 100, log: slog.New(slog.DiscardHandler), windowDays: 7}
	res, err := e.StreamAll(context.Background(), "")
	if err != nil {
		t.Fatalf("StreamAll: %v", err)
	}
	if res.Total != 1 {
		t.Fatalf("streamed = %d docs, want 1 (undatable index included)", res.Total)
	}
	if len(res.ExcludedOld) != 0 {
		t.Fatalf("ExcludedOld = %v, want none", res.ExcludedOld)
	}
}

// TestIndexDate covers the date extraction: plain suffix, random suffix after
// the date, no date, and an invalid calendar date.
func TestIndexDate(t *testing.T) {
	cases := []struct {
		name   string
		want   string
		wantOK bool
	}{
		{"v11-log-linux-2026-10-27", "2026-10-27", true},
		{"v11-log-google-2026-08-03-lx4lkv", "2026-08-03", true},
		{"v11-alert-2026-10-27", "2026-10-27", true},
		{"v11-log-unknown", "", false},
		{"v11-log-2026-13-45", "", false},
		{"v11-statistics-2026.10", "", false},
	}
	for _, c := range cases {
		got, ok := indexDate(c.name)
		if ok != c.wantOK {
			t.Fatalf("indexDate(%q) ok = %v, want %v", c.name, ok, c.wantOK)
		}
		if ok && got.Format("2006-01-02") != c.want {
			t.Fatalf("indexDate(%q) = %s, want %s", c.name, got.Format("2006-01-02"), c.want)
		}
	}
}
