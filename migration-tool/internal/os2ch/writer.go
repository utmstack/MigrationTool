package os2ch

import (
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

// CHWriter inserts mapped rows into a ClickHouse table over HTTP 8123:
// streaming uses InsertFormat, the count/connectivity checks use REST.
type CHWriter struct {
	http       driver.Conn
	httpClient *http.Client
	base       string
	user       string
	pass       string
	database   string
	log        *slog.Logger
}

// CHClient is the ClickHouse surface the engine depends on: streaming inserts
// and read counts for verification. *CHWriter implements it; tests inject a
// fake.
type CHClient interface {
	InsertTable(ctx context.Context, table string, r io.Reader) error
	CountRows(ctx context.Context, ds Dataset) (int64, error)
	SamplePresent(ctx context.Context, ds Dataset, id string) (bool, error)
	Close() error
}

type CHConfig struct {
	HTTPAddr string // host:8123
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
	if cfg.HTTPAddr == "" {
		return nil, fmt.Errorf("CHConfig needs HTTPAddr")
	}
	base := cfg.HTTPAddr
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
		return nil, fmt.Errorf("open clickhouse http: %w", err)
	}
	if err := httpConn.Ping(ctx); err != nil {
		httpConn.Close()
		return nil, fmt.Errorf("ping clickhouse http: %w", err)
	}
	return &CHWriter{
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
	if w.http == nil {
		return nil
	}
	return w.http.Close()
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
