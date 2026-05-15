# UTMStack Migration Tool

A small program that migrates your **UTMStack V10** installation to **V11**.

It can do **three things**, depending on what you need:

| If you want to... | Use this |
|-------------------|----------|
| Upgrade V10 → V11 **on the same server** | [Mode A — In-place upgrade](#mode-a--in-place-upgrade-same-server) |
| Manage the upgrade **from a different machine** (over SSH) | [Mode B — Remote upgrade over SSH](#mode-b--remote-upgrade-over-ssh) |
| **Move** your data to a **brand-new V11 server** | [Mode C — Server-to-server migration](#mode-c--server-to-server-migration) |

> Not sure? **Mode A is what most people want.** Pick that one.

---

## First — download the tool

Run this on whichever machine you'll use to launch the tool (the V10 server
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

You are sitting on the V10 server and want to upgrade it to V11 in place.
**No config file needed.**

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

You want to upgrade a V10 server from your laptop (or another machine).
**You need a small config file with the SSH credentials.**

Create `config.yaml`:

```yaml
source:
  host: "192.168.1.100"          # your V10 server IP
  ssh_user: "utmstack"
  ssh_password: "your-ssh-password"
  # or use a key:
  # ssh_private_key: "/home/me/.ssh/id_rsa"
```

Then run:

```bash
sudo ./utmstack_migration_tool prepare-agents --config config.yaml
sudo ./utmstack_migration_tool upgrade --config config.yaml
```

Same idea as Mode A, but the tool reaches the V10 server over SSH.

---

## Mode C — Server-to-server migration

You want to move your V10 data to a **separate V11 server** **without
touching V10**. Useful when migrating to new hardware.

> ⚠️ **Important — install V11 yourself first.**
> This mode does **not** install V11 on the destination. You must have a
> working UTMStack **V11** installation already running on the destination
> server (Docker stack up, `utmstack_postgres` service running) before you
> launch `migrate`. The tool only **moves the data over**.

Create `config.yaml` with **both** servers:

```yaml
source:
  host: "192.168.1.100"          # your V10 server
  ssh_user: "utmstack"
  ssh_password: "v10-ssh-password"

destination:
  host: "192.168.1.200"          # your new V11 server
  ssh_user: "utmstack"
  ssh_password: "v11-ssh-password"
```

Then run:

```bash
sudo ./utmstack_migration_tool prepare-agents --config config.yaml
sudo ./utmstack_migration_tool migrate --config config.yaml
```

The tool exports V10, imports into the existing V11, and syncs the security
key between both servers. V10 is left untouched. After it finishes, restart
the V11 stack so it picks up the imported data:

```bash
ssh utmstack@your-v11-server
sudo docker stack deploy -c compose.yml utmstack
```

---

## Bonus — Export and import separately

If you want to back up V10 today and restore it later (or onto multiple V11
servers):

```bash
# On the V10 side — save the data to a file
sudo ./utmstack_migration_tool export --config config.yaml --output backup.yml

# Later, on the V11 side — load that file
sudo ./utmstack_migration_tool import --config config.yaml --input backup.yml
```

---

## A few things to know

- **Always run `prepare-agents` first.** The `upgrade` and `migrate` commands
  refuse to start without it — that's on purpose, so your agents don't get
  left behind on V10.
- **`upgrade` is destructive.** V10 is removed and replaced by V11. There's no
  automatic rollback.
- **Offline agents?** During `prepare-agents` you can press **Ctrl+C** and
  accept skipping them. You'll need to install the migration agent on those
  machines manually later.
- **Resume is automatic.** If `prepare-agents` is interrupted, just run it
  again — it picks up where it left off.

---

## Full documentation

For all flags, troubleshooting, the full config file reference, and edge
cases, see the **[full user guide](migration-tool/docs/USER_GUIDE.md)**
(also available in
**[Spanish](migration-tool/docs/USER_GUIDE.es.md)**).

---

## Need help?

- Run `sudo ./utmstack_migration_tool --help` (or `<command> --help`) for the
  full list of options.
- Open an issue at https://github.com/utmstack/MigrationTool/issues if
  something doesn't work.
