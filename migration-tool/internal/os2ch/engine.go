package os2ch

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

const (
	defaultBatch = 5000
	manifestName = "stream.yaml"
	dumpWorkers  = 8
	// streamAttempts bounds how many times a single index is re-streamed from
	// scratch when its scroll dies mid-way (context expired, 5xx that escaped
	// the client retry). Safe because the insert is atomic: a dead scroll
	// commits no rows, so a restart can never duplicate data.
	streamAttempts = 3
)

// IndexProgress reports that one index finished streaming, so the
// caller can keep the user informed during long transfers.
type IndexProgress func(index string, ds Dataset, docs int)

// StreamIndex reports per-index progress and totals for one dataset.
type StreamIndex struct {
	Index    string
	Dataset  Dataset
	Mapped   int
	SampleID string
}

// Engine copies V11 OpenSearch data into V12 ClickHouse by streaming each
// index straight into a single atomic insert. A manifest in stateDir makes
// the run resumable.
type Engine struct {
	os      *OSClient
	ch      CHClient
	batch   int
	log     *slog.Logger
	onIndex IndexProgress
}

// SetIndexProgress attaches a callback invoked once per finished index,
// so long transfers stay visible to the operator.
func (e *Engine) SetIndexProgress(fn IndexProgress) {
	e.onIndex = fn
}

// EngineConfig holds connection settings for both ends.
type EngineConfig struct {
	OS    OSConfig
	CH    CHConfig
	Batch int
	Log   *slog.Logger
}

