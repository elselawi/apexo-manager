# Configuration Reference

Configuration has two layers:

1. **Bootstrap configuration** — root-owned JSON read at startup. It controls where the database and data live and how the HTTP listener behaves. Changes require a service restart and are made with the CLI, not the web UI.
2. **Server settings** — editable in the dashboard and stored in SQLite. They control instance paths, releases, Caddy, ports, backups, monitoring, and discovery roots.

## Bootstrap configuration

Default path: `/etc/pbmanager/config.json`. Default data directory: `/var/lib/pbmanager`.

```json
{
  "data_dir": "/var/lib/pbmanager",
  "db_path": "/var/lib/pbmanager/manager.db",
  "listen_addr": "127.0.0.1:8787",
  "public_url": "https://manager.example.com",
  "secure_cookies": true,
  "log_level": "info",
  "session_hours": 12,
  "trust_proxy": true
}
```

| Field | Default | Meaning |
| --- | --- | --- |
| `data_dir` | `/var/lib/pbmanager` | Manager state: database, secret key, worker lock. Created 0700. |
| `db_path` | `<data_dir>/manager.db` | SQLite database. Must be inside the data directory unless explicitly absolute. |
| `listen_addr` | `127.0.0.1:8787` | HTTP bind address. Non-loopback binds are rejected unless explicitly allowed. Keep it loopback and use Caddy. |
| `public_url` | `http://127.0.0.1:8787` | Browser-facing base URL. Used for cookie security and redirects. |
| `secure_cookies` | derived from `https://` public URL | Forces `Secure` cookies. Keep true in production. |
| `log_level` | `info` | `debug`, `info`, `warn`, `error`. |
| `session_hours` | `12` | Session lifetime; 1–168. |
| `trust_proxy` | `true` | Trust `X-Forwarded-For`/proto only when the peer is loopback or a private address. |

CLI:

```bash
pbmanager config init --path /etc/pbmanager/config.json \
  --data-dir /var/lib/pbmanager --listen 127.0.0.1:8787 \
  --public-url https://manager.example.com
pbmanager --config /etc/pbmanager/config.json config show
pbmanager --config /etc/pbmanager/config.json config validate
pbmanager --config /etc/pbmanager/config.json config set \
  --public-url https://manager.example.com --secure-cookies=true
```

`config set` updates only the supplied bootstrap fields, preserves `data_dir` and `db_path`, writes atomically with 0600 permissions, and prints `restartRequired`. Restart the service to apply it.

The bootstrap config must be owned by root and not writable by group/others. The manager validates this and warns on unsafe permissions.

## Server settings

Stored in `server_settings` and editable from **Server configuration**. The settings API rejects invalid paths and unsafe templates.

| Setting | Default | Notes |
| --- | --- | --- |
| `installRoot` | `/opt` | Default parent for new instances. Writable by root only. |
| `archiveTemplate` | `pocketbase_{version}_linux_{arch}.zip` | Must produce a single path segment. |
| `downloadUrlTemplate` | GitHub release URL | Supports `{version}` and `{arch}`. HTTPS required except loopback mirrors. |
| `checksumUrlTemplate` | GitHub `checksums.txt` | Parsed for the archive filename. Loopback HTTP allowed for test mirrors. |
| `defaultPbVersion` | `0.23.6` | Initial version in the create wizard. Pinned official checksums for 0.23.6 exist. |
| `pbDataDirTemplate` | `{base_dir}/pb_data` | Must stay inside the base directory. |
| `serviceDir` | `/etc/systemd/system` | Must exist. Only manager-marked unit files are overwritten. |
| `caddyfilePath` | `/etc/caddy/Caddyfile` | Never regenerated. Managed import may be added. |
| `caddySnippetDir` | `/etc/caddy/pbmanager.d` | Only this directory is written. |
| `caddyService` | `caddy` | systemd service name. |
| `caddyBinary` | `caddy` | Executable name or absolute path. |
| `systemctlBinary` | `systemctl` | Argument-only invocation. |
| `journalctlBinary` | `journalctl` | Used for logs. |
| `backupDir` | `/var/lib/pbmanager/backups` | Root-only 0700. |
| `discoveryRoots` | `/opt`, `/srv`, `/var/www` | Read-only filesystem scan roots. |
| `publicIpv4` | empty | Used for Cloudflare DNS verification; optional. |
| `portStart` / `portEnd` | `8090` / `8199` | Free port selection range. |
| `serviceUserPrefix` | `pb` | Creates `pb-<instance>`. |
| `monitorIntervalSeconds` | `60` | 15–3600. Low frequency by design. |
| `jobRetentionDays` | `30` | Completed/failed job pruning. |
| `auditRetentionDays` | `180` | Audit pruning. |
| `backupRetentionDefault` | `7` | Default for new/imported instances. |
| `verifyHttps` | `true` | HTTPS and certificate checks after domain attach. |
| `defaultCaddyEnabled` | `true` | Default for the create wizard. |
| `defaultDnsEnabled` | `false` | Default for the create wizard. |
| `requireChecksums` | `true` | Refuse unverified release downloads. |

