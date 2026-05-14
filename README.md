# UTMStack Migration Tool — User Guide

This guide walks you through migrating an UTMStack V10 installation to V11.

## Installation

1. Download the binary from the official releases page.
2. Make it executable:

   ```bash
   chmod +x utmstack_migration_tool
   ```

3. Verify it runs:

   ```bash
   sudo ./utmstack_migration_tool --help
   ```

You should see a list of commands. If you do, the binary is ready.

---

## Recommended workflow

Most users only need two commands, in this exact order:

```bash
# Step 1: Stage the migration agent on every UTMStack agent.
sudo ./utmstack_migration_tool prepare-agents

# Step 2: Upgrade the backend itself.
sudo ./utmstack_migration_tool upgrade
```

Run **`prepare-agents` first**, then `upgrade`. The `upgrade` command checks that
`prepare-agents` already ran and refuses to start otherwise. This is on purpose:
it prevents agents from being orphaned in V10 after the backend moves to V11.

---

## Commands

### `prepare-agents` — Stage the migration agent

Before upgrading the instance, every UTMStack agent must receive the
`migrate_agent` binary. This command takes care of that and remembers its
progress, so you can interrupt it (Ctrl+C) and resume later without losing work.

#### How to run it

On the V10 server itself:

```bash
sudo ./utmstack_migration_tool prepare-agents
```

### `upgrade` — In-place V10 → V11 upgrade

This command upgrades the intance on the same server. It is **destructive**:
V10 is removed and V11 is installed in its place. There is no automatic rollback.

#### How to run it

On the V10 server itself:

```bash
sudo ./utmstack_migration_tool upgrade
```


## Common workflows

### Workflow 1 — Local in-place upgrade (simplest)

You are sitting on the V10 server and want to upgrade it to V11 in place.

```bash
ssh utmstack@v10-server
sudo ./utmstack_migration_tool prepare-agents
# wait until all agents are deployed (or Ctrl+C and accept skip)
sudo ./utmstack_migration_tool upgrade
```