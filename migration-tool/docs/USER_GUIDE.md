# UTMStack Migration Tool — User Guide

This guide walks you through migrating an UTMStack V10 installation to V11.

> **Looking for the Spanish version?** See [`USER_GUIDE.es.md`](USER_GUIDE.es.md).

---

## Before you start

You need:

- A working **UTMStack V10** installation
- A Linux server (Ubuntu, Debian, or similar)
- **`sudo` / root access** on that server
- Internet access (the tool downloads the migration agent from Google Cloud Storage)

You should also know:

- Whether you are upgrading **on the same server** (in-place) or **moving to a new server** (two servers)
- The IP and SSH credentials of your V10 server (only required if you run the tool from a separate workstation)

---

## Installation

1. Download the binary from the official releases page.
2. Make it executable:

   ```bash
   chmod +x utmstack_migration_tool
   ```

3. Verify it runs:

   ```bash
   ./utmstack_migration_tool --help
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

Before upgrading the backend, every UTMStack agent must receive the
`migrate_agent` binary. This command takes care of that and remembers its
progress, so you can interrupt it (Ctrl+C) and resume later without losing work.

#### How to run it

On the V10 server itself (no config file needed):

```bash
sudo ./utmstack_migration_tool prepare-agents
```

From a separate workstation, with a config file:

```bash
sudo ./utmstack_migration_tool prepare-agents --config config.yaml
```

#### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--config`, `-c` | Path to the configuration file. Required for remote use. | (none) |
| `--db-path` | Path to the SQLite state file. | `./agents-migration.db` |

#### What you will see

The command opens a loop that:

- Polls the AgentManager every 60 seconds
- Detects new agents that came online and deploys to them
- Retries failed agents with a backoff (30 s, 1 m, 2 m, 4 m, 8 m, then capped)
- Marks agents as `gone` if they disappear from the AgentManager for several cycles
- Prints a progress line per cycle, e.g.

  ```
  Progress: 47 deployed | 3 pending | 2 offline | 0 failed | 0 skipped | 0 gone | total 52
  ```

The loop ends in one of three ways:

1. Every agent reaches a terminal state (`deployed`, `skipped`, or `gone`).
2. You press **Ctrl+C** and accept marking the remaining agents as `skipped`.
3. The AgentManager becomes unreachable for too long (the command aborts with a clear error).

#### Interrupting the loop

If you do not want to wait for offline agents to come back online, press
**Ctrl+C**. The tool will:

- Stop the loop
- Show how many agents are still pending / offline / failed
- Ask whether to mark them all as `skipped`

If you accept, those agents are recorded as **skipped**. Skipped agents will
need the migration agent installed manually after the upgrade. If you decline,
state is preserved and you can run `prepare-agents` again later to continue.

#### Resuming

Run the same command again from the same directory. The SQLite state file
(`agents-migration.db`) keeps track of which agents are already done — they
won't be re-deployed.

> **Important:** Run the command from the **same directory** every time, so it
> finds the same `agents-migration.db`.

---

### `upgrade` — In-place V10 → V11 upgrade

This command upgrades the backend on the same server. It is **destructive**:
V10 is removed and V11 is installed in its place. There is no automatic rollback.

#### How to run it

On the V10 server itself (recommended, no config file needed):

```bash
sudo ./utmstack_migration_tool upgrade
```

From a separate workstation:

```bash
sudo ./utmstack_migration_tool upgrade --config config.yaml
```

#### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--config`, `-c` | Path to the configuration file. Optional for local upgrades. | (none) |
| `--data-file` | Custom name for the temporary backup file. | `utmstack-upgrade-YYYYMMDD-HHMMSS.yml` |
| `--skip-agent-check` | Skip the `prepare-agents` verification. **Use with care.** | `false` |

#### What it does

1. Confirms the destructive operation with you (you must type `yes`)
2. Checks that `agents-migration.db` exists (proof that `prepare-agents` ran)
3. Opens the database port
4. Exports all V10 configuration data to a backup file
5. Removes the V10 installation
6. Downloads and installs UTMStack V11
7. Imports the data back into V11
8. Closes the database port

#### About `--skip-agent-check`

