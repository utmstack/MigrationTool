package compose

import (
	"errors"
	"os"
	"testing"
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

func TestFindComposeEnvVarAcrossServices(t *testing.T) {
	// The same parser must work for any service/credential pair, not just
	// Postgres, because the migrate flow reads OpenSearch and ClickHouse
	// credentials from the live compose too.
	compose := `
services:
  node1:
    environment:
      - OPENSEARCH_INITIAL_ADMIN_PASSWORD=x
      - ELASTICSEARCH_PASSWORD=LSP#NdRs
  clickhouse:
    environment:
      - CLICKHOUSE_PASSWORD=3tJYT
  postgres:
    environment:
      - POSTGRES_PASSWORD=3tJYT
`
	if v, err := FindComposeEnvVar([]byte(compose), "node1", "ELASTICSEARCH_PASSWORD"); err != nil || v != "LSP#NdRs" {
		t.Errorf("node1/ELASTICSEARCH_PASSWORD = %q, %v", v, err)
	}
	if v, err := FindComposeEnvVar([]byte(compose), "clickhouse", "CLICKHOUSE_PASSWORD"); err != nil || v != "3tJYT" {
		t.Errorf("clickhouse/CLICKHOUSE_PASSWORD = %q, %v", v, err)
	}
	if _, err := FindComposeEnvVar([]byte(compose), "node1", "NOPE"); err == nil {
		t.Error("expected error for missing var")
	}
	if _, err := FindComposeEnvVar([]byte(compose), "missing", "X"); err == nil {
		t.Error("expected error for missing service")
	}
}

func TestReadComposeBytesFallsBackAcrossPaths(t *testing.T) {
	readPaths := []string{}
	ex := &staticExecutor{readFile: func(path string) ([]byte, error) {
		readPaths = append(readPaths, path)
		if path == CandidatePathForUser("utmstack") {
			return nil, os.ErrNotExist
		}
		return []byte(`services:
  postgres:
    environment:
      - POSTGRES_PASSWORD=fallback
`), nil
	}}
	b, err := ReadComposeBytes(ex, "utmstack")
	if err != nil {
		t.Fatalf("ReadComposeBytes: %v", err)
	}
	if v, err := FindComposeEnvVar(b, "postgres", "POSTGRES_PASSWORD"); err != nil || v != "fallback" {
		t.Errorf("fallback value = %q, %v", v, err)
	}
	if len(readPaths) < 2 {
		t.Errorf("expected multiple paths tried, got %v", readPaths)
	}
}
