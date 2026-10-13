package os2ch

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// CHWriter inserts mapped rows into a ClickHouse table. Native inserts go
// over 9000; streaming (InsertFormat) and the count/connectivity checks go
// over HTTP 8123.
type CHWriter struct {
	native     driver.Conn
	http       driver.Conn
	httpClient *http.Client
	base       string
	user       string
	pass       string
	database   string
	log        *slog.Logger
}

// CHClient is the ClickHouse surface the engine depends on: write (stream or
// file) and read counts for verification. *CHWriter implements it; tests
// inject a fake.
type CHClient interface {
	InsertTable(ctx context.Context, table string, r io.Reader) error
	InsertFile(ctx context.Context, ds Dataset, r io.Reader) error
	CountRows(ctx context.Context, ds Dataset) (int64, error)
	SamplePresent(ctx context.Context, ds Dataset, id string) (bool, error)
	Close() error
}

type CHConfig struct {
	Addr     string // host:9000 native
	HTTPAddr string // host:8123 HTTP (optional; derived from Addr if empty)
	User     string
	Password string
	Database string // utmstack
}

func NewCHWriter(ctx context.Context, cfg CHConfig, logger *slog.Logger) (*CHWriter, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.User == "" {
		cfg.User = "default"
	}
	if cfg.Addr == "" && cfg.HTTPAddr == "" {
		return nil, fmt.Errorf("CHConfig needs Addr (native) or HTTPAddr")
	}
	var conn driver.Conn
	if cfg.Addr != "" {
		// Bulk inserts of large NDJSON files can take minutes on small VMs while
		// ClickHouse merges, so the read timeout must comfortably exceed the
		// default 53s.
		var err error
		conn, err = clickhouse.Open(&clickhouse.Options{
			Addr: []string{cfg.Addr},
			Auth: clickhouse.Auth{
				Database: cfg.Database,
				Username: cfg.User,
				Password: cfg.Password,
			},
			ReadTimeout: 30 * time.Minute,
		})
		if err != nil {
			return nil, fmt.Errorf("open clickhouse native: %w", err)
		}
		if err := conn.Ping(ctx); err != nil {
			conn.Close()
			return nil, fmt.Errorf("ping clickhouse: %w", err)
		}
	}
	base := cfg.HTTPAddr
	if base == "" {
		base = "http://" + strings.SplitN(cfg.Addr, ":", 2)[0] + ":8123"
	}
	// httpQuery builds raw REST URLs, which need the scheme; the driver adds
	// it itself, so normalize here.
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	httpConn, err := clickhouse.Open(&clickhouse.Options{
		Addr:     []string{strings.TrimPrefix(base, "http://")},
		Protocol: clickhouse.HTTP,
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.User,
			Password: cfg.Password,
		},
		Compression: &clickhouse.Compression{Method: clickhouse.CompressionGZIP},
		ReadTimeout: 90 * time.Minute,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("open clickhouse http: %w", err)
	}
	if err := httpConn.Ping(ctx); err != nil {
		conn.Close()
		httpConn.Close()
		return nil, fmt.Errorf("ping clickhouse http: %w", err)
	}
	return &CHWriter{
		native:     conn,
		http:       httpConn,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		base:       strings.TrimSuffix(base, "/"),
		user:       cfg.User,
		pass:       cfg.Password,
		database:   cfg.Database,
		log:        logger,
	}, nil
}

func (w *CHWriter) Close() error {
	var first error
	if w.native != nil {
		first = w.native.Close()
	}
	if w.http != nil {
		if err := w.http.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// TableFor maps a dataset to its ClickHouse table name.
func TableFor(ds Dataset) string {
	switch ds {
	case DatasetLogs:
		return "utmstack.logs"
	case DatasetAlerts:
		return "utmstack.alerts"
	case DatasetStatistics:
		return "utmstack.statistics"
	default:
		return ""
	}
}

// insertChunkBytes caps how many raw bytes are buffered into one AsyncInsert
// so a single large NDJSON file never holds an unbounded in-memory string.
const insertChunkBytes = 64 * 1024 * 1024

// InsertFile loads one NDJSON reader into the dataset's table in large
// chunks, issuing one awaited AsyncInsert per chunk. A native-protocol Exec
// does not accept an io.Reader, so the rows are embedded in the query text,
// matching the proven V12 os2ch reference.
func (w *CHWriter) InsertFile(ctx context.Context, ds Dataset, r io.Reader) error {
	table := TableFor(ds)
	if table == "" {
		return fmt.Errorf("unknown dataset %q", ds)
	}

	var (
		scanner = bufio.NewScanner(r)
		chunk   bytes.Buffer
	)
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for scanner.Scan() {
		if chunk.Len() > 0 {
			chunk.WriteByte('\n')
		}
		chunk.Write(scanner.Bytes())
		if chunk.Len() >= insertChunkBytes {
			if err := w.insertChunk(ctx, table, &chunk); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read ndjson for %s: %w", table, err)
	}
	if chunk.Len() > 0 {
		if err := w.insertChunk(ctx, table, &chunk); err != nil {
			return err
		}
	}
	w.log.Debug("bulk insert", "table", table)
	return nil
}

func (w *CHWriter) insertChunk(ctx context.Context, table string, chunk *bytes.Buffer) error {
	query := fmt.Sprintf("INSERT INTO %s FORMAT JSONEachRow\n%s", table, chunk.String())
	if err := w.native.AsyncInsert(ctx, query, true); err != nil {
		return fmt.Errorf("bulk insert into %s: %w", table, err)
	}
	chunk.Reset()
	return nil
}

// InsertTable streams one JSONEachRow reader into the named table as a
// single atomic statement; a failed insert lands no rows.
func (w *CHWriter) InsertTable(ctx context.Context, table string, r io.Reader) error {
	if w.http == nil {
		return fmt.Errorf("no HTTP ClickHouse connection for %s", table)
	}
	return w.http.InsertFormat(ctx, "JSONEachRow", "INSERT INTO "+table, r)
}

// CountRows returns the total row count of a dataset's table via HTTP.
func (w *CHWriter) CountRows(ctx context.Context, ds Dataset) (int64, error) {
	table := TableFor(ds)
	if table == "" {
		return 0, fmt.Errorf("unknown dataset %q", ds)
	}
	body, err := w.httpQuery(ctx, "SELECT count() FROM "+table)
	if err != nil {
		return 0, err
	}
	var n int64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(body)), "%d", &n); err != nil {
		return 0, fmt.Errorf("parse count %q: %w", body, err)
	}
	return n, nil
}

// httpQuery runs a read query over the HTTP interface.
func (w *CHWriter) httpQuery(ctx context.Context, q string) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/?%s", w.base, url.Values{
		"database": {w.database},
		"user":     {w.user},
		"password": {w.pass},
		"query":    {q},
	}.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CH %s: %s: %s", q, resp.Status, truncate(b, 400))
	}
	return b, nil
}