func NewEngine(ctx context.Context, cfg EngineConfig) (*Engine, error) {
	batch := cfg.Batch
	if batch <= 0 {
		batch = defaultBatch
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{
		os:    NewOSClient(cfg.OS),
		batch: batch,
		log:   log,
	}
	if cfg.CH.HTTPAddr != "" {
		ch, err := NewCHWriter(ctx, cfg.CH, log)
		if err != nil {
			return nil, err
		}
		e.ch = ch
	}
	return e, nil
}

// Close releases the ClickHouse connection.
func (e *Engine) Close() error {
	if e.ch == nil {
		return nil
	}
	return e.ch.Close()
}

// OSIndices lists all in-scope V11 indices grouped by destination dataset,
// plus the out-of-scope ones, without moving any data.
func (e *Engine) OSIndices(ctx context.Context) (map[Dataset][]string, []string, error) {
	indices, err := e.os.ListIndices(ctx)
	if err != nil {
		return nil, nil, err
	}
	grouped := map[Dataset][]string{}
	var skipped []string
	for _, name := range indices {
		ds, ok := ClassifyIndex(name)
		if !ok {
			skipped = append(skipped, name)
			continue
		}
		grouped[ds] = append(grouped[ds], name)
	}
	return grouped, skipped, nil
}

type LoadResult struct {
	Skipped    int
	Total      int
	PerIndex   []StreamIndex
	PerDataset map[Dataset]int
	SampleIDs  map[Dataset]string
}

// SampleID returns the first recorded document id for a dataset, used by
// post-load verification.
func (r *LoadResult) SampleID(ds Dataset) string {
	if r.SampleIDs == nil {
		return ""
	}
	return r.SampleIDs[ds]
}

type job struct {
	ds  Dataset
	idx string
}

// streamOutcome carries one worker result back to the collector.
type streamOutcome struct {
	si  StreamIndex
	err error
}

// StreamAll copies every in-scope index straight into ClickHouse with no
// intermediate dump: each index is one parallel worker that scrolls the
// source and pipes mapped JSONEachRow rows into a single atomic InsertFormat
// statement. A manifest in stateDir (when non-empty) makes the run resumable:
// already-streamed indices are skipped on retry, which matters because a
// failed InsertTable leaves zero rows (atomic) but a completed one is committed.
func (e *Engine) StreamAll(ctx context.Context, stateDir string) (*LoadResult, error) {
	if e.ch == nil {
		return nil, fmt.Errorf("engine has no ClickHouse connection for StreamAll")
	}
	var done map[string]bool
	var manifestPath string
	if stateDir != "" {
		manifestPath = filepath.Join(stateDir, manifestName)
		done = e.readStreamManifest(manifestPath)
	}
	grouped, _, err := e.OSIndices(ctx)
	if err != nil {
		return nil, err
	}
	res := &LoadResult{PerDataset: map[Dataset]int{}, SampleIDs: map[Dataset]string{}}

	var jobs []job
	for _, ds := range []Dataset{DatasetLogs, DatasetAlerts, DatasetStatistics} {
		sort.Strings(grouped[ds])
		for _, idx := range grouped[ds] {
			if done != nil && done[idx] {
				res.Skipped++
				continue
			}
			jobs = append(jobs, job{ds: ds, idx: idx})
		}
	}
	workers := dumpWorkers
	if len(jobs) < workers {
		workers = len(jobs)
	}
	if workers > 0 {
		in := make(chan job)
		outc := make(chan streamOutcome, len(jobs))
		for i := 0; i < workers; i++ {
			go func() {
				for j := range in {
					si, err := e.streamIndexWithRetry(ctx, j.ds, j.idx)
					outc <- streamOutcome{si: si, err: err}
				}
			}()
		}
		go func() {
			for _, j := range jobs {
				in <- j
			}
			close(in)
		}()
		for range jobs {
			o := <-outc
			if o.err != nil {
				return nil, o.err
			}
			if o.si.Mapped == 0 {
				continue
			}
			res.PerIndex = append(res.PerIndex, o.si)
			res.PerDataset[o.si.Dataset] += o.si.Mapped
			res.Total += o.si.Mapped
			if res.SampleIDs[o.si.Dataset] == "" {
				res.SampleIDs[o.si.Dataset] = o.si.SampleID
			}
			if done != nil {
				done[o.si.Index] = true
				e.writeStreamManifest(manifestPath, done)
			}
			if e.onIndex != nil {
				e.onIndex(o.si.Index, o.si.Dataset, o.si.Mapped)
			}
		}
	}
	return res, nil
}

// streamIndexWithRetry re-streams an index from the start whenever the scroll
// dies mid-way, so a single flaky index cannot abort the whole migration.
func (e *Engine) streamIndexWithRetry(ctx context.Context, ds Dataset, index string) (StreamIndex, error) {
	var last StreamIndex
	var lastErr error
	for attempt := 1; ; attempt++ {
		last, lastErr = e.streamOne(ctx, ds, index)
		if lastErr == nil || attempt >= streamAttempts {
			return last, lastErr
		}
	}
}

// streamOne maps and streams a single index: the scroll's pull-based onPage
// pushes JSONEachRow rows through an io.Pipe into one InsertFormat statement.
func (e *Engine) streamOne(ctx context.Context, ds Dataset, index string) (StreamIndex, error) {
	var si StreamIndex
	si.Index, si.Dataset = index, ds
	table := TableFor(ds)

	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- e.ch.InsertTable(ctx, table, pr)
	}()

	enc := bufio.NewWriter(pw)
	_, scrollErr := e.os.Scroll(ctx, index, e.batch, func(id string, doc map[string]interface{}) error {
		doc = MapDocument(ds, doc)
		if si.SampleID == "" {
			si.SampleID = id
		}
		line, err := MarshalRow(doc)
		if err != nil {
			return err
		}
		if _, err := enc.WriteString(line + "\n"); err != nil {
			return err
		}
		if err := enc.Flush(); err != nil {
			return err
		}
		si.Mapped++
		return nil
	})
	if scrollErr != nil {
		pw.CloseWithError(scrollErr)
		return si, scrollErr
	}
	if err := enc.Flush(); err != nil {
		pw.CloseWithError(err)
		return si, err
	}
	pw.Close()

	if err := <-errCh; err != nil {
		return si, fmt.Errorf("stream %s -> %s: %w", index, table, err)
	}
	return si, nil
}

type streamState struct {
	Done map[string]bool `yaml:"done"`
}

func (e *Engine) readStreamManifest(path string) map[string]bool {
	s := &streamState{Done: map[string]bool{}}
	if in, err := os.ReadFile(path); err == nil {
		_ = yaml.Unmarshal(in, s)
	}
	if s.Done == nil {
		s.Done = map[string]bool{}
	}
	return s.Done
}

func (e *Engine) writeStreamManifest(path string, done map[string]bool) {
	if out, err := yaml.Marshal(streamState{Done: done}); err == nil {
		_ = os.WriteFile(path, out, 0o644)
	}
}

// VerifyDataset counts rows in the destination table and checks that a sample
// id made it across, for one dataset.
func (e *Engine) VerifyDataset(ctx context.Context, ds Dataset, osCount int64, sampleID string) VerifyResult {
	if e.ch == nil {
		return VerifyResult{Dataset: ds, Error: "engine has no ClickHouse connection"}
	}
	return VerifyOne(ctx, e.ch, ds, osCount, sampleID)
}
