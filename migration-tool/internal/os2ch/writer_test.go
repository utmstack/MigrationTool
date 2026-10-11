package os2ch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseDescribeColumns(t *testing.T) {
	body := "tenantId\tLowCardinality(String)\n@timestamp\tDateTime64(3, 'UTC')\nid\tString\n\t\n"
	cs := ParseDescribeColumns(body)
	for _, want := range []string{"tenantId", "@timestamp", "id"} {
		if _, ok := cs[want]; !ok {
			t.Errorf("column %q missing from %v", want, cs)
		}
	}
	if len(cs) != 3 {
		t.Errorf("parsed %d columns, want 3", len(cs))
	}
}

func TestCHWriterHTTPQueries(t *testing.T) {
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.Query().Get("query")
		switch {
		case lastQuery == "SELECT count() FROM utmstack.logs WHERE id = 'abc' LIMIT 1":
			w.Write([]byte("1"))
		case lastQuery == "SELECT count() FROM utmstack.logs":
			w.Write([]byte("42"))
		case lastQuery == "DESCRIBE TABLE utmstack.logs":
			w.Write([]byte("tenantId\tLowCardinality(String)\n@timestamp\tDateTime64(3, 'UTC')\n"))
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

	cols, err := w.DescribeColumns(ctx, DatasetLogs)
	if err != nil {
		t.Fatalf("DescribeColumns: %v", err)
	}
	if len(cols) != 2 {
		t.Fatalf("columns = %d, want 2", len(cols))
	}

	// Statistics rejects sample lookups (no id column).
	if _, err := w.SamplePresent(ctx, DatasetStatistics, "x"); err == nil {
		t.Fatal("expected statistics sample lookup to error")
	}
}
