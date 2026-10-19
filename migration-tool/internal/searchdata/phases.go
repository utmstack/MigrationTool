package searchdata

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/compose"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/os2ch"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

var osPasswordVars = []string{"OPENSEARCH_INITIAL_ADMIN_PASSWORD", "ELASTICSEARCH_PASSWORD"}

type Config struct {
	SourceHost    string
	DestHost      string
	SourceExec    remote.CommandExecutor
	DestExec      remote.CommandExecutor
	SourceSSHUser string
	DestSSHUser   string
}

type Phases struct {
	cfg      Config
	dumpDir  string
	log      *slog.Logger
	dumped   bool
	loaded   bool
	streamed bool
}

func NewPhases(cfg Config) (*Phases, error) {
	dumpDir, err := ResolveDumpDir()
	if err != nil {
		return nil, fmt.Errorf("resolve dump dir: %w", err)
	}
	p := &Phases{cfg: cfg, dumpDir: dumpDir, log: slog.New(slog.NewTextHandler(os.Stderr, nil))}
	if strings.HasPrefix(dumpDir, "/utmstack/") {
		ui.Warning(dumpDir + " is inside /utmstack, which the V12 cleanup removes; the dump will be lost.")
		ui.Warning("Run the tool from another directory (e.g. your home dir) so the dump survives the cleanup.")
	}
	return p, nil
}

func (p *Phases) DumpDir() string { return p.dumpDir }

// SetDestExec attaches the destination executor once it is available, because
// in migrate the destination SSH is only established after the Postgres import.
func (p *Phases) SetDestExec(exec remote.CommandExecutor) {
	if p.cfg.DestExec == nil && exec != nil {
		p.cfg.DestExec = exec
	}
}

// RunStream copies OpenSearch to ClickHouse directly, over the wire: source
// 9200 and destination 8123 are exposed only if not already, and no NDJSON is
// written to disk. It is the --with-data path for migrate, where both
// endpoints are alive at the same time. The upgrade uses StreamPhantom with
// the same stream core.
func (p *Phases) RunStream(ctx context.Context) error {
	if p.streamed {
		return nil
	}
	if p.cfg.DestExec == nil {
		return fmt.Errorf("no destination executor: --with-data requires SSH access to the destination")
	}
	osPass, err := p.composeEnv(p.cfg.SourceExec, p.cfg.SourceSSHUser, "node1", osPasswordVars, "OpenSearch admin password in source compose")
	if err != nil {
		return err
	}
	chPass, err := p.composeEnv(p.cfg.DestExec, p.cfg.DestSSHUser, "clickhouse", []string{"CLICKHOUSE_PASSWORD"}, "ClickHouse password in destination compose")
	if err != nil {
		return err
	}
	var addedOS, addedCH []string
	defer p.unpublish(p.cfg.SourceExec, "utmstack_node1", addedOS...)
	defer p.unpublish(p.cfg.DestExec, "utmstack_clickhouse", addedCH...)
	addedOS, err = ensurePublished(p.cfg.SourceExec, "utmstack_node1", []string{"9200:9200"})
	if err != nil {
		return fmt.Errorf("publish source OpenSearch 9200: %w", err)
	}
	addedCH, err = ensurePublished(p.cfg.DestExec, "utmstack_clickhouse", []string{"8123:8123"})
	if err != nil {
		return fmt.Errorf("publish destination ClickHouse 8123: %w", err)
	}
	if len(addedOS) > 0 || len(addedCH) > 0 {
		ui.Success(fmt.Sprintf("Published ports: source %v destination %v", addedOS, addedCH))
	} else {
		ui.Info("Source OpenSearch and destination ClickHouse ports already published")
	}
	osClient := os2ch.NewOSClient(os2ch.OSConfig{Host: p.cfg.SourceHost + ":9200", User: "admin", Password: osPass})
	if err := waitOSReady(ctx, osClient); err != nil {
		return fmt.Errorf("wait for source OpenSearch ready: %w", err)
	}
	if err := waitCHReady(ctx, p.cfg.DestHost+":8123"); err != nil {
		return fmt.Errorf("wait for destination ClickHouse ready: %w", err)
	}
	engine, err := os2ch.NewEngine(ctx, os2ch.EngineConfig{
		OS:  osClient.Config(),
		CH:  os2ch.CHConfig{HTTPAddr: p.cfg.DestHost + ":8123", User: "default", Password: chPass, Database: "utmstack"},
		Log: p.log,
	})
	if err != nil {
		return fmt.Errorf("os2ch engine: %w", err)
	}
	defer engine.Close()

	// The stream manifest lives under the dump dir so a crashed run can be
	// resumed without re-streaming committed indices. Nothing else is written
	// to disk in stream mode.
	stateDir := filepath.Join(p.dumpDir, "stream")
	if err := p.streamCore(ctx, engine, stateDir); err != nil {
		return err
	}
	p.streamed = true
	return nil
}

