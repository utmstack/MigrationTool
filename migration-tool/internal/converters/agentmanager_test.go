package converters

import (
	"testing"
)

func TestAgentManagerColumnMapping(t *testing.T) {
	src := map[string]any{
		"id":               "42",
		"ip":               "10.0.0.5",
		"hostname":         "web-01",
		"os":               "linux",
		"platform":         "x86_64",
		"version":          "1.2.3",
		"agent_key":        "agent-key-42",
		"register_by":      "admin",
		"mac":              "aa:bb:cc:dd:ee:ff",
		"aliases":          "web",
		"addresses":        "10.0.0.5,10.0.0.6",
		"os_major_version": "1",
		"os_minor_version": "2",
		"deleted_at":       nil,
		"deleted_by":       nil,
	}
	id := fieldString(src, "id")
	if id != "42" {
		t.Fatalf("id = %q", id)
	}
	if fieldString(src, "os") != "linux" {
		t.Errorf("os = %q", fieldString(src, "os"))
	}
	if parseModTime(src["deleted_at"]) != nil {
		t.Errorf("deleted_at nil should map to nil")
	}
	if nullIfEmpty("") != nil {
		t.Errorf("empty string should map to nil for nullable columns")
	}
}

func TestFederationMapping(t *testing.T) {
	src := map[string]any{
		"id":              "1",
		"fs_client_token": "v11-token-abc",
	}
	token := fieldString(src, "fs_client_token")
	if token != "v11-token-abc" {
		t.Errorf("token = %q", token)
	}
	if fieldString(src, "id") != "1" {
		t.Errorf("id = %q", fieldString(src, "id"))
	}
}

func TestCollectorModulePassThrough(t *testing.T) {
	src := map[string]any{"id": "7", "module": "UTMSTACK"}
	if fieldString(src, "module") != "UTMSTACK" {
		t.Errorf("module should pass through verbatim: %q", fieldString(src, "module"))
	}
}
