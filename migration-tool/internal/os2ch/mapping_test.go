package os2ch

import (
	"encoding/json"
	"testing"
)

func TestMapLogDocument(t *testing.T) {
	// V11 log docs carry `event` (the parsed payload); V12 CH has no `event`
	// column but a `log` JSON column, so the mapping renames it. A foreign
	// tenantId is normalised to the V12 default tenant.
	in := map[string]interface{}{
		"@timestamp": "2026-10-11T18:00:48.347Z",
		"dataType":   "linux",
		"dataSource": "v11",
		"id":         "abc",
		"tenantId":   "foreign-tenant",
		"raw":        "ssh login ok",
		"origin":     map[string]interface{}{"ip": "1.2.3.4"},
		"event":      map[string]interface{}{"type": "auditd", "uid": "105"},
	}
	out := MapDocument(DatasetLogs, in)
	if _, hasEvent := out["event"]; hasEvent {
		t.Errorf("event key must be removed, got %v", out["event"])
	}
	logVal, ok := out["log"]
	if !ok {
		t.Fatalf("log key missing after mapping: %v", out)
	}
	ev, isMap := logVal.(map[string]interface{})
	if !isMap || ev["type"] != "auditd" {
		t.Errorf("log should hold the event object, got %v", logVal)
	}
	if out["@timestamp"] != "2026-10-11T18:00:48.347Z" || out["id"] != "abc" {
		t.Errorf("untouched fields must survive: %v", out)
	}
	if out["tenantId"] != DefaultTenantID || out["tenantName"] != "Default" {
		t.Errorf("tenant must be normalised, got %v / %v", out["tenantId"], out["tenantName"])
	}
}

func TestMapLogDocumentWithoutEvent(t *testing.T) {
	// Docs without `event` pass through unchanged (no `log` invented).
	in := map[string]interface{}{"@timestamp": "2026-10-11T18:00:48Z", "id": "x"}
	out := MapDocument(DatasetLogs, in)
	if _, has := out["log"]; has {
		t.Errorf("no log key should be added when event is absent")
	}
}

func TestMapAlertsNormalisesTenantAndAddsLastUpdate(t *testing.T) {
	in := map[string]interface{}{"id": "a1", "name": "alert", "severity": "high", "@timestamp": "2026-10-12T08:00:00Z"}
	out := MapDocument(DatasetAlerts, in)
	if out["tenantId"] != DefaultTenantID || out["tenantName"] != "Default" {
		t.Errorf("alerts must carry the default tenant, got %v / %v", out["tenantId"], out["tenantName"])
	}
	if out["lastUpdate"] != "2026-10-12T08:00:00Z" {
		t.Errorf("lastUpdate must come from @timestamp, got %v", out["lastUpdate"])
	}
}

func TestMapAlertsConvertsStatusCodeToLabel(t *testing.T) {
	// V11 persists status as a numeric code; V12 stores the label string
	// in `status` and the UI filters by it, so the code must be translated.
	for code, want := range map[float64]string{
		1: "Automatic review",
		2: "Open",
		3: "In review",
		5: "Completed",
	} {
		in := map[string]interface{}{"id": "a1", "status": code, "statusLabel": want}
		out := MapDocument(DatasetAlerts, in)
		if out["status"] != want {
			t.Errorf("status code %v = %v, want %q", code, out["status"], want)
		}
	}
}

func TestMapAlertsKeepsStringStatus(t *testing.T) {
	in := map[string]interface{}{"id": "a1", "status": "Open"}
	out := MapDocument(DatasetAlerts, in)
	if out["status"] != "Open" {
		t.Errorf("string status must survive, got %v", out["status"])
	}
}

func TestMapAlertsTranslatesV11FieldNames(t *testing.T) {
	in := map[string]interface{}{
		"id":             "a1",
		"reference":      []interface{}{"https://x"},
		"groupedBy":      []interface{}{"adversary.user"},
		"deduplicatedBy": []interface{}{"lastEvent.src_ip"},
	}
	out := MapDocument(DatasetAlerts, in)
	assertSlice := func(dst string, want []string) {
		got, ok := out[dst].([]interface{})
		if !ok {
			t.Errorf("%s must be a slice, got %T", dst, out[dst])
			return
		}
		if len(got) != len(want) {
			t.Errorf("%s length = %d, want %d", dst, len(got), len(want))
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s[%d] = %v, want %v", dst, i, got[i], want[i])
			}
		}
	}
	assertSlice("references", []string{"https://x"})
	assertSlice("groupBy", []string{"adversary.user"})
	assertSlice("deduplicateBy", []string{"lastEvent.src_ip"})
	for _, gone := range []string{"reference", "groupedBy", "deduplicatedBy"} {
		if _, has := out[gone]; has {
			t.Errorf("%s must not survive the mapping", gone)
		}
	}
}

func TestMapAlertsSingleValueArray(t *testing.T) {
	in := map[string]interface{}{"id": "a1", "reference": "https://x"}
	out := MapDocument(DatasetAlerts, in)
	refs, ok := out["references"].([]interface{})
	if !ok || len(refs) != 1 {
		t.Fatalf("single reference must become a 1-element array, got %v", out["references"])
	}
}

func TestMarshalRow(t *testing.T) {
	doc := map[string]interface{}{"a": "b"}
	line, err := MarshalRow(doc)
	if err != nil {
		t.Fatalf("MarshalRow: %v", err)
	}
	var round map[string]interface{}
	if err := json.Unmarshal([]byte(line), &round); err != nil {
		t.Fatalf("output must be valid JSON: %v", err)
	}
}
