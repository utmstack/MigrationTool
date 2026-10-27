package os2ch

// Dataset identifies which ClickHouse table a V11 OpenSearch index feeds.
type Dataset string

const (
	DatasetLogs    Dataset = "logs"
	DatasetAlerts  Dataset = "alerts"
	DatasetUnknown Dataset = ""
)

// DefaultTenantID is the V12 tenant every migrated row is written under.
// V11 has no multi-tenancy, so all V12 tenant-scoped queries only match this
// id; foreign tenantIds seen in V11 data are normalised to it.
const DefaultTenantID = "ce66672c-e36d-4761-a8c8-90058fee1a24"

// ClassifyIndex maps a V11 index name to its ClickHouse dataset. Only logs
// and alerts are tenant data sets with a V12 destination; statistics, backend
// and compliance-evaluation indices are derived/operational stores that are
// not migrated.
func ClassifyIndex(index string) (Dataset, bool) {
	switch {
	case index == "":
		return DatasetUnknown, false
	case index == "v11-backend-logs":
		return DatasetUnknown, false
	case index == "v11-log-compliance-evaluation":
		return DatasetUnknown, false
	case hasPrefix(index, "v11-statistics-"):
		return DatasetUnknown, false
	case hasPrefix(index, "v11-alert-"):
		return DatasetAlerts, true
	case hasPrefix(index, "v11-log-"):
		return DatasetLogs, true
	default:
		return DatasetUnknown, false
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
