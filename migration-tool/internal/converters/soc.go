package converters

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

const insertAlertTagSQL = `INSERT INTO alert_tag (id, tenant_id, tag_name, tag_color, system_owner)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO UPDATE SET
	tenant_id = EXCLUDED.tenant_id,
	tag_name = EXCLUDED.tag_name,
	tag_color = EXCLUDED.tag_color,
	system_owner = EXCLUDED.system_owner`

type alertTagConverter struct{}

func (alertTagConverter) SrcTable() string { return "utm_alert_tag" }

func (alertTagConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	count := 0
	for _, src := range rows {
		id := fieldString(src, "id")
		if id == "" {
			continue
		}
		_, err := destDB.ExecContext(ctx, insertAlertTagSQL,
			tagUUIDForV11ID(id), DefaultTenantID,
			fieldString(src, "tag_name"), fieldString(src, "tag_color"),
			fieldBool(src["system_owner"]))
		if err != nil {
			return 0, fmt.Errorf("utm_alert_tag → alert_tag (id=%s): %w", id, err)
		}
		count++
	}
	return count, nil
}

const insertAlertTagRuleSQL = `INSERT INTO alert_tag_rule (id, tenant_id, rule_name, rule_description, rule_conditions, rule_applied_tags, rule_active, rule_deleted)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET
	tenant_id = EXCLUDED.tenant_id,
	rule_name = EXCLUDED.rule_name,
	rule_description = EXCLUDED.rule_description,
	rule_conditions = EXCLUDED.rule_conditions,
	rule_applied_tags = EXCLUDED.rule_applied_tags,
	rule_active = EXCLUDED.rule_active,
	rule_deleted = EXCLUDED.rule_deleted`

type alertTagRuleConverter struct{}

func (alertTagRuleConverter) SrcTable() string { return "utm_alert_tag_rule" }

// ImportAll is a CrossTableConverter: rule_applied_tags holds V11 tag ids
// that must be remapped to the new tag uuids, unknown ids are dropped.
func (alertTagRuleConverter) ImportAll(ctx context.Context, tables map[string][]map[string]any, destDB *sql.DB) (map[string]int, error) {
	imported := map[string]int{}
	tagIDs := map[string]bool{}
	for _, src := range tables["utm_alert_tag"] {
		if id := fieldString(src, "id"); id != "" {
			tagIDs[id] = true
		}
	}
	count := 0
	for _, src := range tables["utm_alert_tag_rule"] {
		id := fieldString(src, "id")
		if id == "" {
			continue
		}
		applied := remapTagCSV(fieldString(src, "rule_applied_tags"), tagIDs)
		_, err := destDB.ExecContext(ctx, insertAlertTagRuleSQL,
			alertTagRuleUUIDForV11ID(id), DefaultTenantID,
			fieldString(src, "rule_name"), fieldString(src, "rule_description"),
			fieldString(src, "rule_conditions"), applied,
			fieldBool(src["rule_active"]), fieldBool(src["rule_deleted"]))
		if err != nil {
			return imported, fmt.Errorf("utm_alert_tag_rule → alert_tag_rule (id=%s): %w", id, err)
		}
		count++
	}
	imported["utm_alert_tag_rule"] = count
	return imported, nil
}

// Import is not used directly; ImportAll is the entry point.
func (alertTagRuleConverter) Import(_ context.Context, _ []map[string]interface{}, _ *sql.DB) (int, error) {
	return 0, nil
}

func remapTagCSV(csv string, known map[string]bool) string {
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !known[p] {
			ui.Logf("WARNING: tag rule references unknown tag id %s - dropped", p)
			continue
		}
		out = append(out, tagUUIDForV11ID(p))
	}
	return strings.Join(out, ",")
}

const insertIncidentSQL = `INSERT INTO incident (id, tenant_id, incident_name, incident_description, incident_status, incident_severity, incident_assigned_to, incident_solution, incident_created_date)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
	tenant_id = EXCLUDED.tenant_id,
	incident_name = EXCLUDED.incident_name,
	incident_description = EXCLUDED.incident_description,
	incident_status = EXCLUDED.incident_status,
	incident_severity = EXCLUDED.incident_severity,
	incident_assigned_to = EXCLUDED.incident_assigned_to,
	incident_solution = EXCLUDED.incident_solution,
	incident_created_date = EXCLUDED.incident_created_date`

type incidentConverter struct{}

func (incidentConverter) SrcTable() string { return "utm_incident" }

