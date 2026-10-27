# UTMStack Migration Tool — V11 to V12

CLI tool to migrate UTMStack data, agents, and recent search logs from version 11 to version 12.

## Quick start

The fastest path: SSH into your V11 server, then run:

```bash
sudo ./utmstack_migration_tool prepare-agents
sudo ./utmstack_migration_tool upgrade --with-data
```

`--with-data` streams the tenant's logs and alerts from the last 7 days from
OpenSearch into the new ClickHouse. Without it, that search data is not moved
(the upgrade removes OpenSearch).

## What it does

| Command | Purpose |
|---------|---------|
| `prepare-agents` | Pre-deploys the migration agent to every UTMStack agent. **Run this first.** |
| `upgrade` | In-place upgrade of V11 → V12 on the same server (destructive). |
| `migrate` | Full migration between two separate servers (export + import). The destination must already be a running V12. |
| `export` | Export V11 data to a YAML file. |
| `import` | Import a YAML file into V12. |

`upgrade` and `migrate` both accept `--with-data`, `--data-file`, and
`--skip-agent-check`. See the [user guide](docs/USER_GUIDE.md)
([Spanish](docs/USER_GUIDE.es.md)) for details.

## Requirements

- Linux (Ubuntu)
- Root / sudo access on the V11 server
- Outbound HTTPS to `storage.googleapis.com` (for downloading the migration agent)
