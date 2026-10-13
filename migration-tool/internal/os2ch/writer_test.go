package os2ch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCHWriterHTTPQueries(t *testing.T) {
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.Query().Get("query")
		switch {
		case lastQuery == "SELECT count() FROM utmstack.logs WHERE id = 'abc' LIMIT 1":
			w.Write([]byte("1"))
		case lastQuery == "SELECT count() FROM utmstack.logs":
			w.Write([]byte("42"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	w := &CHWriter{base: srv.URL, database: "utmstack", httpClient: &http.Client{}}
	ctx := context.Background()

	n, err := w.CountRows(ctx, DatasetLogs)
	if err != nil {
		t.Fatalf("CountRows: %v", err)
	}
	if n != 42 {
		t.Fatalf("count = %d, want 42", n)
	}

	found, err := w.SamplePresent(ctx, DatasetLogs, "abc")
	if err != nil {
		t.Fatalf("SamplePresent: %v", err)
	}
	if !found {
		t.Fatal("expected sample id found")
	}

	// Statistics rejects sample lookups (no id column).
	if _, err := w.SamplePresent(ctx, DatasetStatistics, "x"); err == nil {
		t.Fatal("expected statistics sample lookup to error")
	}
}
