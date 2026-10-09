package converters

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
)

const insertConnectionKeySQL = `INSERT INTO connection_keys (tenant_id, key)
VALUES ($1, $2)
ON CONFLICT (tenant_id) DO UPDATE SET
	key = EXCLUDED.key`

type federationConverter struct{}

func (federationConverter) SrcTable() string { return "utm_federation_service_client" }

func (c federationConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	count := 0
	for _, src := range rows {
		key := fieldString(src, "fs_client_token")
		if key == "" {
			continue
		}
		log.Printf("converter: utm_federation_service_client: writing connection_key for tenant %s", DefaultTenantID)
		if _, err := destDB.ExecContext(ctx, insertConnectionKeySQL, DefaultTenantID, key); err != nil {
			return 0, fmt.Errorf("utm_federation_service_client → connection_keys: %w", err)
		}
		count++
	}
	return count, nil
}

func init() {
	importer.Register(federationConverter{})
}
