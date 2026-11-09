# Deployment

Target: a Linux server with systemd, root access, and existing PocketBase installations you do not want to disturb.

## Prerequisites

- Linux with systemd (tested on Ubuntu 24.04).
- Root or sudo.
- Caddy (recommended) with a reachable public hostname for the dashboard.
- Optional: Cloudflare API token, SMTP credentials, S3-compatible credentials.
- Port 443/80 available for Caddy. PocketBase instances listen on loopback ports.
- No Node.js, Docker, or database server is required in production.

## 1. Build

On a build machine with Go 1.24+ and Node.js 20+:

```bash
git clone <your-repo> pbmanager
cd pbmanager
./scripts/build.sh
```

Output:

- `dist/pbmanager` — static Linux binary with embedded UI.
- `dist/pbmanager.sha256`.

The build runs the Go test suite. For reproducible release artifacts, record the version and checksum:

```bash
VERSION=1.0.0 ./scripts/build.sh
sha256sum dist/pbmanager
```

## 2. Prepare Caddy

If Caddy is not installed, install it from your distribution or the official static binary. A minimal configuration:

```caddyfile
{
	admin localhost:2019
	email ops@example.com
}
```

The Caddy admin endpoint must not be exposed publicly. `localhost:2019` is fine. If you prefer `admin off`, use `systemctl restart caddy` instead of the manager's reload fallback; the manager first tries `systemctl reload caddy`, which works with an admin endpoint.

Do not add the dashboard domain manually if you pass `--domain` to the installer; the installer creates a managed snippet. If you add it manually, use `--skip-caddy`.

Verify:

```bash
caddy validate --config /etc/caddy/Caddyfile
systemctl enable --now caddy
```

## 3. Install the manager

```bash
sudo ./scripts/install.sh \
  --binary ./dist/pbmanager \
  --domain manager.example.com \
  --public-url https://manager.example.com \
  --admin-username admin
```

Options:

| Option | Meaning |
| --- | --- |
| `--binary PATH` | Binary to install; defaults to `dist/pbmanager`. |
| `--listen HOST:PORT` | Loopback listener; defaults to `127.0.0.1:8787`. |
| `--public-url URL` | Browser URL. Required if it differs from the domain. |
| `--domain DOMAIN` | Adds a managed Caddy route. |
| `--admin-username NAME` | Bootstraps the first owner. |
| `--skip-caddy` | Do not configure the dashboard route. |

The installer:

- Backs up prior manager config/unit/binary to `/var/backups/pbmanager/install-<timestamp>/`.
- Refuses to overwrite an unmanaged `pbmanager.service`.
- Creates `/var/lib/pbmanager` 0700.
- Writes `/etc/pbmanager/config.json` with secure cookies when the public URL is HTTPS.
- Installs and enables the unit.
- Waits for `/healthz`.
- Creates the first owner only if none exists.
- Adds the Caddy import and managed dashboard route if requested.

No default password exists. If no admin is created, the installer prints the command to run.

## 4. Verify

```bash
systemctl status pbmanager --no-pager
curl -sS http://127.0.0.1:8787/healthz
curl -k -sS https://manager.example.com/healthz
sudo pbmanager --config /etc/pbmanager/config.json status
sudo pbmanager --config /etc/pbmanager/config.json config validate
```

Open the dashboard and sign in. Check **Monitoring → Platform status** for Caddy, managed import, and database health.

## 5. Add existing installations

Use **Instances → Discover** first. Discovery is read-only. Review each candidate:

- systemd unit path, service name, run-as user
- binary and data directory
- port
- whether the process is running
- whether it is already tracked

Import:

```bash
sudo pbmanager --config /etc/pbmanager/config.json instances import \
  --name clinic-a \
  --hostname clinic-a \
  --base-dir /opt/clinic-a \
  --binary /opt/clinic-a/pocketbase \
  --data-dir /opt/clinic-a/pb_data \
  --port 8090 \
  --service clinic-a \
  --service-file /etc/systemd/system/clinic-a.service \
  --run-as pb-clinic-a \
  --version 0.23.6
```

Import only writes manager inventory. It does not start, stop, rewrite, chown, or back up the app. Link a PocketBase superuser from the instance page later to enable native backups and SMTP/S3 application.

## 6. Provision a new instance

From the dashboard or CLI:

```bash
sudo pbmanager --config /etc/pbmanager/config.json instances create \
  --name clinic-b \
  --hostname clinic-b \
  --version 0.23.6 \
  --port 8091 \
  --base-dir /opt/clinic-b \
  --service-name clinic-b \
  --service-file /etc/systemd/system/clinic-b.service \
  --run-as pb-clinic-b \
  --backup --backup-cron "15 4 * * *" --backup-retention 7
```

Add `--domain app.example.com --cloudflare-account <id> --manage-caddy --manage-dns` to create managed DNS and Caddy records. Cloudflare and Caddy profiles must exist first.

The job creates a generated PocketBase superuser and stores it encrypted. The password is never displayed.

## Firewall

- Allow 80/443 for Caddy.
- Keep PocketBase ports and `127.0.0.1:8787` private.
- Keep the Caddy admin endpoint on loopback.
- Do not expose SQLite or the manager data directory.

Example UFW:

```bash
ufw allow 80/tcp
ufw allow 443/tcp
ufw default deny incoming
ufw enable
```

## systemd behavior

The unit is `Type=simple`, runs as root, restarts on failure, and stops gracefully. `KillMode=mixed` sends the stop signal to the main process; the manager cancels jobs and closes the database.

Boot behavior:

```bash
systemctl is-enabled pbmanager
systemctl is-enabled caddy
```

Both should be `enabled`.

## Existing Caddy configuration

The manager never rewrites site blocks. It will add exactly one line if missing:

```caddyfile
import /etc/caddy/pbmanager.d/*.caddy
```

Before writing, it:

1. Copies the Caddyfile and snippets.
2. Validates a staged configuration with the real Caddy binary.
3. Writes the snippet and Caddyfile atomically.
4. Reloads systemd Caddy.
5. Restores the backup and reloads again if anything fails.

Managed snippets carry a marker. Files without the marker are never overwritten or deleted.

## Unattended install

```bash
export PBMANAGER_ADMIN_PASSWORD='use-a-secret-manager-in-real-life'
sudo -E ./scripts/install.sh \
  --domain manager.example.com \
  --admin-username admin
unset PBMANAGER_ADMIN_PASSWORD
```

Avoid leaving the password in shell history or process listings. Prefer an interactive install.

## Container note

Docker is intentionally unsupported: the manager must manage host systemd units, host users, host paths, and Caddy. Running it in a container would either require dangerous host mounts and a systemd socket or break its core guarantees.

## WSL2 note

This repository was verified under WSL2 Ubuntu 24.04 with systemd. Windows' loopback port 443 was occupied by another process, so verification used the WSL IP with a host-resolver rule. On a normal Linux server, use the public DNS name directly.

See [TESTING.md](TESTING.md) for the exact deployment evidence.
