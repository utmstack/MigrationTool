package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/os2ch"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/upgrade"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

// OpenSearch credentials live under different keys depending on the V11
// minor version; try them in order.
var osPasswordVars = []string{"OPENSEARCH_INITIAL_ADMIN_PASSWORD", "ELASTICSEARCH_PASSWORD"}

// resolveSearchDataDumpDir puts the NDJSON dump in an opensearch_data folder
// next to the running binary, so it is visible and survives a /tmp cleanup.
// If the binary's directory is not writable it falls back to the current
// working directory.
func resolveSearchDataDumpDir() (string, error) {
	const dirName = "opensearch_data"
	execPath, err := os.Executable()
	if err == nil {
		binDir := filepath.Dir(execPath)
		candidate := filepath.Join(binDir, dirName)
		if info, statErr := os.Stat(binDir); statErr == nil && info.IsDir() {
			test := filepath.Join(binDir, ".write-test")
			if err := os.WriteFile(test, []byte{}, 0o644); err == nil {
				os.Remove(test)
				return candidate, nil
			}
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, dirName), nil
}

// runMigrateSearchData streams V11 OpenSearch tenant data into the V12
// ClickHouse. Both stacks must be alive, so it runs after the Postgres import.
func runMigrateSearchData(cfg *domain.MigrationConfig, sourceSSH, destSSH *remote.SSHClient) error {
	dumpDir, err := resolveSearchDataDumpDir()
	if err != nil {
		return fmt.Errorf("resolve dump dir: %w", err)
	}
	if sourceSSH == nil || destSSH == nil {
		return fmt.Errorf("--with-data requires SSH access to both source and destination")
	}

	ui.StartOperation("Migrating search data (OpenSearch -> ClickHouse)")

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Read the live compose files for both stacks' credentials.
	srcCompose, err := upgrade.ReadComposeBytes(sourceSSH, cfg.Source.SSHUser)
	if err != nil {
		return fmt.Errorf("source compose: %w", err)
	}
	dstCompose, err := upgrade.ReadComposeBytes(destSSH, cfg.Destination.SSHUser)
	if err != nil {
		return fmt.Errorf("destination compose: %w", err)
	}

	osPass := ""
	for _, v := range osPasswordVars {
		if osPass, err = upgrade.FindComposeEnvVar(srcCompose, "node1", v); err == nil && osPass != "" {
			break
		}
	}
	if osPass == "" {
		return fmt.Errorf("could not find OpenSearch admin password in source compose (tried %s)", strings.Join(osPasswordVars, ", "))
	}
	chPass, err := upgrade.FindComposeEnvVar(dstCompose, "clickhouse", "CLICKHOUSE_PASSWORD")
	if err != nil || chPass == "" {
		return fmt.Errorf("could not find ClickHouse password in destination compose: %v", err)
	}

	// Publish the ports the engine needs, keeping them open for the whole run.
	opened := 0
	defer func() {
		if opened == 0 {
			return
		}
		if _, e := sourceSSH.ExecuteCommandWithSudo("docker service update --publish-rm 9200:9200 utmstack_node1"); e != nil {
			ui.Warning(fmt.Sprintf("could not unpublish source 9200: %v", e))
		}
		if _, e := destSSH.ExecuteCommandWithSudo("docker service update --publish-rm 9000:9000 --publish-rm 8123:8123 utmstack_clickhouse"); e != nil {
			ui.Warning(fmt.Sprintf("could not unpublish destination 9000/8123: %v", e))
		}
	}()

	if out, e := sourceSSH.ExecuteCommandWithSudo("docker service update --publish-add 9200:9200 utmstack_node1"); e != nil {
		return fmt.Errorf("publish source OpenSearch 9200: %v (out: %s)", e, out)
	}
	opened++
	ui.Success("Source OpenSearch port 9200 published")

	if out, e := destSSH.ExecuteCommandWithSudo("docker service update --publish-add 9000:9000 --publish-add 8123:8123 utmstack_clickhouse"); e != nil {
		return fmt.Errorf("publish destination ClickHouse 9000/8123: %v (out: %s)", e, out)
	}
	opened++
	ui.Success("Destination ClickHouse ports 9000/8123 published")
	time.Sleep(10 * time.Second)

	engine, err := os2ch.NewEngine(ctx, os2ch.EngineConfig{
		OS: os2ch.OSConfig{
			Host:     cfg.Source.Host + ":9200",
			User:     "admin",
			Password: osPass,
		},
		CH: os2ch.CHConfig{
			Addr:     cfg.Destination.Host + ":9000",
			User:     "default",
			Password: chPass,
			Database: "utmstack",
		},
		Log: log,
	})
	if err != nil {
		return fmt.Errorf("os2ch engine: %w", err)
	}
	defer engine.Close()
	// On success the dump is cleaned up; on failure it is preserved so a
	// re-run reuses the NDJSON files instead of reading OpenSearch again.
	var keepDumpDir bool
	defer func() {
		if keepDumpDir {
			ui.Warning("Dump preserved at " + dumpDir + " for re-run (already-dumped indices will be reused)")
		} else {
			os.RemoveAll(dumpDir)
		}
	}()

	ui.Info("Dumping OpenSearch indices to local NDJSON...")
	dres, err := engine.Dump(ctx, dumpDir)
	if err != nil {
		keepDumpDir = true
		return fmt.Errorf("dump: %w", err)
	}
	ui.Success(fmt.Sprintf("Dumped %d documents to %s", dres.Total, dumpDir))

	ui.Info("Loading NDJSON into ClickHouse (one bulk insert per index)...")
	lres, err := engine.Load(ctx, dumpDir)
	if err != nil {
		keepDumpDir = true
		return fmt.Errorf("load: %w", err)
	}
	ui.Success(fmt.Sprintf("Loaded %d documents into ClickHouse (%d skipped)", lres.Total, lres.Skipped))

	// Per-dataset verification against the destination.
	for _, ds := range []os2ch.Dataset{os2ch.DatasetLogs, os2ch.DatasetAlerts, os2ch.DatasetStatistics} {
		vr := engine.VerifyDataset(ctx, ds, int64(dres.PerDataset[ds]), lres.SampleID(ds))
		switch {
		case vr.Error != "":
			ui.Warning(fmt.Sprintf("verify %s: %s", ds, vr.Error))
		case dres.PerDataset[ds] == 0:
		default:
			ui.Success(fmt.Sprintf("%s verified: %d docs in destination", ds, vr.CHDocs))
		}
	}

	if s := lres.PerIndexSummary(); s != "" {
		ui.Info("Loaded per index:")
		for _, line := range strings.Split(s, "\n") {
			ui.Info("  " + line)
		}
	}
	if s := engine.OrphansSummary(); s != "" {
		ui.Info("Orphan field report (V11 fields with no V12 column):")
		for _, line := range strings.Split(s, "\n") {
			ui.Info("  " + line)
		}
	}
	return nil
}
