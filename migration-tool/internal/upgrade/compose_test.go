package upgrade

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/searchdata"
)

type staticExecutor struct {
	readFile func(path string) ([]byte, error)
}

func (s *staticExecutor) ExecuteCommand(cmd string) (string, error) {
	return s.ExecuteCommandWithSudo(cmd)
}
func (s *staticExecutor) ExecuteCommandWithSudo(cmd string) (string, error) {
	return "", errors.New("not used")
}
func (s *staticExecutor) ExecuteCommandWithSudoStreaming(cmd string) (string, error) {
	return "", errors.New("not used")
}
func (s *staticExecutor) FileExists(path string) (bool, error) { return true, nil }
func (s *staticExecutor) ReadFile(path string) ([]byte, error) {
	if s.readFile != nil {
		return s.readFile(path)
	}
	return nil, errors.New("no file")
}
func (s *staticExecutor) WriteFile(path string, data []byte, perm os.FileMode) error {
	return nil
}
func (s *staticExecutor) Close() error { return nil }

func TestGetPostgresPasswordFromComposeUsesYAMLKeyNotSwarmName(t *testing.T) {
	// The installer writes the key "postgres"; the swarm service is
	// utmstack_postgres. The parser must look up the YAML key.
	compose := `
services:
  postgres:
    environment:
      - POSTGRES_PASSWORD=s3cret
      - PGDATA=/var/lib/postgresql/data/pgdata
  frontend:
    environment:
      - X=1
`
	u := &Upgrader{
		executor: &staticExecutor{readFile: func(path string) ([]byte, error) {
			return []byte(compose), nil
		}},
	}

	pass, err := u.GetPostgresPasswordFromCompose()
	if err != nil {
		t.Fatalf("password lookup failed: %v", err)
	}
	if pass != "s3cret" {
		t.Errorf("password = %q, want s3cret", pass)
	}
}

func TestCaptureOSReadsImageAndPassword(t *testing.T) {
	compose := `
services:
  node1:
    image: ghcr.io/utmstack/utmstack/opensearch:latest
    environment:
      - OPENSEARCH_INITIAL_ADMIN_PASSWORD=LSPNdRsZAyL#1+P!
  postgres:
    image: postgres:16
`
	u := &Upgrader{
		sourceCfg: domain.InstanceConfig{SSHUser: "utmstack"},
		executor:  &staticExecutor{readFile: func(string) ([]byte, error) { return []byte(compose), nil }},
	}
	image, pass, err := u.captureOS()
	if err != nil {
		t.Fatalf("captureOS: %v", err)
	}
	if image != "ghcr.io/utmstack/utmstack/opensearch:latest" {
		t.Errorf("image = %q", image)
	}
	if pass != "LSPNdRsZAyL#1+P!" {
		t.Errorf("pass = %q", pass)
	}
}

func TestCaptureOSFallsBackToElasticVar(t *testing.T) {
	compose := `
services:
  node1:
    image: ghcr.io/utmstack/utmstack/opensearch:1.3.14
    environment:
      - ELASTICSEARCH_PASSWORD=espass
`
	u := &Upgrader{
		sourceCfg: domain.InstanceConfig{SSHUser: "utmstack"},
		executor:  &staticExecutor{readFile: func(string) ([]byte, error) { return []byte(compose), nil }},
	}
	_, pass, err := u.captureOS()
	if err != nil {
		t.Fatalf("captureOS: %v", err)
	}
	if pass != "espass" {
		t.Errorf("pass = %q, want espass", pass)
	}
}

func TestCaptureOSFailsWhenNode1Missing(t *testing.T) {
	compose := `
services:
  postgres:
    image: postgres:16
`
	u := &Upgrader{
		sourceCfg: domain.InstanceConfig{SSHUser: "utmstack"},
		executor:  &staticExecutor{readFile: func(string) ([]byte, error) { return []byte(compose), nil }},
	}
	if _, _, err := u.captureOS(); err == nil {
		t.Fatal("expected error when node1 service missing")
	}
}

