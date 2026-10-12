package searchdata

import (
	"context"
	"fmt"
	"log/slog"
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
	cfg     Config
	dumpDir string
	log     *slog.Logger
	dumped  bool
	loaded  bool
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

// SetDestExec attaches the destination executor after the dump phase, because
// in migrate the destination SSH is only established once the dump is done.
func (p *Phases) SetDestExec(exec remote.CommandExecutor) {
	if p.cfg.DestExec == nil && exec != nil {
		p.cfg.DestExec = exec
	}
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

	if s := engine.OrphansSummary(); s != "" {
		ui.Info("Orphan field report (V11 fields with no V12 column):")
		for _, line := range strings.Split(s, "\n") {
			ui.Info("  " + line)
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

func (p *Phases) unpublish(exec remote.CommandExecutor, service string, ports ...string) {
	args := make([]string, 0, len(ports))
	for _, port := range ports {
		args = append(args, "--publish-rm "+port)
	}
	if out, err := exec.ExecuteCommandWithSudo("docker service update " + strings.Join(args, " ") + " " + service); err != nil {
		ui.Warning(fmt.Sprintf("could not unpublish %s: %v (out: %s)", service, err, out))
	}
}
