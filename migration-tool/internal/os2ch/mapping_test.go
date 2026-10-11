package os2ch

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMapLogDocument(t *testing.T) {
	// V11 log docs carry `event` (the parsed payload); V12 CH has no `event`
	// column but a `log` JSON column, so the mapping renames it.
	in := map[string]interface{}{
		"@timestamp": "2026-10-11T18:00:48.347Z",
		"dataType":   "linux",
		"dataSource": "v11",
		"id":         "abc",
		"tenantId":   "ce66",
		"tenantName": "Default",
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
}

func TestMapLogDocumentWithoutEvent(t *testing.T) {
	// Docs without `event` pass through unchanged (no `log` invented).
	in := map[string]interface{}{"@timestamp": "2026-10-11T18:00:48Z", "id": "x"}
	out := MapDocument(DatasetLogs, in)
	if _, has := out["log"]; has {
		t.Errorf("no log key should be added when event is absent")
	}
}

func TestMapStatisticsInjectsTenant(t *testing.T) {
	in := map[string]interface{}{
		"@timestamp": "2026-10-09T17:57:47Z",
		"type":       "enqueue_success",
		"dataType":   "linux",
		"dataSource": "v11",
		"count":      float64(25),
	}
	out := MapDocument(DatasetStatistics, in)
	if out["tenantId"] != DefaultTenantID {
		t.Errorf("tenantId = %v, want %s", out["tenantId"], DefaultTenantID)
	}
	// Existing fields are not clobbered.
	if out["count"] != float64(25) || out["type"] != "enqueue_success" {
		t.Errorf("existing statistics fields must survive: %v", out)
	}
}

func TestMapStatisticsKeepsExistingTenant(t *testing.T) {
	in := map[string]interface{}{"@timestamp": "t", "tenantId": "custom-tenant"}
	out := MapDocument(DatasetStatistics, in)
	if out["tenantId"] != "custom-tenant" {
		t.Errorf("existing tenantId must be kept, got %v", out["tenantId"])
	}
}

func TestMapAlertsPassThrough(t *testing.T) {
	in := map[string]interface{}{"id": "a1", "name": "alert", "severity": "high"}
	out := MapDocument(DatasetAlerts, in)
	if !reflect.DeepEqual(out, in) {
		t.Errorf("alerts should map 1:1, got %v", out)
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
