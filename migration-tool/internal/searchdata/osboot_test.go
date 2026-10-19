package searchdata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/os2ch"
)

var errBoom = errors.New("boom")

type phantomExec struct {
	commands     []string
	runningOut   string // output of `docker ps -q --filter name=^...`
	presentOut   string // output of `docker ps -a -q --filter name=^...`
	presentImage string // if set, `docker image inspect <this>` succeeds
	servicePorts string // output of `docker service inspect --format .Endpoint.Spec.Ports`
}

func newPhantomExec() *phantomExec {
	return &phantomExec{}
}

func (f *phantomExec) Close() error { return nil }

func (f *phantomExec) ReadFile(path string) ([]byte, error) {
	return nil, os.ErrNotExist
}

func (f *phantomExec) WriteFile(path string, content []byte, _ os.FileMode) error { return nil }

func (f *phantomExec) ExecuteCommand(command string) (string, error) { return "", nil }

func (f *phantomExec) ExecuteCommandWithSudo(command string) (string, error) {
	f.commands = append(f.commands, command)
	switch {
	case strings.Contains(command, "docker ps -a -q --filter name=^"):
		return f.presentOut, nil
	case strings.Contains(command, "docker ps -q --filter name=^"):
		return f.runningOut, nil
	case strings.Contains(command, "docker image inspect "):
		img := strings.TrimPrefix(command, "docker image inspect ")
		img = strings.TrimSuffix(img, " >/dev/null 2>&1")
		if img == f.presentImage {
			return "{}", nil
		}
		return "", errBoom
	case strings.Contains(command, "docker service inspect"):
		return f.servicePorts, nil
	}
	return "", nil
}

func (f *phantomExec) ExecuteCommandWithSudoStreaming(command string) (string, error) {
	return f.ExecuteCommandWithSudo(command)
}

func (f *phantomExec) FileExists(path string) (bool, error) {
	return false, nil
}

func assertCommands(t *testing.T, f *phantomExec, want []string) {
	t.Helper()
	for i, w := range want {
		if i >= len(f.commands) || f.commands[i] != w {
			t.Errorf("command %d = %q, want %q (all: %v)", i, cmdAt(f, i), w, f.commands)
		}
	}
}

func cmdAt(f *phantomExec, i int) string {
	if i >= len(f.commands) {
		return "<none>"
	}
	return f.commands[i]
}

func TestPhantomBootPullsAndRuns(t *testing.T) {
	f := newPhantomExec()
	// image not present -> inspect fails, pull runs
	p := NewPhantom(f, "ghcr.io/utmstack/utmstack/opensearch:latest", "P@ss#w$rd")
	if err := p.Boot(); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	assertCommands(t, f, []string{
		"docker ps -q --filter name=^utmstack_migration_os$",
		"docker ps -a -q --filter name=^utmstack_migration_os$",
		"docker image inspect ghcr.io/utmstack/utmstack/opensearch:latest >/dev/null 2>&1",
		"docker pull ghcr.io/utmstack/utmstack/opensearch:latest",
		"docker run -d --name utmstack_migration_os -p 9200:9200 -v /utmstack/opensearch/data:/usr/share/opensearch/data -e cluster.name=utmstack -e node.name=node1 -e discovery.type=single-node -e bootstrap.memory_lock=false -e OPENSEARCH_INITIAL_ADMIN_PASSWORD='P@ss#w$rd' -e action.auto_create_index=true -e 'OPENSEARCH_JAVA_OPTS=-Xms512m -Xmx512m' -e network.host=0.0.0.0 ghcr.io/utmstack/utmstack/opensearch:latest",
	})
}

func TestPhantomBootReusesRunningContainer(t *testing.T) {
	f := newPhantomExec()
	f.runningOut = "abc123\n"
	p := NewPhantom(f, "img", "pass")
	if err := p.Boot(); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	if len(f.commands) != 1 {
		t.Fatalf("expected only the running check, got %v", f.commands)
	}
}

func TestPhantomBootStartsStoppedContainer(t *testing.T) {
	f := newPhantomExec()
	f.presentOut = "abc123\n"
	p := NewPhantom(f, "img", "pass")
	if err := p.Boot(); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	assertCommands(t, f, []string{
		"docker ps -q --filter name=^utmstack_migration_os$",
		"docker ps -a -q --filter name=^utmstack_migration_os$",
		"docker start utmstack_migration_os",
	})
}

func TestPhantomBootSkipsPullWhenImagePresent(t *testing.T) {
	f := newPhantomExec()
	f.presentImage = "img"
	p := NewPhantom(f, "img", "pass")
	if err := p.Boot(); err != nil {
		t.Fatalf("Boot: %v", err)
	}
	for _, c := range f.commands {
		if strings.Contains(c, "docker pull") {
			t.Fatalf("unexpected pull: %v", f.commands)
		}
	}
}

// TestPhantomReadyPollsUntilSearchable pins the readiness protocol the stream
// waits on: the cluster must stop initializing and a probe search must be
// served. A fresh boot reports zero shards before the volume is discovered,
// so shard counts alone are not a readiness signal.
func TestPhantomReadyPollsUntilSearchable(t *testing.T) {
	var healthCalls, probeCalls int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_cluster/health":
			healthCalls++
			w.WriteHeader(200)
			if healthCalls < 2 {
				w.Write([]byte(`{"initializing_shards":2,"active_primary_shards":0}`))
			} else {
				w.Write([]byte(`{"initializing_shards":0,"active_primary_shards":5}`))
			}
		case r.URL.Path == "/v11-*/_search":
			probeCalls++
			if probeCalls < 2 {
				w.WriteHeader(http.StatusNotFound)
			} else {
				w.WriteHeader(200)
				w.Write([]byte(`{"hits":{"hits":[]}}`))
			}
		default:
			t.Errorf("path = %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := os2ch.NewOSClient(os2ch.OSConfig{Host: srv.URL, User: "admin", Password: "pass"})
	ready := false
	for i := 0; i < 4 && !ready; i++ {
		ready, _ = client.Ready(context.Background())
	}
	if !ready {
		t.Fatalf("client never became ready (healthCalls=%d)", healthCalls)
	}
}

func TestPhantomTeardownRemovesContainer(t *testing.T) {
	f := newPhantomExec()
	p := NewPhantom(f, "img", "pass")
	if err := p.Teardown(); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	assertCommands(t, f, []string{"docker rm -f utmstack_migration_os"})
}

func TestPhantomBootRequiresCapturedValues(t *testing.T) {
	f := newPhantomExec()
	if err := NewPhantom(f, "", "pass").Boot(); err == nil {
		t.Fatal("expected error for empty image")
	}
	if err := NewPhantom(f, "img", "").Boot(); err == nil {
		t.Fatal("expected error for empty password")
	}
	if len(f.commands) != 0 {
		t.Fatalf("no docker command may run without captured values: %v", f.commands)
	}
}