// streamCore runs StreamAll plus per-dataset verification against engine.
func (p *Phases) streamCore(ctx context.Context, engine *os2ch.Engine, stateDir string) error {
	ui.StartOperation("Streaming OpenSearch into ClickHouse")
	engine.SetIndexProgress(func(index string, ds os2ch.Dataset, docs int) {
		ui.Info(fmt.Sprintf("streamed %s: %d docs", index, docs))
	})
	sres, err := engine.StreamAll(ctx, stateDir)
	if err != nil {
		ui.Warning("Stream state preserved at " + stateDir + " for re-run (committed indices will be skipped)")
		return fmt.Errorf("stream: %w", err)
	}

	for _, ds := range []os2ch.Dataset{os2ch.DatasetLogs, os2ch.DatasetAlerts, os2ch.DatasetStatistics} {
		vr := engine.VerifyDataset(ctx, ds, int64(sres.PerDataset[ds]), sres.SampleID(ds))
		switch {
		case vr.Error != "":
			ui.Warning(fmt.Sprintf("verify %s: %s", ds, vr.Error))
		case sres.PerDataset[ds] == 0:
		default:
			ui.Success(fmt.Sprintf("%s verified: %d docs in destination", ds, vr.CHDocs))
		}
	}
	os.RemoveAll(stateDir)
	ui.Success(fmt.Sprintf("Streamed %d documents into ClickHouse (%d skipped)", sres.Total, sres.Skipped))
	return nil
}

// StreamPhantom streams the upgrade's phantom OpenSearch (already running and
// exposing 9200) into the V12 ClickHouse. It only ensures destination ports
// and waits for both endpoints before the single shared stream core.
func (p *Phases) StreamPhantom(ctx context.Context, apiAddr, osPass string) error {
	if p.streamed {
		return nil
	}
	if p.cfg.DestExec == nil {
		return fmt.Errorf("no destination executor: --with-data requires SSH access to the destination")
	}
	chPass, err := p.composeEnv(p.cfg.DestExec, p.cfg.DestSSHUser, "clickhouse", []string{"CLICKHOUSE_PASSWORD"}, "ClickHouse password in destination compose")
	if err != nil {
		return err
	}
	var addedCH []string
	defer p.unpublish(p.cfg.DestExec, "utmstack_clickhouse", addedCH...)
	addedCH, err = ensurePublished(p.cfg.DestExec, "utmstack_clickhouse", []string{"8123:8123"})
	if err != nil {
		return fmt.Errorf("publish destination ClickHouse 8123: %w", err)
	}
	if len(addedCH) > 0 {
		ui.Success(fmt.Sprintf("Published destination ports: %v", addedCH))
	}
	osClient := os2ch.NewOSClient(os2ch.OSConfig{Host: apiAddr + ":9200", User: "admin", Password: osPass})
	if err := waitOSReady(ctx, osClient); err != nil {
		return fmt.Errorf("wait for phantom OpenSearch ready: %w", err)
	}
	if err := waitCHReady(ctx, p.cfg.DestHost+":8123"); err != nil {
		return fmt.Errorf("wait for destination ClickHouse ready: %w", err)
	}
	engine, err := os2ch.NewEngine(ctx, os2ch.EngineConfig{
		OS:  osClient.Config(),
		CH:  os2ch.CHConfig{HTTPAddr: p.cfg.DestHost + ":8123", User: "default", Password: chPass, Database: "utmstack"},
		Log: p.log,
	})
	if err != nil {
		return fmt.Errorf("os2ch engine: %w", err)
	}
	defer engine.Close()

	stateDir := filepath.Join(p.dumpDir, "stream")
	if err := p.streamCore(ctx, engine, stateDir); err != nil {
		return err
	}
	p.streamed = true
	return nil
}

