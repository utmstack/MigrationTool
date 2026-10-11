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
	case DatasetStatistics:
		// Statistics rows in V11 are tenant-less; the V12 table requires one.
		if _, ok := out["tenantId"]; !ok {
			out["tenantId"] = DefaultTenantID
		}
	}

	return out
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
