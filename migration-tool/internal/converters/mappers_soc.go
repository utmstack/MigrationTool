package converters

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	tagNamespace             = "402218d7-f216-56d0-a6bc-be7414c8aff9"
	alertTagRuleNamespace    = "d65b0f32-73f4-5340-acf5-ae4df2dbaba0"
	incidentNamespace        = "ffa82061-9bd3-56c0-8f37-e34e33e2f9d0"
	incidentAlertNamespace   = "b685a259-ebac-589d-9425-cfc09cf2e9b9"
	incidentHistoryNamespace = "c7c9699c-a814-5e85-ba9d-ad11ddac670d"
	incidentNoteNamespace    = "351bf618-4905-5df6-bf66-2e7280a205d0"
)

func tagUUIDForV11ID(v11ID string) string {
	return sha1UUID(tagNamespace, v11ID)
}

func alertTagRuleUUIDForV11ID(v11ID string) string {
	return sha1UUID(alertTagRuleNamespace, v11ID)
}

func incidentUUIDForV11ID(v11ID string) string {
	return sha1UUID(incidentNamespace, v11ID)
}

func incidentAlertUUIDForV11ID(v11ID string) string {
	return sha1UUID(incidentAlertNamespace, v11ID)
}

func incidentHistoryUUIDForV11ID(v11ID string) string {
	return sha1UUID(incidentHistoryNamespace, v11ID)
}

func incidentNoteUUIDForV11ID(v11ID string) string {
	return sha1UUID(incidentNoteNamespace, v11ID)
}

func socStatus(v11 string) string {
	switch strings.ToUpper(strings.TrimSpace(v11)) {
	case "OPEN":
		return "Open"
	case "IN_REVIEW":
		return "In review"
	case "COMPLETED":
		return "Completed"
	case "MERGED":
		return "Merged"
	default:
		return "Open"
	}
}

func socSeverity(n string) string {
	i, err := strconv.Atoi(strings.TrimSpace(n))
	if err != nil {
		return "medium"
	}
	switch i {
	case 1:
		return "low"
	case 3:
		return "high"
	default:
		return "medium"
	}
}

func socAlertStatus(n string) string {
	i, err := strconv.Atoi(strings.TrimSpace(n))
	if err != nil {
		return ""
	}
	switch i {
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

func socHistoryAction(v11 string) string {
	switch strings.TrimSpace(v11) {
	case "INCIDENT_MERGED", "INCIDENT_COMPLETED":
		return "INCIDENT_STATUS_CHANGE"
	case "INCIDENT_ASSIGNED_TO", "INCIDENT_ASSIGNED_CHANGE":
		return "INCIDENT_ASSIGNED"
	case "INCIDENT_ALERT_DELETED", "INCIDENT_ALERT_MOVED", "INCIDENT_COMMAND_EXECUTED":
		return ""
	default:
		return strings.TrimSpace(v11)
	}
}

// sha1UUID derives a deterministic uuid from the table namespace and the V11 id.
func sha1UUID(namespace, v11ID string) string {
	return uuid.NewSHA1(uuid.MustParse(namespace), []byte(v11ID)).String()
}

var socTimeFormats = []string{
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999",
	"2006-01-02 15:04:05",
}

// parseSocTime accepts a native time.Time or a YAML-roundtripped string.
// V11 stores UTC wall-clock times without a zone; parse them as UTC.
func parseSocTime(v any) time.Time {
	if t, ok := v.(time.Time); ok {
		return t
	}
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	for _, f := range socTimeFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

func fieldBool(v any) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "t", "true", "1", "yes":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