func (p *Phases) RunDump(ctx context.Context) error {
	if p.dumped {
		return nil
	}
	osPass, err := p.composeEnv(p.cfg.SourceExec, p.cfg.SourceSSHUser, "node1", osPasswordVars, "OpenSearch admin password in source compose")
	if err != nil {
		return err
	}
	defer p.unpublish(p.cfg.SourceExec, "utmstack_node1", "9200:9200")
	if err := publishPorts(p.cfg.SourceExec, "utmstack_node1", []string{"9200:9200"}); err != nil {
		return fmt.Errorf("publish source OpenSearch 9200: %w", err)
	}
	ui.Success("Source OpenSearch port 9200 published")
	time.Sleep(10 * time.Second)

	engine, err := os2ch.NewEngine(ctx, os2ch.EngineConfig{
		OS:  os2ch.OSConfig{Host: p.cfg.SourceHost + ":9200", User: "admin", Password: osPass},
		Log: p.log,
	})
	if err != nil {
		return fmt.Errorf("os2ch engine: %w", err)
	}
	grouped, _, _ := engine.OSIndices(ctx)
	nIndices := len(grouped[os2ch.DatasetLogs]) + len(grouped[os2ch.DatasetAlerts]) + len(grouped[os2ch.DatasetStatistics])
	ui.StartOperation(fmt.Sprintf("Dumping %d OpenSearch index(es) to %s", nIndices, p.dumpDir))
	engine.SetIndexProgress(func(index string, ds os2ch.Dataset, docs int) {
		ui.Info(fmt.Sprintf("dumped %s: %d docs", index, docs))
	})
	dres, err := engine.Dump(ctx, p.dumpDir)
	if err != nil {
		ui.Warning("Dump preserved at " + p.dumpDir + " for re-run (already-dumped indices will be reused)")
		return fmt.Errorf("dump: %w", err)
	}
	p.dumped = true
	ui.Success(fmt.Sprintf("Dumped %d documents to %s", dres.Total, p.dumpDir))
	return nil
}

func (p *Phases) RunLoad(ctx context.Context) error {
	if p.loaded {
		return nil
	}
	if p.cfg.DestExec == nil {
		return fmt.Errorf("no destination executor: --with-data requires SSH access to the destination")
	}
	chPass, err := p.composeEnv(p.cfg.DestExec, p.cfg.DestSSHUser, "clickhouse", []string{"CLICKHOUSE_PASSWORD"}, "ClickHouse password in destination compose")
	if err != nil {
		return err
	}
	defer p.unpublish(p.cfg.DestExec, "utmstack_clickhouse", "9000:9000", "8123:8123")
	if err := publishPorts(p.cfg.DestExec, "utmstack_clickhouse", []string{"9000:9000", "8123:8123"}); err != nil {
		return fmt.Errorf("publish destination ClickHouse 9000/8123: %w", err)
	}
	ui.Success("Destination ClickHouse ports 9000/8123 published")
	time.Sleep(10 * time.Second)

	engine, err := os2ch.NewEngine(ctx, os2ch.EngineConfig{
		CH:  os2ch.CHConfig{Addr: p.cfg.DestHost + ":9000", User: "default", Password: chPass, Database: "utmstack"},
		Log: p.log,
	})
	if err != nil {
		return fmt.Errorf("os2ch engine: %w", err)
	}
	defer engine.Close()
	ui.StartOperation("Loading NDJSON into ClickHouse")
	engine.SetIndexProgress(func(index string, ds os2ch.Dataset, docs int) {
		ui.Info(fmt.Sprintf("loaded %s: %d docs", index, docs))
	})

	lres, err := engine.Load(ctx, p.dumpDir)
	if err != nil {
		ui.Warning("Dump preserved at " + p.dumpDir + " for re-run (already-loaded files will be skipped)")
		return fmt.Errorf("load: %w", err)
	}
	ui.Success(fmt.Sprintf("Loaded %d documents into ClickHouse (%d skipped)", lres.Total, lres.Skipped))

	for _, ds := range []os2ch.Dataset{os2ch.DatasetLogs, os2ch.DatasetAlerts, os2ch.DatasetStatistics} {
		vr := engine.VerifyDataset(ctx, ds, int64(lres.PerDataset[ds]), lres.SampleID(ds))
		switch {
		case vr.Error != "":
			ui.Warning(fmt.Sprintf("verify %s: %s", ds, vr.Error))
		case lres.PerDataset[ds] == 0:
		default:
			ui.Success(fmt.Sprintf("%s verified: %d docs in destination", ds, vr.CHDocs))
		}
	}

	p.loaded = true
	os.RemoveAll(p.dumpDir)
	return nil
}

