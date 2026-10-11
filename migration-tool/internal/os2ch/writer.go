package os2ch

import (
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

// CHWriter inserts mapped rows into a ClickHouse table. It mirrors the V12
// tools/os2ch reference: inserts go over the native protocol (9000) as
// JSONEachRow, because the HTTP interface rejects the RFC3339 timestamps and
// nested objects these documents carry. Count and connectivity checks use the
// HTTP interface (8123).
type CHWriter struct {
	native     driver.Conn
	httpClient *http.Client
	base       string
	user       string
	pass       string
	database   string
	log        *slog.Logger
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
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.Addr},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
			Username: cfg.User,
			Password: cfg.Password,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("open clickhouse native: %w", err)
	}
	if err := conn.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping clickhouse: %w", err)
	}
	base := cfg.HTTPAddr
	if base == "" {
		base = "http://" + strings.SplitN(cfg.Addr, ":", 2)[0] + ":8123"
	}
	return &CHWriter{
		native:     conn,
		httpClient: &http.Client{Timeout: 60 * time.Second},
		base:       strings.TrimSuffix(base, "/"),
		user:       cfg.User,
		pass:       cfg.Password,
		database:   cfg.Database,
		log:        logger,
	}, nil
}

func (w *CHWriter) Close() error {
	if w.native == nil {
		return nil
	}
	return w.native.Close()
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

// Insert appends a batch of already-mapped documents to the dataset's table
// as one JSONEachRow AsyncInsert over the native protocol.
func (w *CHWriter) Insert(ctx context.Context, ds Dataset, docs []map[string]interface{}) error {
	table := TableFor(ds)
	if table == "" {
		return fmt.Errorf("unknown dataset %q", ds)
	}
	if len(docs) == 0 {
		return nil
	}
	var rows bytes.Buffer
	for _, doc := range docs {
		line, err := MarshalRow(doc)
		if err != nil {
			return fmt.Errorf("marshal row: %w", err)
		}
		rows.WriteString(line)
		rows.WriteByte('\n')
	}
	batch := fmt.Sprintf("INSERT INTO %s FORMAT JSONEachRow\n%s", table, rows.String())
	if err := w.native.AsyncInsert(ctx, batch, true); err != nil {
		return fmt.Errorf("async insert into %s: %w", table, err)
	}
	w.log.Debug("inserted batch", "table", table, "rows", len(docs))
	return nil
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

// DescribeColumns returns the destination table's column names for a dataset,
// read live from ClickHouse so the orphan report compares against the schema
// that actually exists (not a hardcoded copy).
func (w *CHWriter) DescribeColumns(ctx context.Context, ds Dataset) (ColumnSet, error) {
	table := TableFor(ds)
	if table == "" {
		return nil, fmt.Errorf("unknown dataset %q", ds)
	}
	body, err := w.httpQuery(ctx, "DESCRIBE TABLE "+table)
	if err != nil {
		return nil, err
	}
	return ParseDescribeColumns(string(body)), nil
}

// ParseDescribeColumns splits a DESCRIBE TABLE result into its column names.
// Each line is <name>\t<type>\t[comment]; we only need the name.
func ParseDescribeColumns(body string) ColumnSet {
	cs := make(ColumnSet)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if name, _, ok := strings.Cut(line, "\t"); ok {
			cs[name] = struct{}{}
		}
	}
	return cs
}
