package upgrade

import (
	"errors"
	"os"
	"testing"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
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
