package converters

import (
	"testing"
)

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
