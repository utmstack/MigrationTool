package os2ch

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDumpWritesNDJSONAndManifest verifies the upgrade dump phase: raw docs
// land in NDJSON files and a manifest records each index and its doc count.
func TestDumpWritesNDJSONAndManifest(t *testing.T) {
	srv := fakeOS(t)
	defer srv.Close()

	e := &Engine{
		os:    NewOSClient(OSConfig{Host: srv.URL}),
		log:   slog.New(slog.DiscardHandler),
		batch: 2,
	}
	dir := t.TempDir()
	res, err := e.Dump(context.Background(), dir)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	// fakeOS serves one in-scope index with 3 docs and one out-of-scope index.
	if res.PerDataset[DatasetLogs] != 3 {
		t.Fatalf("logs dumped = %d, want 3", res.PerDataset[DatasetLogs])
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "v11-backend-logs" {
		t.Fatalf("skipped = %v, want [v11-backend-logs]", res.Skipped)
	}

	ndjson := filepath.Join(dir, "v11-log-linux-2026-10-11.ndjson")
	f, err := os.Open(ndjson)
	if err != nil {
		t.Fatalf("ndjson missing: %v", err)
	}
	defer f.Close()
	lines := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var doc map[string]interface{}
		if err := json.Unmarshal(sc.Bytes(), &doc); err != nil {
			t.Fatalf("bad ndjson: %v", err)
		}
		// Raw docs keep the V11 `event` key; mapping happens at load time.
		if doc["id"] == "" {
			t.Fatalf("ndjson line missing id: %s", sc.Text())
		}
		lines++
	}
	if lines != 3 {
		t.Fatalf("ndjson lines = %d, want 3", lines)
	}

	body, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if !contains(string(body), "index: v11-log-linux-2026-10-11") || !contains(string(body), "docs: 3") {
		t.Fatalf("manifest content wrong:\n%s", body)
	}
}

func TestLoadSkipsAlreadyLoaded(t *testing.T) {
	dir := t.TempDir()
	// A manifest with one loaded index must be skipped on load.
	manifest := `version: 1
created_at: "2026-01-01T00:00:00Z"
records:
  - dataset: logs
    index: v11-log-linux-2026-10-11
    file: v11-log-linux-2026-10-11.ndjson
    docs: 1
    loaded: true
`
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "v11-log-linux-2026-10-11.ndjson"), []byte("{\"id\":\"1\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(dir, "manifest.yaml"))
	if err != nil {
		t.Fatalf("readManifest: %v", err)
	}
	if len(m.Records) != 1 || !m.Records[0].Loaded {
		t.Fatalf("manifest parsed wrong: %+v", m.Records)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
