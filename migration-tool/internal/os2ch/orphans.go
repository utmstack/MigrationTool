package os2ch

import (
	"fmt"
	"sort"
	"strings"
)

// ColumnSet is the set of column names a destination ClickHouse table
// accepts. A JSON-typed column (origin, log, ...) swallows any keys nested
// inside it, so only top-level document keys are compared against this set.
type ColumnSet map[string]struct{}

func NewColumnSet(names ...string) ColumnSet {
	cs := make(ColumnSet, len(names))
	for _, n := range names {
		cs[n] = struct{}{}
	}
	return cs
}

// OrphanKeys returns the sorted top-level keys of doc that the destination
// table has no column for. ClickHouse silently drops these, so surfacing
// them is the whole point of the report.
func OrphanKeys(doc map[string]interface{}, cols ColumnSet) []string {
	out := make([]string, 0)
	for k := range doc {
		if _, ok := cols[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// dataTypeOf reads the doc's dataType, tolerating a missing or non-string value.
func dataTypeOf(doc map[string]interface{}) string {
	if v, ok := doc["dataType"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "(no dataType)"
}

// OrphanReport accumulates, per dataType, which document keys have no
// destination column and how many docs carried them.
type OrphanReport struct {
	keys       map[string]map[string]struct{}
	count      map[string]map[string]int
	seen       map[string]int
	orphanDocs map[string]int
}

func NewOrphanReport() *OrphanReport {
	return &OrphanReport{
		keys:       map[string]map[string]struct{}{},
		count:      map[string]map[string]int{},
		seen:       map[string]int{},
		orphanDocs: map[string]int{},
	}
}

// Note inspects one already-mapped document against the destination columns.
// The report is keyed by the document's own dataType so the summary is
// grouped the way a V11 deployment is queried.
func (r *OrphanReport) Note(ds Dataset, doc map[string]interface{}, cols ColumnSet) {
	dt := dataTypeOf(doc)
	r.seen[dt]++
	orphans := OrphanKeys(doc, cols)
	if len(orphans) > 0 {
		r.orphanDocs[dt]++
	}
	for _, k := range orphans {
		if r.keys[dt] == nil {
			r.keys[dt] = map[string]struct{}{}
		}
		r.keys[dt][k] = struct{}{}
		if r.count[dt] == nil {
			r.count[dt] = map[string]int{}
		}
		r.count[dt][k]++
	}
}

// Orphans lists the orphan keys for a dataType.
func (r *OrphanReport) Orphans(dt string) []string {
	out := make([]string, 0, len(r.keys[dt]))
	for k := range r.keys[dt] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Count returns how many docs of dataType carried the given orphan key.
func (r *OrphanReport) Count(dt, key string) int {
	return r.count[dt][key]
}

// Seen returns how many docs of dataType were inspected.
func (r *OrphanReport) Seen(dt string) int {
	return r.seen[dt]
}

// TotalOrphans sums distinct orphan keys across all dataTypes.
func (r *OrphanReport) TotalOrphans() int {
	n := 0
	for _, s := range r.keys {
		n += len(s)
	}
	return n
}

// Summary renders the report for humans. With no orphans it is a single
// all-clear line; otherwise it lists each dataType and its orphan keys with
// the number of docs that carried them.
func (r *OrphanReport) Summary() string {
	if r.TotalOrphans() == 0 {
		return "no orphan keys: every document field has a destination column"
	}
	dts := make([]string, 0, len(r.keys))
	for dt := range r.keys {
		dts = append(dts, dt)
	}
	sort.Strings(dts)

	var lines []string
	lines = append(lines, fmt.Sprintf("%d orphan key(s) across %d dataType(s):", r.TotalOrphans(), len(dts)))
	for _, dt := range dts {
		keys := r.Orphans(dt)
		var keyParts []string
		for _, k := range keys {
			keyParts = append(keyParts, fmt.Sprintf("%s (x%d)", k, r.Count(dt, k)))
		}
		lines = append(lines, fmt.Sprintf("  %s (%d docs): %s", dt, r.orphanDocs[dt], strings.Join(keyParts, ", ")))
	}
	return strings.Join(lines, "\n")
}
