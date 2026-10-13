package compose

import (
	"strings"
	"testing"
)

func TestSetComposeEnvVars(t *testing.T) {
	input := `services:
  agent-manager:
    image: utmstack/agent-manager:latest
    environment:
      - REDIS_HOST=utmstack_redis
      - ENCRYPTION_KEY=old-key-123
      - INTERNAL_KEY=old-key-123
  backend:
    image: utmstack/backend:latest
    environment:
      - POSTGRES_DB=utmstack
      - ENCRYPTION_KEY=old-key-123
      - INTERNAL_KEY=old-key-123
      - FEDERATION_INTERNAL_KEY=old-key-123
`

	t.Run("rewrites each var in place, leaves everything else byte-identical", func(t *testing.T) {
		out, nEnc, _ := SetComposeEnvVars([]byte(input), "ENCRYPTION_KEY", "new-key-456")
		if nEnc != 2 {
			t.Fatalf("expected 2 ENCRYPTION_KEY lines changed, got %d", nEnc)
		}
		s := string(out)
		if strings.Count(s, "- ENCRYPTION_KEY=new-key-456") != 2 {
			t.Errorf("ENCRYPTION_KEY not rewritten in both services:\n%s", s)
		}
		if strings.Count(s, "- INTERNAL_KEY=old-key-123") != 2 {
			t.Errorf("INTERNAL_KEY should be untouched when only ENCRYPTION_KEY is targeted")
		}
		// Unrelated lines must survive verbatim, including the sibling
		// FEDERATION_INTERNAL_KEY which contains ENCRYPTION_KEY as a substring.
		for _, want := range []string{
			"  agent-manager:\n    image: utmstack/agent-manager:latest",
			"      - REDIS_HOST=utmstack_redis",
			"      - POSTGRES_DB=utmstack",
			"      - FEDERATION_INTERNAL_KEY=old-key-123",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("missing expected verbatim line: %q", want)
			}
		}
	})

	t.Run("second var rewrites on top of first", func(t *testing.T) {
		out, _, _ := SetComposeEnvVars([]byte(input), "ENCRYPTION_KEY", "new-key-456")
		out, nInt, _ := SetComposeEnvVars(out, "INTERNAL_KEY", "new-key-456")
		if nInt != 2 {
			t.Fatalf("expected 2 INTERNAL_KEY lines changed, got %d", nInt)
		}
		s := string(out)
		if strings.Count(s, "- INTERNAL_KEY=new-key-456") != 2 {
			t.Errorf("INTERNAL_KEY not rewritten in both services:\n%s", s)
		}
		if !strings.Contains(s, "- FEDERATION_INTERNAL_KEY=old-key-123") {
			t.Errorf("FEDERATION_INTERNAL_KEY should remain old:\n%s", s)
		}
	})

	t.Run("no-op when value already matches", func(t *testing.T) {
		already := "  svc:\n    environment:\n      - ENCRYPTION_KEY=new-key-456\n"
		out, n, _ := SetComposeEnvVars([]byte(already), "ENCRYPTION_KEY", "new-key-456")
		if n != 0 {
			t.Fatalf("expected 0 changed, got %d", n)
		}
		if string(out) != already {
			t.Errorf("content changed on no-op:\n%s", out)
		}
	})
}
