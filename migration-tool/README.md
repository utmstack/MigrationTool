# UTMStack Migration Tool — V10 to V11

CLI tool to migrate UTMStack data and agents from version 10 to version 11.

## Quick start

The fastest path: SSH into your V10 server, then run:

```bash
sudo ./utmstack_migration_tool prepare-agents
sudo ./utmstack_migration_tool upgrade
```

## What it does

| Command | Purpose |
|---------|---------|
| `prepare-agents` | Pre-deploys the migration agent to every UTMStack agent. **Run this first.** |
| `upgrade` | In-place upgrade of V10 → V11 on the same server (destructive). |
| `migrate` | Full migration between two separate servers (export + import). |
| `export` | Export V10 data to a YAML file. |
| `import` | Import a YAML file into V11. |


## Requirements

- Linux (Ubuntu)
- Root / sudo access on the V10 server
- Outbound HTTPS to `storage.googleapis.com` (for downloading the migration agent)
