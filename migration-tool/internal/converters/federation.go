package converters

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
)

const insertConnectionKeySQL = `INSERT INTO connection_keys (tenant_id, key)
VALUES ($1, $2)
ON CONFLICT (tenant_id) DO UPDATE SET
	key = EXCLUDED.key`

type federationConverter struct{}

func (federationConverter) SrcTable() string { return "utm_federation_service_client" }

func (federationConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	// The source table lives in the utmstack database but the destination
	// table (connection_keys) lives in the agentmanager database. This
	// converter is only reached via ImportAll with all database handles.
	return 0, fmt.Errorf("federation requires cross-database import (use ImportAll)")
}

func (c federationConverter) ImportAll(ctx context.Context, tables map[string][]map[string]any, dbs map[string]*sql.DB) (map[string]int, error) {
	am, ok := dbs[domain.AgentManagerDatabase]
	if !ok {
		return nil, fmt.Errorf("agentmanager database not available for connection_keys import")
	}

	count := 0
	for _, src := range tables["utm_federation_service_client"] {
		key := fieldString(src, "fs_client_token")
		if key == "" {
			continue
		}
		if _, err := am.ExecContext(ctx, insertConnectionKeySQL, DefaultTenantID, key); err != nil {
			return nil, fmt.Errorf("utm_federation_service_client → connection_keys: %w", err)
		}
		count++
	}
	return map[string]int{"utm_federation_service_client": count}, nil
}

func init() {
	importer.Register(federationConverter{})
}