func (incidentConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	count := 0
	for _, src := range rows {
		id := fieldString(src, "id")
		if id == "" {
			continue
		}
		description := fieldString(src, "incident_description")
		solution := fieldString(src, "incident_solution")
		var descPtr, solPtr *string
		if description != "" {
			descPtr = &description
		}
		if solution != "" {
			solPtr = &solution
		}
		_, err := destDB.ExecContext(ctx, insertIncidentSQL,
			incidentUUIDForV11ID(id), DefaultTenantID,
			fieldString(src, "incident_name"), descPtr,
			socStatus(fieldString(src, "incident_status")),
			socSeverity(fieldString(src, "incident_severity")),
			fieldString(src, "incident_assigned_to"), solPtr,
			parseSocTime(src["incident_created_date"]))
		if err != nil {
			return 0, fmt.Errorf("utm_incident → incident (id=%s): %w", id, err)
		}
		count++
	}
	return count, nil
}

const insertIncidentAlertSQL = `INSERT INTO incident_alert (id, tenant_id, incident_id, alert_id, alert_name, alert_severity, alert_status)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO UPDATE SET
	tenant_id = EXCLUDED.tenant_id,
	incident_id = EXCLUDED.incident_id,
	alert_id = EXCLUDED.alert_id,
	alert_name = EXCLUDED.alert_name,
	alert_severity = EXCLUDED.alert_severity,
	alert_status = EXCLUDED.alert_status`

type incidentAlertConverter struct{}

func (incidentAlertConverter) SrcTable() string { return "utm_incident_alert" }

func (incidentAlertConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	count := 0
	for _, src := range rows {
		id := fieldString(src, "id")
		incidentID := fieldString(src, "incident_id")
		if id == "" || incidentID == "" {
			continue
		}
		_, err := destDB.ExecContext(ctx, insertIncidentAlertSQL,
			incidentAlertUUIDForV11ID(id), DefaultTenantID,
			incidentUUIDForV11ID(incidentID),
			fieldString(src, "alert_id"), fieldString(src, "alert_name"),
			socSeverity(fieldString(src, "alert_severity")),
			socAlertStatus(fieldString(src, "alert_status")))
		if err != nil {
			return 0, fmt.Errorf("utm_incident_alert → incident_alert (id=%s): %w", id, err)
		}
		count++
	}
	return count, nil
}

const insertIncidentHistorySQL = `INSERT INTO incident_history (id, tenant_id, incident_id, action, action_created_date, action_created_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO UPDATE SET
	tenant_id = EXCLUDED.tenant_id,
	incident_id = EXCLUDED.incident_id,
	action = EXCLUDED.action,
	action_created_date = EXCLUDED.action_created_date,
	action_created_by = EXCLUDED.action_created_by`

type incidentHistoryConverter struct{}

func (incidentHistoryConverter) SrcTable() string { return "utm_incident_history" }

func (incidentHistoryConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	count := 0
	for _, src := range rows {
		id := fieldString(src, "id")
		incidentID := fieldString(src, "incident_id")
		if id == "" || incidentID == "" {
			continue
		}
		action := socHistoryAction(fieldString(src, "action_type"))
		if action == "" {
			continue
		}
		_, err := destDB.ExecContext(ctx, insertIncidentHistorySQL,
			incidentHistoryUUIDForV11ID(id), DefaultTenantID,
			incidentUUIDForV11ID(incidentID), action,
			parseSocTime(src["action_date"]), fieldString(src, "action_created_by"))
		if err != nil {
			return 0, fmt.Errorf("utm_incident_history → incident_history (id=%s): %w", id, err)
		}
		count++
	}
	return count, nil
}

const insertIncidentNoteSQL = `INSERT INTO incident_note (id, tenant_id, incident_id, note_text, note_send_date, note_send_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (id) DO UPDATE SET
	tenant_id = EXCLUDED.tenant_id,
	incident_id = EXCLUDED.incident_id,
	note_text = EXCLUDED.note_text,
	note_send_date = EXCLUDED.note_send_date,
	note_send_by = EXCLUDED.note_send_by`

type incidentNoteConverter struct{}

func (incidentNoteConverter) SrcTable() string { return "utm_incident_note" }

func (incidentNoteConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	count := 0
	for _, src := range rows {
		id := fieldString(src, "id")
		incidentID := fieldString(src, "incident_id")
		if id == "" || incidentID == "" {
			continue
		}
		_, err := destDB.ExecContext(ctx, insertIncidentNoteSQL,
			incidentNoteUUIDForV11ID(id), DefaultTenantID,
			incidentUUIDForV11ID(incidentID), fieldString(src, "note_text"),
			parseSocTime(src["note_send_date"]), fieldString(src, "note_send_by"))
		if err != nil {
			return 0, fmt.Errorf("utm_incident_note → incident_note (id=%s): %w", id, err)
		}
		count++
	}
	return count, nil
}

func init() {
	importer.Register(alertTagConverter{})
	importer.Register(alertTagRuleConverter{})
	importer.Register(incidentConverter{})
	importer.Register(incidentAlertConverter{})
	importer.Register(incidentHistoryConverter{})
	importer.Register(incidentNoteConverter{})
}