`upgrade` refuses to run if it can't find `agents-migration.db` in the current
directory. This is a safeguard so you don't forget to prepare your agents.

If you have a legitimate reason to skip it (test environment, no agents, agents
deployed by other means), pass `--skip-agent-check`. Any agent that did not
receive the migration agent will remain on V10 and disconnect after the upgrade.

---

### `migrate` — Move V10 to a new V11 server

Use this when you want to move from one server to a different one (for example,
new hardware). The source V10 stays untouched until the migration succeeds.

#### How to run it

```bash
sudo ./utmstack_migration_tool migrate --config config.yaml
```

#### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--config`, `-c` | Path to the configuration file. **Required.** | (none) |
| `--data-file` | Custom name for the data file. | `utmstack-migration-YYYYMMDD-HHMMSS.yml` |

The config file must define both `source` (V10) and `destination` (V11). See
[Configuration file](#configuration-file) below.

#### What it does

1. Connects to the source V10 server over SSH
2. Exports all V10 data into a YAML file
3. Connects to the destination V11 server over SSH
4. Imports the data into V11
5. Synchronizes the security key between both servers
6. Asks whether to delete the temporary data file

---

### `export` — Save V10 data to a file

Saves V10 data to a YAML file you can keep for later. Pairs with `import`.

```bash
sudo ./utmstack_migration_tool export --config config.yaml
sudo ./utmstack_migration_tool export --config config.yaml --output my-backup.yml
```

| Flag | Description | Default |
|------|-------------|---------|
| `--config`, `-c` | Configuration file. **Required.** | (none) |
| `--output`, `-o` | Output file name. | `utmstack-export-YYYYMMDD-HHMMSS.yml` |

---

### `import` — Load a saved file into V11

Reads a YAML file produced by `export` and writes its content into a V11 server.

```bash
sudo ./utmstack_migration_tool import --config config.yaml --input my-backup.yml
```

| Flag | Description | Default |
|------|-------------|---------|
| `--config`, `-c` | Configuration file. **Required.** | (none) |
| `--input`, `-i` | Input file. **Required.** | (none) |

---

## Configuration file

The config file is YAML. The shape depends on the command:

- For **`upgrade`** running locally on the V10 server: no file needed.
- For **`upgrade`** / **`prepare-agents`** / **`export`** running remotely: only `source` is required.
- For **`migrate`** / **`import`**: `destination` is required (and `source` for `migrate`).

### Minimal example for remote operations

```yaml
source:
  host: "192.168.1.100"          # V10 server IP or hostname
  ssh_user: "utmstack"           # SSH username
  ssh_password: "your-ssh-pass"  # SSH password
  # ssh_private_key: "/home/me/.ssh/id_rsa"   # Alternative to ssh_password
```

### Minimal example for `migrate`

```yaml
source:
  host: "192.168.1.100"
  ssh_user: "utmstack"
  ssh_password: "ssh-pass-source"

destination:
  host: "192.168.1.200"
  ssh_user: "utmstack"
  ssh_password: "ssh-pass-destination"
```

### All available fields

```yaml
source:
  host: "192.168.1.100"      # Required for remote operations
  port: 5432                 # PostgreSQL port (default 5432)
  user: "postgres"           # PostgreSQL user (default postgres)
  password: ""               # Auto-detected from compose.yml if empty
  sslmode: "disable"         # PostgreSQL SSL mode (default disable)
  ssh_user: "utmstack"       # SSH user (required for remote)
  ssh_password: ""           # SSH password
  ssh_private_key: ""        # Path to a private key file (alternative to ssh_password)
  ssh_port: 22               # SSH port (default 22)

destination:
  # Same fields as source. Used by migrate and import.
```

> **Tip:** You usually only need `host`, `ssh_user`, and either `ssh_password` or
> `ssh_private_key`. The rest is auto-detected.

---

## Common workflows

### Workflow 1 — Local in-place upgrade (simplest)

You are sitting on the V10 server and want to upgrade it to V11 in place.

```bash
ssh utmstack@v10-server
sudo ./utmstack_migration_tool prepare-agents
# wait until all agents are deployed (or Ctrl+C and accept skip)
sudo ./utmstack_migration_tool upgrade
```

### Workflow 2 — Remote in-place upgrade

You manage your V10 server from a different machine.

```bash
cat > config.yaml <<EOF
source:
  host: "192.168.1.100"
  ssh_user: "utmstack"
  ssh_password: "your-ssh-pass"
EOF

sudo ./utmstack_migration_tool prepare-agents --config config.yaml
sudo ./utmstack_migration_tool upgrade --config config.yaml
```

### Workflow 3 — Move to a new server

You have a fresh V11 server and want to bring V10 data over without touching V10.

```bash
cat > config.yaml <<EOF
source:
  host: "192.168.1.100"
  ssh_user: "utmstack"
  ssh_password: "v10-ssh-pass"

destination:
  host: "192.168.1.200"
  ssh_user: "utmstack"
  ssh_password: "v11-ssh-pass"
EOF

sudo ./utmstack_migration_tool prepare-agents --config config.yaml
sudo ./utmstack_migration_tool migrate --config config.yaml
```

### Workflow 4 — Export now, import later

When you want to back up V10 and import the data later (or to multiple V11 servers).

```bash
# Export
sudo ./utmstack_migration_tool export --config config.yaml --output backup.yml

# (Move backup.yml wherever you need it, review it, etc.)

# Import on the V11 side
sudo ./utmstack_migration_tool import --config config.yaml --input backup.yml
```

---

## Troubleshooting

### "permission denied" reading `/root/utmstack.yml` or `/root/compose.yml`

You must run the tool with `sudo`. The configuration files live under `/root/`
on UTMStack V10, and only root can read them.

```bash
sudo ./utmstack_migration_tool prepare-agents
```

### "State database 'agents-migration.db' not found" on `upgrade`

You forgot to run `prepare-agents` before `upgrade`, or you are running
`upgrade` from a different directory.

Either:

- Run `prepare-agents` first from the same directory, or
- Pass `--skip-agent-check` if you know what you are doing.

### "AgentManager unreachable after 5 consecutive failures"

The tool can't reach the AgentManager service on the V10 server. Check that:

- The V10 server is up and `docker service ls` shows `utmstack_agentmanager` running
- Port `9000` is reachable from where you are running the tool
- The internal key in `/root/utmstack.yml` has not been modified by hand

### "compose.yml not found"

The tool looks for `compose.yml` in these locations, in order:

- `~/compose.yml` (the home directory of the user running the tool)
- `/root/compose.yml`
- `/home/utmstack/compose.yml`

If your `compose.yml` lives elsewhere, copy or symlink it into one of those
paths, or run the tool from a directory where it can be found.

### "failed to connect via SSH"

Check that:

- The SSH credentials in `config.yaml` are correct
- SSH (port 22 by default) is reachable from your machine
- You can connect manually: `ssh utmstack@<host>`
- If you use `ssh_private_key`, the file path is correct and the key is unencrypted

### Loop ran for hours but several agents stay `offline`

That is expected if those agents are physically off, on a laptop that's closed,
or behind an unreachable network. You have two options:

- Keep `prepare-agents` running and wait for them to come back
- Press Ctrl+C, accept marking them as `skipped`, run `upgrade`, and install
  the migration agent on those machines manually later

### The `import` step fails on a specific table

The tool uses `ON CONFLICT DO UPDATE`, so re-running the import is safe — it
won't duplicate data. Fix the underlying issue (for example, a constraint in
V11) and run `import` again.

---

## Files the tool creates

| File | Created by | Purpose |
|------|------------|---------|
| `agents-migration.db` | `prepare-agents` | Tracks which agents already received the migration agent. Keep it until `upgrade` is done. |
| `utmstack-upgrade-<timestamp>.yml` | `upgrade` | Temporary backup of V10 data. The tool reuses it during the upgrade. |
| `utmstack-migration-<timestamp>.yml` | `migrate` | Temporary data file. You will be asked whether to delete it at the end. |
| `utmstack-export-<timestamp>.yml` | `export` | Your V10 data dump. Keep it for as long as you need. |

---

## Getting help

- Run any command with `--help` to see all its options.
- Open an issue at the project's GitHub repository if you hit a real bug.
