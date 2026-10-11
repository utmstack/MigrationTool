package converters

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

const insertAgentSQL = `INSERT INTO agents (id, ip, hostname, os, platform, version, agent_key, deleted_at, register_by, deleted_by, mac, os_major_version, os_minor_version, aliases, addresses, tenant_id, no_remote_control)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT (id) DO UPDATE SET
	ip = EXCLUDED.ip,
	hostname = EXCLUDED.hostname,
	os = EXCLUDED.os,
	platform = EXCLUDED.platform,
	version = EXCLUDED.version,
	agent_key = EXCLUDED.agent_key,
	deleted_at = EXCLUDED.deleted_at,
	register_by = EXCLUDED.register_by,
	deleted_by = EXCLUDED.deleted_by,
	mac = EXCLUDED.mac,
	os_major_version = EXCLUDED.os_major_version,
	os_minor_version = EXCLUDED.os_minor_version,
	aliases = EXCLUDED.aliases,
	addresses = EXCLUDED.addresses,
	tenant_id = EXCLUDED.tenant_id`

type agentConverter struct{}

func (agentConverter) SrcTable() string { return "agents" }

func (c agentConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	count := 0
	for _, src := range rows {
		id := fieldString(src, "id")
		if id == "" {
			continue
		}
		if _, err := destDB.ExecContext(ctx, insertAgentSQL,
			id,
			fieldString(src, "ip"),
			fieldString(src, "hostname"),
			nullIfEmpty(fieldString(src, "os")),
			nullIfEmpty(fieldString(src, "platform")),
			nullIfEmpty(fieldString(src, "version")),
			nullIfEmpty(fieldString(src, "agent_key")),
			parseModTime(src["deleted_at"]),
			fieldString(src, "register_by"),
			nullIfEmpty(fieldString(src, "deleted_by")),
			nullIfEmpty(fieldString(src, "mac")),
			nullIfEmpty(fieldString(src, "os_major_version")),
			nullIfEmpty(fieldString(src, "os_minor_version")),
			nullIfEmpty(fieldString(src, "aliases")),
			nullIfEmpty(fieldString(src, "addresses")),
			DefaultTenantID,
			false,
		); err != nil {
			return 0, fmt.Errorf("agents (id=%s): %w", id, err)
		}
		count++
	}
	return count, nil
}

const insertCollectorSQL = `INSERT INTO collectors (id, collector_key, ip, hostname, version, module, deleted_by, tenant_id, no_remote_control)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (id) DO UPDATE SET
	collector_key = EXCLUDED.collector_key,
	ip = EXCLUDED.ip,
	hostname = EXCLUDED.hostname,
	version = EXCLUDED.version,
	module = EXCLUDED.module,
	deleted_by = EXCLUDED.deleted_by,
	tenant_id = EXCLUDED.tenant_id`

type collectorConverter struct{}

func (collectorConverter) SrcTable() string { return "collectors" }

func (c collectorConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	count := 0
	for _, src := range rows {
		id := fieldString(src, "id")
		if id == "" {
			continue
		}
		module := fieldString(src, "module")
		ui.Logf("converter: collectors (id=%s): module %q", id, module)
		if _, err := destDB.ExecContext(ctx, insertCollectorSQL,
			id,
			fieldString(src, "collector_key"),
			nullIfEmpty(fieldString(src, "ip")),
			fieldString(src, "hostname"),
			nullIfEmpty(fieldString(src, "version")),
			module,
			nullIfEmpty(fieldString(src, "deleted_by")),
			DefaultTenantID,
			false,
		); err != nil {
			return 0, fmt.Errorf("collectors (id=%s): %w", id, err)
		}
		count++
	}
	return count, nil
}

func init() {
	importer.Register(agentConverter{})
	importer.Register(collectorConverter{})
}
