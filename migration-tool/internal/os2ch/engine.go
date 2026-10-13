package os2ch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultBatch    = 5000
	manifestName    = "manifest.yaml"
	dumpWorkers     = 8
	estimatePerPage = 1000
)

// IndexProgress reports that one index finished dumping or loading, so the
// caller can keep the user informed during long transfers.
type IndexProgress func(index string, ds Dataset, docs int)

// StreamIndex reports per-index progress and totals for one dataset.
type StreamIndex struct {
	Index    string
	Dataset  Dataset
	Mapped   int
	SampleID string
}

// Engine copies V11 OpenSearch data into V12 ClickHouse. StreamAll pipes each
// index straight into a single atomic insert (migrate, both endpoints live);
// Dump and Load move it via NDJSON on disk (upgrade, where the source dies
// before the destination ClickHouse exists). All three are resumable via a
// manifest.
type Engine struct {
	os      *OSClient
	ch      CHClient
	batch   int
	log     *slog.Logger
	onIndex IndexProgress
}

// SetIndexProgress attaches a callback invoked once per finished index, in
// both Dump and Load, so long transfers stay visible to the operator.
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
	if cfg.CH.Addr != "" || cfg.CH.HTTPAddr != "" {
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

// Estimate samples every in-scope index (first page of the scroll) and
// extrapolates the dump size from the sampled bytes, so the tool can refuse
// to start when the destination disk cannot hold the NDJSON files.
type EstimateResult struct {
	PerIndex   []StreamIndex
	PerDataset map[Dataset]int64
	TotalDocs  int64
	RawBytes   int64
	Errs       []string
}

func (e *Engine) Estimate(ctx context.Context) (*EstimateResult, error) {
	grouped, _, err := e.OSIndices(ctx)
	if err != nil {
		return nil, err
	}
	res := &EstimateResult{PerDataset: map[Dataset]int64{}}
	for _, ds := range []Dataset{DatasetLogs, DatasetAlerts, DatasetStatistics} {
		for _, idx := range grouped[ds] {
			var (
				docs   int
				bytes  int
				count  int64
				sample string
			)
			page, scrollErr := e.os.SampleFirst(ctx, idx, estimatePerPage, func(id string, doc map[string]interface{}) error {
				b, err := MarshalRow(doc)
				if err != nil {
					return err
				}
				docs++
				bytes += len(b) + 1
				if sample == "" {
					sample = id
				}
				return nil
			})
			if scrollErr != nil {
				res.Errs = append(res.Errs, fmt.Sprintf("%s: %v", idx, scrollErr))
				continue
			}
			if count, err = e.os.CountDocs(ctx, idx); err != nil {
				res.Errs = append(res.Errs, fmt.Sprintf("%s: count: %v", idx, err))
				continue
			}
			estDocs := count
			estBytes := int64(bytes)
			if page > 0 && count > int64(page) {
				estBytes = int64(float64(bytes) / float64(page) * float64(count))
			}
			if estDocs < int64(docs) {
				estDocs = int64(docs)
			}
			res.PerIndex = append(res.PerIndex, StreamIndex{
				Index: idx, Dataset: ds, Mapped: int(estDocs), SampleID: sample,
			})
			res.PerDataset[ds] += estDocs
			res.TotalDocs += estDocs
			res.RawBytes += estBytes
		}
	}
	return res, nil
}

// FreeDiskSpace reports the free bytes on the filesystem holding path. When
// path does not exist yet (the dump dir is created after the check), it walks
// up to the nearest existing ancestor so the estimate is still valid.
func FreeDiskSpace(path string) (int64, error) {
	cur := path
	for {
		var st syscall.Statfs_t
		if err := syscall.Statfs(cur, &st); err == nil {
			return int64(st.Bavail) * int64(st.Bsize), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return 0, fmt.Errorf("cannot determine filesystem for %s", path)
		}
		cur = parent
	}
}

// HumanSize renders bytes as a human-readable string for pre-flight messages.
func HumanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return strconv.FormatInt(b, 10) + " B"
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// DumpResult reports what Dump wrote.
type DumpResult struct {
	Dir        string
	PerDataset map[Dataset]int
	Indexes    []StreamIndex
	Skipped    []string
	Total      int
}

type job struct {
	ds   Dataset
	idx  string
	file string
}

// dumpOutcome carries one worker result back to the collector.
type dumpOutcome struct {
	ds     Dataset
	idx    string
	file   string
	count  int
	sample string
	err    error
}

// Dump writes every in-scope index to NDJSON files under dir plus a
// manifest.yaml. Raw V11 documents are preserved; mapping happens at Load.
// Indices already dumped (manifest record with an existing file) are reused,
// and the remaining indices are read by a small worker pool.
func (e *Engine) Dump(ctx context.Context, dir string) (*DumpResult, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	grouped, skipped, err := e.OSIndices(ctx)
	if err != nil {
		return nil, err
	}
	res := &DumpResult{Dir: dir, PerDataset: map[Dataset]int{}, Skipped: skipped}

	var manifest *dumpManifest
	if m, err := readManifest(filepath.Join(dir, manifestName)); err == nil {
		manifest = m
	} else {
		manifest = &dumpManifest{Version: 1, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	dumped := map[string]bool{}
	for _, rec := range manifest.Records {
		dumped[rec.Index] = true
	}

	var jobs []job
	for _, ds := range []Dataset{DatasetLogs, DatasetAlerts, DatasetStatistics} {
		sort.Strings(grouped[ds])
		for _, idx := range grouped[ds] {
			file := filepath.Join(dir, sanitizeFilename(idx)+".ndjson")
			if dumped[idx] {
				if _, err := os.Stat(file); err == nil {
					for _, rec := range manifest.Records {
						if rec.Index == idx {
							res.PerDataset[ds] += rec.Docs
							res.Total += rec.Docs
						}
					}
					continue
				}
			}
			jobs = append(jobs, job{ds: ds, idx: idx, file: file})
		}
	}

	workers := dumpWorkers
	if len(jobs) < workers {
		workers = len(jobs)
	}
	if workers > 0 {
		in := make(chan job)
		outc := make(chan dumpOutcome, len(jobs))
		for i := 0; i < workers; i++ {
			go func() {
				for j := range in {
					outc <- dumpOne(ctx, e.os, e.batch, j)
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
			res.Indexes = append(res.Indexes, StreamIndex{
				Index: o.idx, Dataset: o.ds, Mapped: o.count, SampleID: o.sample,
			})
			res.PerDataset[o.ds] += o.count
			res.Total += o.count
			manifest.Records = append(manifest.Records, dumpRecord{
				Dataset: o.ds,
				Index:   o.idx,
				File:    filepath.Base(o.file),
				Docs:    o.count,
			})
			if e.onIndex != nil {
				e.onIndex(o.idx, o.ds, o.count)
			}
		}
	}

	if err := writeManifest(filepath.Join(dir, manifestName), manifest); err != nil {
		return nil, err
	}
	sort.Slice(res.Indexes, func(i, j int) bool { return res.Indexes[i].Index < res.Indexes[j].Index })
	return res, nil
}

// dumpOne reads a single index to an NDJSON file.
func dumpOne(ctx context.Context, client *OSClient, batch int, j job) dumpOutcome {
	var o dumpOutcome
	o.ds, o.idx, o.file = j.ds, j.idx, j.file
	f, err := os.Create(j.file)
	if err != nil {
		o.err = err
		return o
	}
	w := bufio.NewWriterSize(f, 1<<20)
	count := 0
	_, scrollErr := client.Scroll(ctx, j.idx, batch, func(id string, doc map[string]interface{}) error {
		b, err := MarshalRow(doc)
		if err != nil {
			return err
		}
		if _, err := w.WriteString(b + "\n"); err != nil {
			return err
		}
		count++
		if o.sample == "" {
			o.sample = id
		}
		return nil
	})
	if scrollErr != nil {
		f.Close()
		o.err = scrollErr
		return o
	}
	if err := w.Flush(); err != nil {
		f.Close()
		o.err = err
		return o
	}
	if err := f.Close(); err != nil {
		o.err = err
		return o
	}
	o.count = count
	return o
}

// LoadResult reports what Load inserted.
type LoadResult struct {
	Loaded     int
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

// PerIndexSummary renders one line per loaded index.
func (r *LoadResult) PerIndexSummary() string {
	if len(r.PerIndex) == 0 {
		return ""
	}
	sorted := make([]StreamIndex, len(r.PerIndex))
	copy(sorted, r.PerIndex)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Index < sorted[j].Index })
	empties := 0
	var b strings.Builder
	for _, si := range sorted {
		if si.Mapped == 0 {
			empties++
			continue
		}
		fmt.Fprintf(&b, "%s -> %s: %d docs", si.Index, si.Dataset, si.Mapped)
		b.WriteByte('\n')
	}
	if empties > 0 {
		fmt.Fprintf(&b, "%d index(es) empty (0 docs)", empties)
	}
	return b.String()
}

type dumpManifest struct {
	Version   int          `yaml:"version"`
	CreatedAt string       `yaml:"created_at"`
	Records   []dumpRecord `yaml:"records"`
}

type dumpRecord struct {
	Dataset Dataset `yaml:"dataset"`
	Index   string  `yaml:"index"`
	File    string  `yaml:"file"`
	Docs    int     `yaml:"docs"`
	Loaded  bool    `yaml:"loaded"`
}

// Load reads the dump produced by Dump and inserts the not-yet-loaded records
// into ClickHouse. Each file becomes one bulk insert over a pipe, so the
// mapped rows stream through without being buffered in memory. It is
// idempotent: indices already marked loaded in the manifest are skipped.
func (e *Engine) Load(ctx context.Context, dir string) (*LoadResult, error) {
	manifestPath := filepath.Join(dir, manifestName)
	manifest, err := readManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	if e.ch == nil {
		return nil, fmt.Errorf("engine has no ClickHouse connection for Load")
	}
	res := &LoadResult{PerDataset: map[Dataset]int{}, SampleIDs: map[Dataset]string{}}

	for i := range manifest.Records {
		rec := &manifest.Records[i]
		if rec.Loaded {
			res.Skipped++
			continue
		}
		path := filepath.Join(dir, rec.File)
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", rec.File, err)
		}
		si := StreamIndex{Index: rec.Index, Dataset: rec.Dataset}

		pr, pw := io.Pipe()
		go func() {
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)
			for scanner.Scan() {
				line := scanner.Bytes()
				if len(line) == 0 {
					continue
				}
				var doc map[string]interface{}
				if err := json.Unmarshal(line, &doc); err != nil {
					pw.CloseWithError(fmt.Errorf("parse %s: %w", rec.File, err))
					return
				}
				doc = MapDocument(rec.Dataset, doc)
				b, err := MarshalRow(doc)
				if err != nil {
					pw.CloseWithError(err)
					return
				}
				if si.SampleID == "" {
					si.SampleID, _ = doc["id"].(string)
				}
				if _, err := pw.Write(append([]byte(b), '\n')); err != nil {
					return
				}
				si.Mapped++
			}
			if err := scanner.Err(); err != nil {
				pw.CloseWithError(err)
				return
			}
			pw.Close()
		}()
		if err := e.ch.InsertFile(ctx, rec.Dataset, pr); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
		res.PerIndex = append(res.PerIndex, si)
		res.PerDataset[rec.Dataset] += si.Mapped
		res.Total += si.Mapped
		if res.SampleIDs[rec.Dataset] == "" {
			res.SampleIDs[rec.Dataset] = si.SampleID
		}

		rec.Loaded = true
		if err := writeManifest(manifestPath, manifest); err != nil {
			return nil, fmt.Errorf("update manifest: %w", err)
		}
		if e.onIndex != nil {
			e.onIndex(rec.Index, rec.Dataset, si.Mapped)
		}
	}
	for _, si := range res.PerIndex {
		res.PerDataset[si.Dataset] += si.Mapped
		res.Total += si.Mapped
	}
	return res, nil
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
		manifestPath = filepath.Join(stateDir, "stream.yaml")
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
					si, err := e.streamOne(ctx, j.ds, j.idx)
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

func sanitizeFilename(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		}
		return r
	}, s)
}

func writeManifest(path string, m *dumpManifest) error {
	out, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

func readManifest(path string) (*dumpManifest, error) {
	in, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m dumpManifest
	if err := yaml.Unmarshal(in, &m); err != nil {
		return nil, err
	}
	return &m, nil
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
