package agentmanager

import (
	"strings"
	"testing"
)

func TestBridgeCredentialsYAML(t *testing.T) {
	y := BridgeCredentialsYAML("10.11.11.139", "secretkey")
	for _, want := range []string{"server: '10.11.11.139'", "internal_key: 'secretkey'", "insecure: true"} {
		if !strings.Contains(y, want) {
			t.Errorf("credentials YAML missing %q:\n%s", want, y)
		}
	}
}
