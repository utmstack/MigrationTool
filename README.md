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

## Known issues

A few things that can trip you up after a migration — and how to fix them.

### The panel stays stuck on "Welcome to UTMStack"

When the migration finishes, open the panel in your browser. If it never gets
past the **"Welcome to UTMStack"** loading screen, restart the frontend
container:

```bash
sudo docker service update --force utmstack_frontend
```

Give it a minute and reload the page.

### Agents fail to install with "invalid connection key"

Old connection keys don't survive the migration. If agents won't install and
you see **`invalid connection key`** in the logs, rotate the key from the
panel:

```
https://<your-instance>/app-management/settings/connection-key
```

Generate a new key there, then retry the agent installation with the new key.

### Mode C — keep the same IP / DNS as your old V10 server

If you migrated to a **brand-new V11 server** (Mode C), the new instance
**must end up reachable on the same IP address or DNS name** that your old
V10 server used. Reason: every agent out there is hardcoded to talk to that
address — change it and they all go silent.

Not sure what address the agents are using? On the **old V10 instance**, go
to the **Integrations** page and look at the **Linux agent install command** —
the IP or domain in that command is what your agents expect. Make sure the
new V11 server answers on that exact address (swap IPs, update DNS, etc.)
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
> over either — the new V11 server will have its own fresh certs. Same fix:
> copy them from the old server and drop them into `/utmstack/cert/` on the
> new one.

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
