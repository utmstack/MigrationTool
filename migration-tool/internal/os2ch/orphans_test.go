package os2ch

import (
	"encoding/json"
	"sort"
	"testing"
)

func TestOrphanKeys(t *testing.T) {
	cols := NewColumnSet("@timestamp", "dataType", "id", "log", "raw", "tenantId", "tenantName", "origin", "target", "compliance")

	doc := map[string]interface{}{
		"@timestamp": "t",
		"dataType":   "linux",
		"weirdField": "x",
		"origin":     map[string]interface{}{"ip": "1.2.3.4"},
		"log":        map[string]interface{}{"k": "v"},
	}

	orphans := OrphanKeys(doc, cols)
	if len(orphans) != 1 || orphans[0] != "weirdField" {
		t.Errorf("orphans = %v, want [weirdField]", orphans)
	}
}

func TestOrphanKeysNestedIsNotExpanded(t *testing.T) {
	// `origin`/`log` are JSON columns in CH: any keys inside them are fine.
	// Only top-level keys are compared.
	cols := NewColumnSet("origin", "log")
	doc := map[string]interface{}{
		"origin": map[string]interface{}{"anything": "ok"},
		"log":    map[string]interface{}{"also": "ok"},
	}
	if got := OrphanKeys(doc, cols); len(got) != 0 {
		t.Errorf("nested JSON columns must not produce orphans, got %v", got)
	}
}

func TestOrphanReportAggregatesByDataType(t *testing.T) {
	cols := NewColumnSet("id", "dataType")
	r := NewOrphanReport()

	r.Note(DatasetLogs, map[string]interface{}{"id": "1", "dataType": "linux", "bad1": "x", "bad2": "y"}, cols)
	r.Note(DatasetLogs, map[string]interface{}{"id": "2", "dataType": "linux", "bad1": "x"}, cols)
	r.Note(DatasetLogs, map[string]interface{}{"id": "3", "dataType": "o365", "bad1": "x"}, cols)

	keys := r.Orphans("linux")
	sort.Strings(keys)
	if len(keys) != 2 || keys[0] != "bad1" || keys[1] != "bad2" {
		t.Errorf("linux orphans = %v, want [bad1 bad2]", keys)
	}
	if r.Count("linux", "bad1") != 2 {
		t.Errorf("bad1 count = %d, want 2", r.Count("linux", "bad1"))
	}
	if r.Count("o365", "bad1") != 1 {
		t.Errorf("o365 bad1 count = %d, want 1", r.Count("o365", "bad1"))
	}
	if r.Seen("linux") != 2 {
		t.Errorf("seen(linux) = %d, want 2", r.Seen("linux"))
	}
	if r.TotalOrphans() != 3 { // bad1, bad2 (linux) + bad1 (o365)
		t.Errorf("total orphans = %d, want 3", r.TotalOrphans())
	}
}

func TestOrphanReportNoOrphans(t *testing.T) {
	cols := NewColumnSet("id", "dataType")
	r := NewOrphanReport()
	r.Note(DatasetLogs, map[string]interface{}{"id": "1", "dataType": "linux"}, cols)

	if r.TotalOrphans() != 0 {
		t.Errorf("expected 0 orphans, got %d", r.TotalOrphans())
	}
	if s := r.Summary(); s == "" {
		t.Error("summary should not be empty")
	}
	_ = json.Valid([]byte("x")) // keep import
}
