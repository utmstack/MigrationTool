package os2ch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeOS serves a two-page scroll for one index, plus _cat/indices and _count.
func fakeOS(t *testing.T) *httptest.Server {
	t.Helper()

	page1 := []map[string]interface{}{
		{"id": "1", "dataType": "linux", "event": map[string]interface{}{"a": 1}},
		{"id": "2", "dataType": "linux"},
	}
	page2 := []map[string]interface{}{
		{"id": "3", "dataType": "o365"},
	}

	hitsFrom := func(docs []map[string]interface{}) map[string]interface{} {
		hits := make([]map[string]interface{}, 0, len(docs))
		for _, d := range docs {
			id, _ := d["id"].(string)
			hits = append(hits, map[string]interface{}{"_id": id, "_source": d})
		}
		return map[string]interface{}{"_scroll_id": "s-1", "hits": map[string]interface{}{"hits": hits}}
	}

	// scrollsSeen counts /_search/scroll requests so the first continuation
	// returns page2 and the second returns an empty page (end of scroll).
	scrollsSeen := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/_cat/indices" && r.URL.Query().Get("format") == "json":
			_ = json.NewEncoder(w).Encode([]interface{}{
				map[string]interface{}{"index": "v11-log-linux-2026-10-11"},
				map[string]interface{}{"index": "v11-backend-logs"},
			})
		case r.URL.Path == "/v11-log-linux-2026-10-11/_search":
			_ = json.NewEncoder(w).Encode(hitsFrom(page1))
		case r.URL.Path == "/_search/scroll":
			scrollsSeen++
			if scrollsSeen >= 2 {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"hits": map[string]interface{}{"hits": []interface{}{}}})
			} else {
				_ = json.NewEncoder(w).Encode(hitsFrom(page2))
			}
		case r.URL.Path == "/v11-log-linux-2026-10-11/_count":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"count": 3})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestScrollPages(t *testing.T) {
	srv := fakeOS(t)
	defer srv.Close()

	c := NewOSClient(OSConfig{Host: srv.URL})
	var got []string
	n, err := c.Scroll(context.Background(), "v11-log-linux-2026-10-11", 2, func(id string, doc map[string]interface{}) error {
		got = append(got, id)
		return nil
	})
	if err != nil {
		t.Fatalf("Scroll: %v", err)
	}
	if n != 3 {
		t.Fatalf("read %d docs, want 3", n)
	}
	want := []string{"1", "2", "3"}
	if len(got) != len(want) {
		t.Fatalf("got %d ids %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got ids %v, want %v", got, want)
		}
	}
}

func TestListIndices(t *testing.T) {
	srv := fakeOS(t)
	defer srv.Close()
	c := NewOSClient(OSConfig{Host: srv.URL})
	idx, err := c.ListIndices(context.Background())
	if err != nil {
		t.Fatalf("ListIndices: %v", err)
	}
	if len(idx) != 2 || idx[0] != "v11-backend-logs" || idx[1] != "v11-log-linux-2026-10-11" {
		t.Fatalf("indices = %v", idx)
	}
}
