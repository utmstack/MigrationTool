package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExtractMainServer(t *testing.T) {
	yml := []byte("main_server: 10.11.11.139\ninternal_key: abc\n")
	s, err := ExtractMainServer(yml)
	if err != nil {
		t.Fatal(err)
	}
	if s != "10.11.11.139" {
		t.Fatalf("s = %q, want 10.11.11.139", s)
	}
}

func TestExtractMainServerMissing(t *testing.T) {
	_, err := ExtractMainServer([]byte("internal_key: abc\n"))
	if err == nil {
		t.Fatal("want error when main_server is absent")
	}
	if !strings.Contains(err.Error(), "main_server") {
		t.Fatalf("err = %q, want mention of main_server", err.Error())
	}
}

func TestExtractMainServerBadYAML(t *testing.T) {
	if _, err := ExtractMainServer([]byte("::: not yaml")); err == nil {
		t.Fatal("want error for invalid yaml")
	}
	_ = yaml.Marshal
}
