package searchdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEnsurePublishedSkipsAlreadyPublished(t *testing.T) {
	f := newPhantomExec()
	f.servicePorts = " 8123 "
	added, err := ensurePublished(f, "utmstack_clickhouse", []string{"8123:8123"})
	if err != nil {
		t.Fatalf("ensurePublished: %v", err)
	}
	if len(added) != 0 {
		t.Fatalf("added = %v, want none", added)
	}
	for _, c := range f.commands {
		if strings.Contains(c, "--publish-add") {
			t.Fatalf("unexpected publish-add: %v", f.commands)
		}
	}
}

func TestEnsurePublishedAddsMissingPorts(t *testing.T) {
	f := newPhantomExec()
	added, err := ensurePublished(f, "utmstack_clickhouse", []string{"9000:9000", "8123:8123"})
	if err != nil {
		t.Fatalf("ensurePublished: %v", err)
	}
	want := []string{"9000:9000", "8123:8123"}
	if len(added) != 2 || added[0] != want[0] || added[1] != want[1] {
		t.Fatalf("added = %v, want %v", added, want)
	}
	last := f.commands[len(f.commands)-1]
	if !strings.Contains(last, "--publish-add 9000:9000 --publish-add 8123:8123 utmstack_clickhouse") {
		t.Fatalf("last command = %q", last)
	}
}

func TestEnsurePublishedMixed(t *testing.T) {
	f := newPhantomExec()
	f.servicePorts = " 9000 "
	added, err := ensurePublished(f, "utmstack_clickhouse", []string{"9000:9000", "8123:8123"})
	if err != nil {
		t.Fatalf("ensurePublished: %v", err)
	}
	if len(added) != 1 || added[0] != "8123:8123" {
		t.Fatalf("added = %v, want [8123:8123]", added)
	}
}

func TestPortAlreadyPublished(t *testing.T) {
	f := newPhantomExec()
	f.servicePorts = " 9200 "
	if !portAlreadyPublished(f, "utmstack_node1", "9200:9200") {
		t.Fatal("9200 should be published")
	}
	if portAlreadyPublished(f, "utmstack_node1", "8123:8123") {
		t.Fatal("8123 should not be published")
	}
}

func TestWaitCHReadyWaitsForPing(t *testing.T) {
	var pings int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ping" {
			t.Errorf("path = %q", r.URL.Path)
		}
		pings++
		if pings < 2 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("Ok.\n"))
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	if err := waitCHReady(context.Background(), host); err != nil {
		t.Fatalf("waitCHReady: %v", err)
	}
	if pings < 2 {
		t.Fatalf("pings = %d", pings)
	}
}

func TestWaitCHReadyFailsClosedOnBadHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitCHReady(ctx, "127.0.0.1:1"); err == nil {
		t.Fatal("expected error for unreachable ClickHouse")
	}
}