func TestCaptureOSFailsWhenNoPasswordVar(t *testing.T) {
	compose := `
services:
  node1:
    image: ghcr.io/utmstack/utmstack/opensearch:latest
    environment:
      - cluster.name=utmstack
`
	u := &Upgrader{
		sourceCfg: domain.InstanceConfig{SSHUser: "utmstack"},
		executor:  &staticExecutor{readFile: func(string) ([]byte, error) { return []byte(compose), nil }},
	}
	if _, _, err := u.captureOS(); err == nil {
		t.Fatal("expected error when no OpenSearch password var present")
	}
}

func TestStreamPhantomDataTearsDownOnlyOnSuccess(t *testing.T) {
	ex := &dockerExecutor{}
	u := &Upgrader{
		executor:   ex,
		osImage:    "img",
		osPassword: "pw",
		streamFn:   func(a, p string) error { return nil },
	}
	if err := u.streamPhantomData(); err != nil {
		t.Fatalf("streamPhantomData: %v", err)
	}
	teardownSeen := false
	for _, c := range ex.commands {
		if strings.Contains(c, "docker rm -f "+searchdata.PhantomName) {
			teardownSeen = true
		}
	}
	if !teardownSeen {
		t.Fatalf("expected teardown after clean stream, commands: %v", ex.commands)
	}
}

func TestStreamPhantomDataKeepsNodeOnStreamError(t *testing.T) {
	ex := &dockerExecutor{}
	u := &Upgrader{
		executor:   ex,
		osImage:    "img",
		osPassword: "pw",
		streamFn:   func(a, p string) error { return errors.New("stream boom") },
	}
	if err := u.streamPhantomData(); err == nil || err.Error() != "stream boom" {
		t.Fatalf("err = %v, want stream boom", err)
	}
	for _, c := range ex.commands {
		if strings.Contains(c, "docker rm -f ") {
			t.Fatalf("phantom must stay up on stream error: %v", ex.commands)
		}
	}
}

func TestGetPostgresPasswordFromComposeMissingService(t *testing.T) {
	compose := `
services:
  frontend:
    environment:
      - POSTGRES_PASSWORD=wrong-service
`
	u := &Upgrader{
		executor: &staticExecutor{readFile: func(path string) ([]byte, error) {
			return []byte(compose), nil
		}},
	}
	if _, err := u.GetPostgresPasswordFromCompose(); err == nil {
		t.Fatal("expected error when postgres service missing")
	}
}

func TestGetPostgresPasswordFromComposeFallsBackToSecondPath(t *testing.T) {
	// The home compose is missing (first install vs updater self-copy); the
	// root path must be tried before the password lookup fails.
	compose := `
services:
  postgres:
    environment:
      - POSTGRES_PASSWORD=fb-secret
`
	readPaths := []string{}
	u := &Upgrader{
		sourceCfg: domain.InstanceConfig{SSHUser: "utmstack"},
		executor: &staticExecutor{readFile: func(path string) ([]byte, error) {
			readPaths = append(readPaths, path)
			if path == "/home/utmstack/compose.yml" {
				return nil, os.ErrNotExist
			}
			return []byte(compose), nil
		}},
	}
	pass, err := u.GetPostgresPasswordFromCompose()
	if err != nil {
		t.Fatalf("fallback lookup failed: %v", err)
	}
	if pass != "fb-secret" {
		t.Errorf("password = %q, want fb-secret", pass)
	}
	if len(readPaths) < 2 {
		t.Errorf("expected at least 2 paths tried, got %v", readPaths)
	}
}

// dockerExecutor emulates the docker queries Phantom.Boot issues, so the
// streamPhantomData orchestration can be tested without a real engine.
type dockerExecutor struct {
	staticExecutor
	commands []string
}

func (d *dockerExecutor) ExecuteCommandWithSudo(cmd string) (string, error) {
	d.commands = append(d.commands, cmd)
	switch {
	case strings.Contains(cmd, "docker ps -a -q"):
		// No existing container: force the pull+run path.
		return "", nil
	case strings.Contains(cmd, "docker ps -q"):
		return "", nil
	case strings.Contains(cmd, "docker image inspect"):
		return "{}", nil
	case strings.HasPrefix(cmd, "docker run "):
		return "cid", nil
	}
	return "", nil
}
