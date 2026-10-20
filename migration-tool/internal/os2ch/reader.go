package os2ch

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// OSClient talks to an OpenSearch REST endpoint. It lists indices and scrolls
// one index at a time, feeding each document to a callback. It trusts
// self-signed certificates (in-cluster TLS) and uses basic auth.
type OSClient struct {
	base   string
	user   string
	pass   string
	client *http.Client
}

type OSConfig struct {
	Host     string // host:port
	User     string
	Password string
}

func NewOSClient(cfg OSConfig) *OSClient {
	base := cfg.Host
	if !strings.HasPrefix(base, "http") {
		base = "https://" + base
	}
	return &OSClient{
		base: strings.TrimSuffix(base, "/"),
		user: cfg.User,
		pass: cfg.Password,
		client: &http.Client{
			Timeout: 10 * time.Minute,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// ListIndices returns every index name, sorted.
func (c *OSClient) ListIndices(ctx context.Context) ([]string, error) {
	var resp []struct {
		Index string `json:"index"`
	}
	if err := c.do(ctx, http.MethodGet, "/_cat/indices?format=json", nil, &resp); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(resp))
	for _, in := range resp {
		names = append(names, in.Index)
	}
	sort.Strings(names)
	return names, nil
}

type osSearchResponse struct {
	ScrollID string `json:"_scroll_id"`
	Hits     struct {
		Hits []struct {
			ID     string                 `json:"_id"`
			Source map[string]interface{} `json:"_source"`
		} `json:"hits"`
	} `json:"hits"`
}

// Scroll reads one index via the scroll API, calling onPage per document.
// It returns the number of documents read.
func (c *OSClient) Scroll(ctx context.Context, index string, pageSize int, onPage func(id string, doc map[string]interface{}) error) (int, error) {
	if pageSize <= 0 {
		pageSize = 2000
	}
	var scrollID string
	total := 0
	for {
		var r osSearchResponse
		var (
			path string
			body []byte
		)
		if scrollID == "" {
			path = "/" + url.PathEscape(index) + "/_search?scroll=2m"
			body, _ = json.Marshal(map[string]interface{}{"size": pageSize})
		} else {
			path = "/_search/scroll?scroll=2m"
			body, _ = json.Marshal(map[string]interface{}{"scroll_id": scrollID})
		}
		if err := c.do(ctx, http.MethodPost, path, body, &r); err != nil {
			return total, err
		}
		scrollID = r.ScrollID
		for _, h := range r.Hits.Hits {
			if err := onPage(h.ID, h.Source); err != nil {
				return total, err
			}
			total++
		}
		if len(r.Hits.Hits) < pageSize {
			break
		}
	}
	return total, nil
}

// Config returns the original connection settings of this client, so callers
// can build other endpoints against the same host/credentials.
func (c *OSClient) Config() OSConfig {
	host := c.base
	for _, p := range []string{"https://", "http://"} {
		host = strings.TrimPrefix(host, p)
	}
	return OSConfig{Host: host, User: c.user, Password: c.pass}
}

// ClusterStatus returns shard counts from GET /_cluster/health.
type ClusterStatus struct {
	Initializing  int `json:"initializing_shards"`
	ActivePrimary int `json:"active_primary_shards"`
}

func (c *OSClient) Status(ctx context.Context) (ClusterStatus, error) {
	var resp ClusterStatus
	if err := c.do(ctx, http.MethodGet, "/_cluster/health", nil, &resp); err != nil {
		return ClusterStatus{}, err
	}
	return resp, nil
}

// InScopeSearchable reports whether a search over the v11-* tenant indices
// is served (200). 404 means the indices are not (yet) discovered; 5xx means
// the cluster is up but shards are not serving searches.
func (c *OSClient) InScopeSearchable(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v11-*/_search?size=0", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.user, c.pass)
	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("OS probe /v11-*/_search: %s: %s", resp.Status, truncate(body, 200))
	}
}

// Ready reports whether the tenant indices are safe to read: no shard is still
// initializing and, when any primary exists, a probe search is served. A fresh
// boot reports zero shards before the volume is discovered, so this is the only
// reliable stop-waiting signal. An empty cluster (no primaries) is ready.
func (c *OSClient) Ready(ctx context.Context) (bool, error) {
	st, err := c.Status(ctx)
	if err != nil {
		return false, err
	}
	if st.Initializing > 0 {
		return false, nil
	}
	if st.ActivePrimary == 0 {
		return true, nil
	}
	return c.InScopeSearchable(ctx)
}

func (c *OSClient) do(ctx context.Context, method, path string, body []byte, out interface{}) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("OS %s %s: %s: %s", method, path, resp.Status, truncate(data, 300))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func truncate(b []byte, n int) string {
	s := strings.TrimSpace(string(b))
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
