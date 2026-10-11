package os2ch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Engine wires the OpenSearch reader and ClickHouse writer into the
// high-level operations the migrate and upgrade commands need.
type Engine struct {
	os    *OSClient
	ch    *CHWriter
	log   *slog.Logger
	batch int
}

type EngineConfig struct {
	OS       OSConfig
	CH       CHConfig
	MaxBatch int
	Log      *slog.Logger
}

func NewEngine(ctx context.Context, cfg EngineConfig) (*Engine, error) {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	batch := cfg.MaxBatch
	if batch <= 0 {
		batch = 500
	}
	e := &Engine{
		os:    NewOSClient(cfg.OS),
		log:   cfg.Log,
		batch: batch,
	}
	if cfg.CH.Addr != "" {
		ch, err := NewCHWriter(ctx, cfg.CH, cfg.Log)
		if err != nil {
			return nil, err
		}
		e.ch = ch
	}
	return e, nil
}

func (e *Engine) Close() error {
	if e.ch == nil {
		return nil
	}
	return e.ch.Close()
}

// OSIndices lists the in-scope V11 indices on the source, grouped by dataset.
// The second return lists every V11 index that is out of scope (skipped).
func (e *Engine) OSIndices(ctx context.Context) (map[Dataset][]string, []string, error) {
	names, err := e.os.ListIndices(ctx)
	if err != nil {
		return nil, nil, err
	}
	in := map[Dataset][]string{}
	var skipped []string
	for _, n := range names {
		ds, ok := ClassifyIndex(n)
		if !ok {
			skipped = append(skipped, n)
			continue
		}
		in[ds] = append(in[ds], n)
	}
	for ds := range in {
		sort.Strings(in[ds])
	}
	sort.Strings(skipped)
	return in, skipped, nil
}

// StreamResult is the outcome of a full Stream run.
type StreamResult struct {
	TotalRead    int
	TotalWritten int
	PerDataset   map[Dataset]int
	PerIndex     map[string]int
	Skipped      []string
	Orphans      *OrphanReport
	Duration     time.Duration
}

// Stream copies every in-scope index from OpenSearch to ClickHouse in one
// pass. It is the migrate --with-search-data operation.
func (e *Engine) Stream(ctx context.Context) (*StreamResult, error) {
	if e.ch == nil {
		return nil, fmt.Errorf("stream: no ClickHouse writer configured")
	}
	start := time.Now()
	in, skipped, err := e.OSIndices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list indices: %w", err)
	}
	res := &StreamResult{
		PerDataset: map[Dataset]int{},
		PerIndex:   map[string]int{},
		Skipped:    skipped,
		Orphans:    NewOrphanReport(),
	}

	for _, ds := range []Dataset{DatasetLogs, DatasetAlerts, DatasetStatistics} {
		for _, index := range in[ds] {
			e.log.Info("streaming index", "index", index, "dataset", string(ds))
			n, err := e.streamIndex(ctx, ds, index, res.Orphans)
			if err != nil {
				return nil, fmt.Errorf("stream %s: %w", index, err)
			}
			res.PerIndex[index] = n
			res.PerDataset[ds] += n
			res.TotalRead += n
			res.TotalWritten += n
		}
	}
	res.Duration = time.Since(start)
	return res, nil
}