### Templates

`{version}`, `{arch}`, and `{base_dir}` are expanded. Unknown placeholders are rejected. Templates may not traverse directories or contain absolute paths where only a filename is expected.

### Legacy release checksums

PocketBase did not publish `checksums.txt` for older releases. The manager pins official SHA-256 digests for `0.23.6` on `linux/amd64`, `linux/arm64`, and `linux/armv7`, obtained from the official GitHub release assets over TLS. Newer releases use release metadata (`digest`) or `checksums.txt`.

## Instance inventory settings

Each instance stores:

| Field | Meaning |
| --- | --- |
| `name` | Stable manager identifier; lowercase DNS-like. |
| `hostname` | Display/host label. |
| `baseDir` | Installation root. |
| `binaryPath` | PocketBase executable. |
| `dataDir` | `pb_data` directory. |
| `port` | Loopback HTTP port. |
| `serviceName`, `serviceFile` | systemd identity. |
| `runAsUser` | Unprivileged account for managed instances. |
| `pbVersion` | Installed version. |
| `managed` | True only for provisioned instances. |
| `source` | `provisioned`, `cli-import`, `discovery`, `manual`. |
| `notes` | Free-form operator notes. |
| `caddyMode` | `unmanaged` or `managed`. |
| `backupEnabled`, `backupCron`, `backupRetention`, `nextBackupAt` | Schedule. |
| `cloudflareAccountId`, `smtpProfileId`, `storageProfileId` | Profile assignments. |
| `pbSuperuserEmail` / encrypted secret / status | Optional application linkage. |
| `pbSuperuserManaged` | True when the manager owns and auto-heals the account. |
| `pbLastVerifiedAt`, `pbLastRepairedAt`, `pbReconcileError` | Managed-credential watchdog state. |

Imported instances default to `managed: false`, `caddyMode: unmanaged`, backups disabled but with a valid cron and retention so a schedule can be enabled.

## Cron expressions

Five fields: minute hour day-of-month month day-of-week.

- UTC.
- `*`, lists, ranges, steps.
- Standard OR semantics when both day fields are restricted.
- Example: `15 4 * * *` = 04:15 UTC daily.

## Reverse proxy

Recommended Caddyfile:

```caddyfile
{
	admin localhost:2019
	email ops@example.com
}

import /etc/caddy/pbmanager.d/*.caddy
```

The manager adds the import automatically if missing and validates first. It never edits existing site blocks.

If you terminate TLS elsewhere, set `public_url` to the external HTTPS URL and `secure_cookies` true, and ensure `X-Forwarded-Proto` is set. Because `trust_proxy` only trusts loopback/private peers, put the proxy on the same host or a private network.

## Environment variables

| Variable | Purpose |
| --- | --- |
| `PBMANAGER_ADMIN_PASSWORD` | Optional non-interactive first-owner bootstrap for the installer. |
| `GO_BIN`, `NPM_BIN`, `OUT`, `VERSION` | Build script overrides. |
| `PBMANAGER_CADDY_BIN` | Test-only override for real Caddy validation. |

No secrets are read from the environment at runtime except the first-owner installer input. Runtime secrets live in the encrypted database.

## File permissions

| Path | Mode |
| --- | --- |
| Data directory | 0700 root |
| Manager DB | 0600 root |
| `secret.key` | 0600 root |
| Backups | 0700 root; files 0600 |
| Bootstrap config | 0600 root |
| Managed unit | 0644 root |
| Caddy snippets | 0644/0640 as required by Caddy, root-owned |
| Instance data | instance user, typically 0700/0750 |

The process runs with `umask 0077`.