// ResolveDumpDir keeps the dump next to the running binary so it is visible
// and survives /tmp cleanup; it falls back to the working directory when the
// binary's directory is not writable.
func ResolveDumpDir() (string, error) {
	const dirName = "opensearch_data"
	if execPath, err := os.Executable(); err == nil {
		binDir := filepath.Dir(execPath)
		if info, err := os.Stat(binDir); err == nil && info.IsDir() {
			test := filepath.Join(binDir, ".write-test")
			if err := os.WriteFile(test, []byte{}, 0o644); err == nil {
				os.Remove(test)
				return filepath.Join(binDir, dirName), nil
			}
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, dirName), nil
}

func (p *Phases) composeEnv(exec remote.CommandExecutor, user, service string, vars []string, what string) (string, error) {
	content, err := compose.ReadComposeBytes(exec, user)
	if err != nil {
		return "", err
	}
	for _, v := range vars {
		if val, e := compose.FindComposeEnvVar(content, service, v); e == nil && val != "" {
			return val, nil
		}
	}
	return "", fmt.Errorf("could not find %s (tried %s)", what, strings.Join(vars, ", "))
}

// publishPorts adds published ports to a swarm service (used by the dump/load
// path, which is being retired).
func publishPorts(exec remote.CommandExecutor, service string, ports []string) error {
	args := make([]string, 0, len(ports))
	for _, port := range ports {
		args = append(args, "--publish-add "+port)
	}
	if out, err := exec.ExecuteCommandWithSudo("docker service update " + strings.Join(args, " ") + " " + service); err != nil {
		return fmt.Errorf("%v (out: %s)", err, out)
	}
	return nil
}

// ensurePublished publishes ports on a swarm service, returning only the
// ports it actually added. Ports already exposed are left untouched so the
// service is not rotated needlessly.
func ensurePublished(exec remote.CommandExecutor, service string, ports []string) ([]string, error) {
	added := make([]string, 0, len(ports))
	args := make([]string, 0, len(ports))
	for _, port := range ports {
		if portAlreadyPublished(exec, service, port) {
			continue
		}
		args = append(args, "--publish-add "+port)
		added = append(added, port)
	}
	if len(args) == 0 {
		return nil, nil
	}
	if out, err := exec.ExecuteCommandWithSudo("docker service update " + strings.Join(args, " ") + " " + service); err != nil {
		return added, fmt.Errorf("%v (out: %s)", err, out)
	}
	return added, nil
}

// portAlreadyPublished checks whether a service already exposes the given
// published port, from its endpoint spec.
func portAlreadyPublished(exec remote.CommandExecutor, service string, port string) bool {
	want := strings.SplitN(port, ":", 2)[0]
	out, err := exec.ExecuteCommandWithSudo("docker service inspect " + service + " --format '{{range .Endpoint.Spec.Ports}} {{.PublishedPort}}{{end}}'")
	if err != nil {
		return false
	}
	for _, p := range strings.Fields(out) {
		if p == want {
			return true
		}
	}
	return false
}

const readyPollInterval = 2 * time.Second

// waitOSReady polls the source OpenSearch until it reports the tenant indices
// are safe to read, replacing the old blind sleep that raced cluster recovery
// on large instances.
func waitOSReady(ctx context.Context, client *os2ch.OSClient) error {
	ui.StartOperation("Waiting for source OpenSearch to be ready")
	deadline := time.Now().Add(15 * time.Minute)
	var lastErr error
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ok, err := client.Ready(ctx)
		if err != nil {
			lastErr = err
		} else if ok {
			ui.Success("Source OpenSearch ready")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("source OpenSearch not ready after 15m (last error: %v)", lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyPollInterval):
		}
	}
}

// waitCHReady polls the destination ClickHouse HTTP endpoint (host:port) until
// it answers ping, so a just-rotated service cannot be hit before it accepts
// queries.
func waitCHReady(ctx context.Context, addr string) error {
	ui.StartOperation("Waiting for destination ClickHouse to be ready")
	deadline := time.Now().Add(10 * time.Minute)
	var lastErr error
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := pingCH(ctx, addr); err != nil {
			lastErr = err
		} else {
			ui.Success("Destination ClickHouse ready")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("destination ClickHouse not ready after 10m (last error: %v)", lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(readyPollInterval):
		}
	}
}

func pingCH(ctx context.Context, addr string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ping", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Ok.") {
		return fmt.Errorf("ping: %s", resp.Status)
	}
	return nil
}

func (p *Phases) unpublish(exec remote.CommandExecutor, service string, ports ...string) {
	if len(ports) == 0 {
		return
	}
	args := make([]string, 0, len(ports))
	for _, port := range ports {
		args = append(args, "--publish-rm "+port)
	}
	if out, err := exec.ExecuteCommandWithSudo("docker service update " + strings.Join(args, " ") + " " + service); err != nil {
		ui.Warning(fmt.Sprintf("could not unpublish %s: %v (out: %s)", service, err, out))
	}
}