// streamIndex scrolls one index, maps + reports orphans per doc, writes
// batches to ClickHouse.
func (e *Engine) streamIndex(ctx context.Context, ds Dataset, index string, orphans *OrphanReport) (int, error) {
	cols, err := e.ch.DescribeColumns(ctx, ds)
	if err != nil {
		return 0, fmt.Errorf("describe %s: %w", TableFor(ds), err)
	}
	var (
		total int
		batch []map[string]interface{}
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := e.ch.Insert(ctx, ds, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	_, err = e.os.Scroll(ctx, index, e.batch, func(id string, doc map[string]interface{}) error {
		mapped := MapDocument(ds, doc)
		orphans.Note(ds, mapped, cols)
		total++
		batch = append(batch, mapped)
		if len(batch) >= e.batch {
			return flush()
		}
		return nil
	})
	if err != nil {
		return total, err
	}
	return total, flush()
}

// DumpResult records a per-dataset dump for the upgrade flow.
type DumpResult struct {
	Dir        string
	PerDataset map[Dataset]int
	PerIndex   map[string]int
	Skipped    []string
	Orphans    *OrphanReport
}

type dumpManifestRecord struct {
	Dataset Dataset `yaml:"dataset"`
	Index   string  `yaml:"index"`
	File    string  `yaml:"file"`
	Docs    int     `yaml:"docs"`
	Loaded  bool    `yaml:"loaded"`
}

type dumpManifest struct {
	Version   int                  `yaml:"version"`
	CreatedAt string               `yaml:"created_at"`
	Records   []dumpManifestRecord `yaml:"records"`
}

// Dump writes every in-scope index to NDJSON files under dir plus a
// manifest.yaml. It is the upgrade --with-search-data dump phase.
func (e *Engine) Dump(ctx context.Context, dir string) (*DumpResult, error) {
	in, skipped, err := e.OSIndices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list indices: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	res := &DumpResult{
		Dir:        dir,
		PerDataset: map[Dataset]int{},
		PerIndex:   map[string]int{},
		Skipped:    skipped,
		Orphans:    NewOrphanReport(),
	}
	manifest := dumpManifest{
		Version:   1,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	for _, ds := range []Dataset{DatasetLogs, DatasetAlerts, DatasetStatistics} {
		for _, index := range in[ds] {
			file := filepath.Join(dir, sanitizeFilename(index)+".ndjson")
			e.log.Info("dumping index", "index", index, "dataset", string(ds))
			n, err := e.dumpIndex(ctx, ds, index, file)
			if err != nil {
				return nil, fmt.Errorf("dump %s: %w", index, err)
			}
			res.PerIndex[index] = n
			res.PerDataset[ds] += n
			manifest.Records = append(manifest.Records, dumpManifestRecord{
				Dataset: ds,
				Index:   index,
				File:    filepath.Base(file),
				Docs:    n,
			})
		}
	}

	body, err := yaml.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), body, 0o644); err != nil {
		return nil, err
	}
	return res, nil
}

// dumpIndex scrolls one index and writes raw docs to NDJSON.
func (e *Engine) dumpIndex(ctx context.Context, ds Dataset, index, file string) (int, error) {
	f, err := os.Create(file)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	total := 0
	_, err = e.os.Scroll(ctx, index, e.batch, func(id string, doc map[string]interface{}) error {
		line, err := MarshalRow(doc)
		if err != nil {
			return err
		}
		if _, err := f.WriteString(line + "\n"); err != nil {
			return err
		}
		total++
		return nil
	})
	if err != nil {
		return total, err
	}
	return total, f.Sync()
}

// Load reads a dumped directory and writes its NDJSON files into
// ClickHouse. It is the upgrade --with-search-data load phase.
func (e *Engine) Load(ctx context.Context, dir string) (*StreamResult, error) {
	if e.ch == nil {
		return nil, fmt.Errorf("load: no ClickHouse writer configured")
	}
	manifestPath := filepath.Join(dir, "manifest.yaml")
	m, err := readManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	res := &StreamResult{
		PerDataset: map[Dataset]int{},
		PerIndex:   map[string]int{},
		Orphans:    NewOrphanReport(),
	}
	for _, rec := range m.Records {
		if rec.Loaded {
			e.log.Info("skipping already-loaded index", "index", rec.Index)
			continue
		}
		file := filepath.Join(dir, rec.File)
		n, err := e.loadIndex(ctx, rec.Dataset, rec.Index, file, res.Orphans)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", rec.Index, err)
		}
		res.PerIndex[rec.Index] = n
		res.PerDataset[rec.Dataset] += n
		res.TotalRead += n
		res.TotalWritten += n
		// Mark loaded and persist the manifest so a re-run skips it.
		for i := range m.Records {
			if m.Records[i].Index == rec.Index {
				m.Records[i].Loaded = true
			}
		}
		if err := writeManifest(manifestPath, m); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// loadIndex reads one NDJSON file, maps each doc, and writes to ClickHouse.
func (e *Engine) loadIndex(ctx context.Context, ds Dataset, index, file string, orphans *OrphanReport) (int, error) {
	cols, err := e.ch.DescribeColumns(ctx, ds)
	if err != nil {
		return 0, err
	}
	f, err := os.Open(file)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var (
		total int
		batch []map[string]interface{}
	)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := e.ch.Insert(ctx, ds, batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		var doc map[string]interface{}
		if err := json.Unmarshal([]byte(line), &doc); err != nil {
			return total, fmt.Errorf("bad ndjson line in %s: %w", file, err)
		}
		mapped := MapDocument(ds, doc)
		orphans.Note(ds, mapped, cols)
		total++
		batch = append(batch, mapped)
		if len(batch) >= e.batch {
			if err := flush(); err != nil {
				return total, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return total, err
	}
	return total, flush()
}

func sanitizeFilename(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, s)
}

func readManifest(path string) (*dumpManifest, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m dumpManifest
	if err := yaml.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func writeManifest(path string, m *dumpManifest) error {
	body, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}
