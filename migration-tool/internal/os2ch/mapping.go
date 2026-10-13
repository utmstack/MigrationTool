package os2ch

import (
	"encoding/json"
	"strings"
)

// MapDocument converts a raw V11 document into the shape the V12 ClickHouse
// table expects. It returns the document as a string so it can be piped
// straight into a CH insert.
func MapDocument(ds Dataset, doc map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(doc))
	for k, v := range doc {
		out[k] = v
	}

	switch ds {
	case DatasetLogs:
		// V11 stores the parsed payload under `event`; the V12 logs table has
		// no `event` column, it lands in the `log` JSON column.
		if ev, ok := out["event"]; ok {
			delete(out, "event")
			if _, has := out["log"]; !has {
				out["log"] = ev
			}
		}
	case DatasetAlerts:
		// V11 persists the alert status as a numeric code, but V12 stores the
		// label string in `status` and the UI filters by it (e.g. "Open"), so a
		// code like 2 would never match. Translate the code to its label.
		if code, ok := out["status"].(float64); ok {
			label := alertStatusLabel(int(code))
			if sl, ok := out["statusLabel"].(string); ok && sl != "" {
				label = sl
			}
			out["status"] = label
		}
		// The V11 alert names its link/field-list attributes differently from
		// the V12 schema (reference vs references, groupedBy vs groupBy, ...),
		// and they may arrive as a single value where V12 expects an array.
		for src, dst := range map[string]string{
			"reference":      "references",
			"groupedBy":      "groupBy",
			"deduplicatedBy": "deduplicateBy",
		} {
			if v, ok := out[src]; ok {
				out[dst] = ensureSlice(v)
				delete(out, src)
			}
		}
		// V11 alerts carry no lastUpdate, and the ReplacingMergeTree collapses
		// a 1970 row against anything newer; use the alert timestamp.
		if _, ok := out["lastUpdate"]; !ok {
			if ts, ok2 := out["@timestamp"].(string); ok2 && ts != "" {
				out["lastUpdate"] = ts
			}
		}
	}

	// V11 has no multi-tenancy; every V12 dataset row must belong to the
	// default tenant or the UI (tenant-scoped) never shows it. Foreign
	// tenantIds seen in V11 data (e.g. O365) are normalised away.
	out["tenantId"] = DefaultTenantID
	if _, ok := out["tenantName"]; !ok {
		out["tenantName"] = "Default"
	}

	return out
}

// ensureSlice returns v as a []interface{} if it already is one, or wraps it
// in a single-element slice otherwise.
func ensureSlice(v interface{}) []interface{} {
	if s, ok := v.([]interface{}); ok {
		return s
	}
	return []interface{}{v}
}

// alertStatusLabel maps a V11 alert status code to its V12 label. The codes
// come from the V11 AlertStatus enum; the labels are what the V12 backend
// writes to `status` and filters on.
func alertStatusLabel(code int) string {
	switch code {
	case 1:
		return "Automatic review"
	case 2:
		return "Open"
	case 3:
		return "In review"
	case 5:
		return "Completed"
	default:
		return ""
	}
}

// MarshalRow encodes a document as a single-line JSONEachRow value, which is
// the format ClickHouse's native/HTTP insert expects.
func MarshalRow(doc map[string]interface{}) (string, error) {
	line, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(line)), nil
}
