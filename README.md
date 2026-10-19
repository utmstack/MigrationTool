# UTMStack Migration Tool

A small program that migrates your **UTMStack V11** installation to **V12**.

It can do **three things**, depending on what you need:

| If you want to... | Use this |
|-------------------|----------|
| Upgrade V11 → V12 **on the same server** | [Mode A — In-place upgrade](#mode-a--in-place-upgrade-same-server) |
| Manage the upgrade **from a different machine** (over SSH) | [Mode B — Remote upgrade over SSH](#mode-b--remote-upgrade-over-ssh) |
| **Move** your data to a **brand-new V12 server** | [Mode C — Server-to-server migration](#mode-c--server-to-server-migration) |

> Not sure? **Mode A is what most people want.** Pick that one.

---

## First — download the tool

Run this on whichever machine you'll use to launch the tool (the V11 server
itself for Mode A, or your workstation for Mode B/C):

```bash
sudo curl -L -o utmstack_migration_tool \
  https://github.com/utmstack/MigrationTool/releases/latest/download/utmstack_migration_tool

sudo chmod +x utmstack_migration_tool
```

Check it works:

```bash
sudo ./utmstack_migration_tool --help
```

---

## Mode A — In-place upgrade (same server)

You are sitting on the V11 server and want to upgrade it to V12 in place.
**No config file needed** — the tool auto-detects the local PostgreSQL
credentials from `compose.yml`.

```bash
# 1. Get every agent ready
sudo ./utmstack_migration_tool prepare-agents

# 2. Do the actual upgrade
sudo ./utmstack_migration_tool upgrade
```

Wait for step 1 to finish before running step 2. Type `yes` when the upgrade
asks you to confirm. Done.

---

## Mode B — Remote upgrade over SSH

You want to upgrade a V11 server from your laptop (or another machine).
**You need a small config file with the SSH credentials.**

Create `config.yaml`:

```yaml
source:
  host: "192.168.1.100"          # your V11 server IP
  ssh_user: "utmstack"
  ssh_password: "your-ssh-password"
  # or use a key:
  # ssh_private_key: "/home/me/.ssh/id_rsa"
```

Then run:

```bash
sudo ./utmstack_migration_tool prepare-agents --config config.yaml
sudo ./utmstack_migration_tool upgrade --config config.yaml            # config, users, integrations, agents
sudo ./utmstack_migration_tool upgrade --config config.yaml --with-data  # + logs, alerts, statistics
```

`--with-data` is optional. Without it the search data is **not** moved — in an
upgrade OpenSearch is removed, so that data is lost. The flag streams the
data into the new ClickHouse before the old stack comes down.

Same idea as Mode A, but the tool reaches the V11 server over SSH.

---

## Mode C — Server-to-server migration

You want to move your V11 data to a **separate V12 server** **without
touching V11**. Useful when migrating to new hardware.

> ⚠️ **Important — install V12 yourself first.**
> This mode does **not** install V12 on the destination. You must have a
> working UTMStack **V12** installation already running on the destination
> server (Docker stack up, `utmstack_postgres` service running) before you
> launch `migrate`. The tool only **moves the data over**.

Create `config.yaml` with **both** servers:

```yaml
source:
  host: "192.168.1.100"          # your V11 server
  ssh_user: "utmstack"
  ssh_password: "v11-ssh-password"

destination:
  host: "192.168.1.200"          # your new V12 server
  ssh_user: "utmstack"
  ssh_password: "v12-ssh-password"
  # where V12 reads the migrated integration YAMLs (optional — the
  # default is /utmstack/events-engine-workdir/pipeline)
  # integrations_config_dir: "/utmstack/events-engine-workdir/pipeline"
```

Then run:

```bash
sudo ./utmstack_migration_tool prepare-agents --config config.yaml
sudo ./utmstack_migration_tool migrate --config config.yaml            # config, users, integrations, agents
sudo ./utmstack_migration_tool migrate --config config.yaml --with-data  # + logs, alerts, statistics
```

The tool exports from V11, imports into the existing V12, and copies the
security key over so all encrypted values stay decryptable. V11 is left
untouched. After it finishes, restart the V12 stack so it picks up the
imported data:

```bash
ssh utmstack@your-v12-server
sudo docker stack deploy -c compose.yml utmstack
```

---

## Bonus — Export and import separately

If you want to back up V11 today and restore it later (or onto multiple V12
servers):

```bash
# On the V11 side — save the data to a file
sudo ./utmstack_migration_tool export --config config.yaml --output backup.yml

# Later, on the V12 side — load that file
sudo ./utmstack_migration_tool import --config config.yaml --input backup.yml
```

Both commands read the same `config.yaml` as the modes above: `export` uses
`source`, `import` uses `destination`.

---

## What migrates

| What | Notes |
|------|-------|
| Users and roles | Password hashes are copied verbatim — logins keep working. |
| Application configuration | Copied verbatim; the security key travels with it, so stored passwords stay decryptable. |
| Integration configurations | Only the values you configured, written to V12 YAML files under the destination's `integrations_config_dir`. V12 ships the integration catalog itself. |
| Agents and collectors | Re-registered on the destination so they keep working. |
| Agent connection key | Copied over, so existing agents keep enrolling without re-provisioning. |
| Internal encryption key | Written to the destination's `/root/utmstack.yml`, to the live `compose.yml` (`ENCRYPTION_KEY`/`INTERNAL_KEY` on each affected service), and to the events-engine pipeline YAML the plugins read. A `docker stack deploy` propagates the compose env, and the event-processor services are force-updated afterwards so they reload the key cached from the pipeline YAML — without that, the V12 plugin would keep using the old key and V11-encrypted credentials would not decrypt. No-op if the destination already carries the same key. |
| Search data (logs, alerts, statistics) | With `--with-data`, OpenSearch tenant data streams straight into the V12 ClickHouse — no intermediate file. Migrated rows are normalised to the V12 default tenant, since V11 had no multi-tenancy. |

## Not migrated

These are intentionally left behind — V12 provides or re-discovers them:

- The **integration catalog** (V12 seeds it itself)
- **Dashboards and visualizations** (V12 provisions them from its filesystem)
- **Assets, data sources, and network-scan results** (re-discovered by V12 scans)
- **Correlation rules and logstash filters** (V12 provisions them from its filesystem)

---

## A few things to know

- **Always run `prepare-agents` first.** The `upgrade` and `migrate` commands
  refuse to start without it — that's on purpose, so your agents don't get
  left behind on V11.
- **`upgrade` is destructive.** V11 is removed and replaced by V12. There's no
  automatic rollback.
- **Offline agents?** During `prepare-agents` you can press **Ctrl+C** and
  accept skipping them. You'll need to install the migration agent on those
  machines manually later.
- **Resume is automatic.** If `prepare-agents` is interrupted, just run it
  again — it picks up where it left off.
- **PostgreSQL credentials are auto-detected** from `compose.yml` when you
  don't set `password` — the only required config fields are `host`,
  `ssh_user`, and `ssh_password` or `ssh_private_key`.

---

## Troubleshooting

A few things that can trip you up after a migration:

### The panel stays stuck on "Welcome to UTMStack"

When the migration finishes, open the panel in your browser. If it never gets
past the **"Welcome to UTMStack"** loading screen, restart the frontend
container:

```bash
sudo docker service update --force utmstack_frontend
```

Give it a minute and reload the page.

### Mode C — keep the same IP / DNS as your old V11 server

If you migrated to a **brand-new V12 server** (Mode C), the new instance
**must end up reachable on the same IP address or DNS name** that your old
V11 server used. Reason: every agent out there is hardcoded to talk to that
address — change it and they all go silent.

Not sure what address the agents are using? On the **old V11 instance**, go
to the **Integrations** page and look at the **Linux agent install command** —
the IP or domain in that command is what your agents expect. Make sure the
new V12 server answers on that exact address (swap IPs, update DNS, etc.)
before pointing users at it.

### Back up your certificates before upgrading

The upgrade **regenerates the TLS certificates** under `/utmstack/cert/`.
If you're using custom certs (or just want to keep the existing ones), copy
them somewhere safe **before** running `upgrade`:

```bash
sudo cp -r /utmstack/cert/ ~/utmstack-cert-backup/
```

After the upgrade, put them back in `/utmstack/cert/` and restart the stack.

> Note: in **Mode C** (server-to-server) the certificates are **not** copied
> over either — the new V12 server will have its own fresh certs. Same fix:
> copy them from the old server and drop them into `/utmstack/cert/` on the
> new one.

For the full list of flags, error messages, and edge cases, see the
**[full user guide](migration-tool/docs/USER_GUIDE.md)** (also available in
**[Spanish](migration-tool/docs/USER_GUIDE.es.md)**).

---

## Need help?

- Run `sudo ./utmstack_migration_tool --help` (or `<command> --help`) for the
  full list of options.
- Open an issue at https://github.com/utmstack/MigrationTool/issues if
  something doesn't work.
