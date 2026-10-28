package cmd

import (
	"testing"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
)

func TestDeriveBridgeDestServerInPlaceUsesMainServer(t *testing.T) {
	cfg := &domain.MigrationConfig{
		Source:      domain.InstanceConfig{Host: "127.0.0.1"},
		Destination: domain.InstanceConfig{Host: ""},
	}
	yml := []byte("main_server: 10.11.11.139\ninternal_key: abc\n")
	server, fallback := deriveBridgeDestServer(cfg, yml, true)
	if server != "10.11.11.139" {
		t.Fatalf("server = %q, want 10.11.11.139 (main_server wins over 127.0.0.1)", server)
	}
	if fallback {
		t.Fatal("fallback = true, want false when main_server is present")
	}
}

func TestDeriveBridgeDestServerInPlaceWithoutYMLFallsBack(t *testing.T) {
	cfg := &domain.MigrationConfig{
		Source:      domain.InstanceConfig{Host: "127.0.0.1"},
		Destination: domain.InstanceConfig{Host: ""},
	}
	server, fallback := deriveBridgeDestServer(cfg, nil, true)
	if server != "127.0.0.1" {
		t.Fatalf("server = %q, want 127.0.0.1 fallback", server)
	}
	if !fallback {
		t.Fatal("fallback = false, want true when utmstack.yml is unavailable")
	}
}

func TestDeriveBridgeDestServerMigrationUnchanged(t *testing.T) {
	cfg := &domain.MigrationConfig{
		Source:      domain.InstanceConfig{Host: "127.0.0.1"},
		Destination: domain.InstanceConfig{Host: "10.0.0.2"},
	}
	yml := []byte("main_server: 10.11.11.139\n")
	server, fallback := deriveBridgeDestServer(cfg, yml, true)
	if server != "10.0.0.2" {
		t.Fatalf("server = %q, want 10.0.0.2 (explicit destination wins)", server)
	}
	if fallback {
		t.Fatal("fallback = true, want false for explicit destination")
	}
}

func TestDeriveBridgeDestServerRemoteUnchanged(t *testing.T) {
	cfg := &domain.MigrationConfig{
		Source:      domain.InstanceConfig{Host: "10.11.11.5"},
		Destination: domain.InstanceConfig{Host: ""},
	}
	yml := []byte("main_server: 10.11.11.139\n")
	server, fallback := deriveBridgeDestServer(cfg, yml, false)
	if server != "10.11.11.5" {
		t.Fatalf("server = %q, want 10.11.11.5 (remote source, no local yml)", server)
	}
	if fallback {
		t.Fatal("fallback = true, want false when source host is usable")
	}
}
