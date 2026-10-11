package os2ch

// Dataset identifies which ClickHouse table a V11 OpenSearch index feeds.
type Dataset string

const (
	DatasetLogs       Dataset = "logs"
	DatasetAlerts     Dataset = "alerts"
	DatasetStatistics Dataset = "statistics"
	DatasetUnknown    Dataset = ""
)

// DefaultTenantID is the tenant injected into documents that lack one
// (statistics rows). It matches the tenant UUID the V11 single-tenant
// instance already writes into its log documents.
const DefaultTenantID = "ce66672c-e36d-4761-a8c8-90058fee1a24"

// ClassifyIndex maps a V11 index name to its ClickHouse dataset. Only tenant
// data sets are migrated; backend and compliance-evaluation indices are
// derived/operational stores without a V12 destination.
func ClassifyIndex(index string) (Dataset, bool) {
	switch {
	case index == "":
		return DatasetUnknown, false
	case index == "v11-backend-logs":
		return DatasetUnknown, false
	case index == "v11-log-compliance-evaluation":
		return DatasetUnknown, false
	case hasPrefix(index, "v11-statistics-"):
		return DatasetStatistics, true
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
