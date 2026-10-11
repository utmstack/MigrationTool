package converters

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

// configUUIDNamespace derives V12 app_config IDs from the V11 short key.
// Deriving from the key (not the surrogate id) makes re-runs idempotent and
// keeps this converter independent of section ordering.
var configUUIDNamespace = uuid.MustParse("04f5f62e-5ec0-4042-825b-2c7b8a38ec73")

func configUUIDForKey(key string) string {
	return uuid.NewSHA1(configUUIDNamespace, []byte(key)).String()
}

type configRow struct {
	ID          string
	TenantID    string
	Key         string
	Label       string
	Description string
	Value       string
	IsSecret    bool
	UpdatedAt   *time.Time
	UpdatedBy   string
}

func parseModTime(v interface{}) *time.Time {
	switch val := v.(type) {
	case time.Time:
		if val.IsZero() {
			return nil
		}
		t := val
		return &t
	case string:
		if val == "" {
			return nil
		}
		if t, err := time.Parse("2006-01-02 15:04:05.999999", val); err == nil {
			return &t
		}
	}
	return nil
}

func mapConfigRow(src map[string]interface{}) (configRow, bool) {
	key := strings.TrimSpace(fieldString(src, "conf_param_short"))
	if key == "" {
		return configRow{}, false
	}

	value := fieldString(src, "conf_param_value")
	isSecret := strings.EqualFold(fieldString(src, "conf_param_datatype"), "password")
	if isSecret && value == "*****" {
		// V12 would fail to decrypt the UI mask; store empty instead.
		value = ""
	}

	updatedBy := fieldString(src, "modification_user")

	return configRow{
		ID:          configUUIDForKey(key),
		TenantID:    DefaultTenantID,
		Key:         key,
		Label:       fieldString(src, "conf_param_large"),
		Description: fieldString(src, "conf_param_description"),
		Value:       value,
		IsSecret:    isSecret,
		UpdatedAt:   parseModTime(src["modification_time"]),
		UpdatedBy:   updatedBy,
	}, true
}

const insertConfigSQL = `INSERT INTO app_config (id, tenant_id, key, label, description, value, is_secret, updated_at, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (tenant_id, key) DO UPDATE SET
	value = EXCLUDED.value,
	is_secret = EXCLUDED.is_secret,
	label = EXCLUDED.label,
	description = EXCLUDED.description,
	updated_at = EXCLUDED.updated_at,
	updated_by = EXCLUDED.updated_by`

type configConverter struct{}

func (configConverter) SrcTable() string { return "utm_configuration_parameter" }

func (c configConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	seen := map[string]bool{}
	count := 0
	skipped := 0
	for _, src := range rows {
		key := fieldString(src, "conf_param_short")
		if key == "" {
			skipped++
			continue
		}
		if seen[key] {
			ui.Logf("converter: utm_configuration_parameter: duplicate key %q (first row wins), skipping", key)
			continue
		}
		seen[key] = true
		row, ok := mapConfigRow(src)
		if !ok {
			skipped++
			continue
		}
		if _, err := destDB.ExecContext(ctx, insertConfigSQL,
			row.ID, row.TenantID, row.Key, row.Label, row.Description, row.Value,
			row.IsSecret, row.UpdatedAt, nullIfEmpty(row.UpdatedBy)); err != nil {
			return 0, fmt.Errorf("utm_configuration_parameter → app_config (key=%s): %w", key, err)
		}
		count++
	}
	if skipped > 0 {
		ui.Logf("converter: utm_configuration_parameter: %d rows skipped (empty key)", skipped)
	}
	return count, nil
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func init() {
	importer.Register(configConverter{})
}
