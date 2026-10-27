package os2ch

import (
	"regexp"
	"time"
)

// indexDatePattern matches the first YYYY-MM-DD occurrence inside an index
// name. V11 log/alert indices embed their day this way (optionally with a
// random suffix after the date, e.g. v11-log-google-2026-08-03-lx4lkv).
var indexDatePattern = regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)

// indexDate extracts the calendar day embedded in an index name. It is the
// only place that knows V11's daily index naming. ok is false when the name
// has no valid date; callers must then include the index (fail-safe).
func indexDate(name string) (time.Time, bool) {
	m := indexDatePattern.FindString(name)
	if m == "" {
		return time.Time{}, false
	}
	d, err := time.Parse("2006-01-02", m)
	if err != nil {
		return time.Time{}, false
	}
	return d, true
}
