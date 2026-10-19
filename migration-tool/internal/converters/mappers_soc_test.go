package converters

import (
	"testing"
	"time"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
)

func TestSha1UUIDIsDeterministicAndDistinct(t *testing.T) {
	if tagUUIDForV11ID("1") != tagUUIDForV11ID("1") {
		t.Errorf("tagUUIDForV11ID is not deterministic")
	}
	if tagUUIDForV11ID("1") == tagUUIDForV11ID("2") {
		t.Errorf("tagUUIDForV11ID collides for different ids")
	}

	fns := []struct {
		name string
		fn   func(string) string
	}{
		{"tag", tagUUIDForV11ID},
		{"alertTagRule", alertTagRuleUUIDForV11ID},
		{"incident", incidentUUIDForV11ID},
		{"incidentAlert", incidentAlertUUIDForV11ID},
		{"incidentHistory", incidentHistoryUUIDForV11ID},
		{"incidentNote", incidentNoteUUIDForV11ID},
	}

	seen := map[string]string{}
	for _, f := range fns {
		u := f.fn("42")
		if prev, dup := seen[u]; dup {
			t.Errorf("namespace %q produced uuid %s, same as %q", f.name, u, prev)
		}
		seen[u] = f.name
	}
}

func TestSocStatus(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"OPEN", "Open"},
		{"open", "Open"},
		{"IN_REVIEW", "In review"},
		{"COMPLETED", "Completed"},
		{"MERGED", "Merged"},
		{"", "Open"},
		{"GARBAGE", "Open"},
	}
	for _, tt := range tests {
		if got := socStatus(tt.in); got != tt.want {
			t.Errorf("socStatus(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSocSeverity(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"1", "low"},
		{"2", "medium"},
		{"3", "high"},
		{"99", "medium"},
		{"", "medium"},
		{"garbage", "medium"},
	}
	for _, tt := range tests {
		if got := socSeverity(tt.in); got != tt.want {
			t.Errorf("socSeverity(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSocAlertStatus(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"1", "Automatic review"},
		{"2", "Open"},
		{"3", "In review"},
		{"5", "Completed"},
		{"0", ""},
		{"99", ""},
		{"", ""},
		{"garbage", ""},
	}
	for _, tt := range tests {
		if got := socAlertStatus(tt.in); got != tt.want {
			t.Errorf("socAlertStatus(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSocHistoryAction(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"INCIDENT_CREATED", "INCIDENT_CREATED"},
		{"INCIDENT_ALERT_ADD", "INCIDENT_ALERT_ADD"},
		{"INCIDENT_ALERT_STATUS_CHANGED", "INCIDENT_ALERT_STATUS_CHANGED"},
		{"INCIDENT_STATUS_CHANGE", "INCIDENT_STATUS_CHANGE"},
		{"INCIDENT_NOTE_ADD", "INCIDENT_NOTE_ADD"},
		{"INCIDENT_NOTE_CHANGE", "INCIDENT_NOTE_CHANGE"},
		{"INCIDENT_MERGED", "INCIDENT_STATUS_CHANGE"},
		{"INCIDENT_COMPLETED", "INCIDENT_STATUS_CHANGE"},
		{"INCIDENT_ASSIGNED_TO", "INCIDENT_ASSIGNED"},
		{"INCIDENT_ASSIGNED_CHANGE", "INCIDENT_ASSIGNED"},
		{"INCIDENT_ALERT_DELETED", ""},
		{"INCIDENT_ALERT_MOVED", ""},
		{"INCIDENT_COMMAND_EXECUTED", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := socHistoryAction(tt.in); got != tt.want {
			t.Errorf("socHistoryAction(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseSocTime(t *testing.T) {
	native := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if got := parseSocTime(native); !got.Equal(native) {
		t.Errorf("parseSocTime(native) = %v, want %v", got, native)
	}

	rfc := native.Format(time.RFC3339Nano)
	if got := parseSocTime(rfc); !got.Equal(native) {
		t.Errorf("parseSocTime(%q) = %v, want %v", rfc, got, native)
	}

	plain := "2026-10-19 12:00:00"
	want := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if got := parseSocTime(plain); !got.Equal(want) {
		t.Errorf("parseSocTime(%q) = %v, want %v", plain, got, want)
	}

	if got := parseSocTime(nil); got.IsZero() {
		t.Errorf("parseSocTime(nil) is zero, expected fallback to now")
	}
}

func TestFieldBool(t *testing.T) {
	tests := []struct {
		in   any
		want bool
	}{
		{true, true},
		{false, false},
		{"t", true},
		{"f", false},
		{"TRUE", true},
		{"false", false},
		{"1", true},
		{nil, false},
	}
	for _, tt := range tests {
		if got := fieldBool(tt.in); got != tt.want {
			t.Errorf("fieldBool(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestRemapTagCSV(t *testing.T) {
	known := map[string]bool{"1": true, "2": true, "5": true}
	if got := remapTagCSV("", known); got != "" {
		t.Errorf("remapTagCSV empty = %q, want empty", got)
	}
	if got := remapTagCSV("1,2", known); got != tagUUIDForV11ID("1")+","+tagUUIDForV11ID("2") {
		t.Errorf("remapTagCSV wrong output: %q", got)
	}
	if got := remapTagCSV("99,1", known); got != tagUUIDForV11ID("1") {
		t.Errorf("remapTagCSV should drop unknown id 99, got %q", got)
	}
}

func TestSocConvertersRegistered(t *testing.T) {
	pairs := []struct {
		src  string
		want string
	}{
		{"utm_alert_tag", "utm_alert_tag"},
		{"utm_alert_tag_rule", "utm_alert_tag_rule"},
		{"utm_incident", "utm_incident"},
		{"utm_incident_alert", "utm_incident_alert"},
		{"utm_incident_history", "utm_incident_history"},
		{"utm_incident_note", "utm_incident_note"},
	}
	for _, p := range pairs {
		c, ok := importer.Get(p.src)
		if !ok {
			t.Errorf("no converter registered for %s", p.src)
			continue
		}
		if c.SrcTable() != p.want {
			t.Errorf("converter for %s reports SrcTable %q, want %q", p.src, c.SrcTable(), p.want)
		}
	}
}

func TestDefaultMigrationTablesIncludesSOC(t *testing.T) {
	tables := domain.GetDefaultMigrationTables()
	var utmstack []string
	for _, ds := range tables {
		if ds.Database == domain.DefaultDatabase {
			utmstack = ds.Tables
		}
	}
	for _, want := range []string{"utm_alert_tag", "utm_alert_tag_rule", "utm_incident", "utm_incident_alert", "utm_incident_history", "utm_incident_note"} {
		if !contains(utmstack, want) {
			t.Errorf("default tables missing %q", want)
		}
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
