# Testing and Verified Evidence

This document records what was actually executed, not just what the code is intended to do.

Environment:

- Windows 11 workspace: `C:\Users\alias\Coding\apexo-manager`.
- Linux test host: WSL2 Ubuntu 24.04.4 LTS, systemd enabled.
- Go 1.27.3 at `/home/alias/tools/go/bin/go`.
- Node/Vite on Windows for the frontend build.
- Caddy 2.9.1.
- PocketBase 0.23.6, official Linux amd64 release.
- Browser screenshots via Chrome + `playwright-core`.

## Automated tests

Command:

```bash
cd /mnt/c/Users/alias/Coding/apexo-manager
/home/alias/tools/go/bin/gofmt -w cmd internal
GOTOOLCHAIN=auto /home/alias/tools/go/bin/go test ./... -count=1
```

Latest run: all packages passing.

Coverage highlights:

| Area | Tests |
| --- | --- |
| Store | Open/migrate/reopen, admin/session lifecycle, instance/domain CRUD, profile assignment clearing, job claim/idempotency/recovery, cascade behavior. |
| Auth | Argon2id round-trip, weak passwords, username/role validation, login limiter block/expiry, sessions, disabled accounts, last-owner protection. |
| Secrets | Seal/open, ciphertext tamper, AAD purpose binding, key permission enforcement, symlink rejection. |
| Platform | Absolute/normalized path validation, containment, parent directory traversal bits, atomic writes, copies, safe names, limited reads. |
| Cron | Next run, ranges/steps, invalid expressions, DOM/DOW OR semantics. |
| Jobs | Success + idempotency, retry then success, permanent failure, crash recovery, cancellation, step checkpoint survival across retries. |
| Caddy parser | Sites, addresses, upstreams, imports, managed markers, import helpers, domain validation. |
| Caddy manager | Apply/remove, reload-failure rollback, refusal to overwrite unmanaged snippets, real `caddy validate`. |
| systemd | Unit rendering, unsafe names/ports/specifiers, name normalization. |
| Cloudflare | Token verification, zones, longest-zone match, record create/delete, ownership markers, auth failure. |
| S3 | Signed `ListObjectsV2` probe, independent SigV4 recomputation, headers/path style, access denied, remote HTTP rejection. |
| SMTP | Fake SMTP server, PLAIN auth success/failure, remote plaintext rejection. |
| PocketBase client | Auth, settings GET/PATCH, SMTP/S3 extraction helpers, health, file token/download. |
| Backup | SQLite snapshot + uploads archive, manager create/verify/prune, traversal rejection, official ZIP inspect/validate, native bundle with uploads, full restore with service stop/start, upload recovery, previous-data retention, wrong confirmation. |
| API | Unauthorized access, health, security headers, login, cookie flags, CSRF enforcement, owner/viewer roles, settings update/audit, invalid credentials. |
| CLI | Flags before/after positionals, equals form, effective-UID root enforcement, `config set` updates bootstrap URL/cookie/listen without changing data paths. |
| Discovery | Unit parsing, PB args, name sanitization, unique names. |
| Settings | Defaults, strict validation, unsafe templates, no implicit data relocation, template expansion. |
| Monitor | Host info sanity, disk free bounds. |
| PocketBase release | Pinned legacy checksums for 0.23.6. |
| Manager update | GitHub release parsing, 404/no-release handling, asset selection, digest/checksums verification, download hash, version comparison, detached `systemd-run` argument construction. |
| Discovery response | API always serializes `items` and `warnings` as arrays; UI handles null defensively. |

A safe end-to-end `self-upgrade` exercise used a disposable `pbmanager-selftest.service` with a tiny health endpoint and a fake staged binary:

- staged hash/version verified
- target replaced atomically
- previous target retained as `.previous`
- service restarted and `/healthz` polled
- `/tmp/pb-self/data/updates/last-result.json` recorded `status: success`
- `version: 9.9.9`

The test service and files were removed afterward.

A real GitHub end-to-end update was then exercised:

- `.github/workflows/release.yml` published `v1.0.2` and `v1.0.3` with amd64/arm64 binaries and `checksums.txt`.
- A manager reporting `1.0.2` checked the live GitHub API and received the `1.0.3` asset name, size, release notes, and SHA-256 `6cf27e200a5698263cc195849ea96cd1fb2c1c50f0ef13906c348b0827821b6d`.
- `POST /api/v1/system/update` queued a `manager.update` job; the asset was downloaded and verified; the detached updater installed 1.0.3, restarted the service, and wrote `last-result.json` with `status: success`.
- The job was recovered on the new 1.0.3 process. Its pre-check recognized the running version was already current and completed successfully instead of scheduling a second installer.
- The 1.0.3 source adds `--no-block` and a pre-check specifically to prevent the duplicate-installer race observed during the 1.0.2 test.

Final health: `{"service":"pbmanager","status":"ok","version":"1.0.3"}`.

Real Caddy validation test:

```bash
PBMANAGER_CADDY_BIN=/home/alias/tools/caddy \
  go test ./internal/services/caddy -run RealCaddy -count=1 -v
```

It runs the actual Caddy 2.9.1 binary against a staged config and passes.

## Production-style deployment on WSL2

Installed with the repository installer:

```bash
PBMANAGER_ADMIN_PASSWORD=... ./scripts/install.sh \
  --binary dist/pbmanager \
  --domain manager.localhost \
  --public-url https://manager.localhost \
  --admin-username admin
```

Observed:

- `/usr/local/bin/pbmanager` installed.
- `/etc/pbmanager/config.json` with `secure_cookies: true`, public URL `https://manager.localhost`.
- `/var/lib/pbmanager` 0700.
- `pbmanager.service` active and enabled.
- Caddy active; managed import and `/etc/caddy/pbmanager.d/manager.localhost.caddy` created.
- `https://manager.localhost/healthz` returned HTTP/2 200 with HSTS, CSP, and other security headers.
- The dashboard rendered through Caddy with a valid internal certificate (browser test ignored the local CA).

Local HTTPS command:

```bash
curl -k -sS https://manager.localhost/healthz
{"service":"pbmanager","status":"ok","version":"1.0.0"}
```

Screenshots in `docs/screenshots/` were taken over this HTTPS deployment.

## Existing PocketBase import

An existing PocketBase 0.23.6 installation was already running as `clinic-a` on `127.0.0.1:18090` under user `pb-clinic-a`.

Discovery was read-only. Import:

```bash
pbmanager --config /etc/pbmanager/config.json instances import \
  --name clinic-a --hostname clinic-a \
  --base-dir /opt/pbm-it/clinic-a \
  --binary /opt/pbm-it/clinic-a/pocketbase \
  --data-dir /opt/pbm-it/clinic-a/pb_data \
  --port 18090 --service clinic-a \
  --service-file /etc/systemd/system/clinic-a.service \
  --run-as pb-clinic-a --version 0.23.6
```

Observed:

- Import succeeded.
- `clinic-a` stayed active and healthy.
- No service restart, file rewrite, chown, Caddy, or DNS change occurred during import.
- Monitor later reported `running / healthy`.

## Unmanaged existing installation import

`clinic-a` was already present, but it had originally been created by an earlier test manager, so it was not independent evidence. A separate installation, `legacy-a`, was created manually outside the manager:

- system user `legacy-pb`
- directory `/srv/legacy-a`
- binary `/srv/legacy-a/pocketbase` (42 MB, official 0.23.6 binary)
- data `/srv/legacy-a/pb_data`
- port `18093`
- unit `/etc/systemd/system/legacy-a.service` with **no pbmanager marker**

Import command:

```bash
pbmanager --config /etc/pbmanager/config.json instances import \
  --name legacy-a --hostname legacy-a \
  --base-dir /srv/legacy-a \
  --binary /srv/legacy-a/pocketbase \
  --data-dir /srv/legacy-a/pb_data \
  --port 18093 --service legacy-a \
  --service-file /etc/systemd/system/legacy-a.service \
  --run-as legacy-pb --version 0.23.6
```

Before/after evidence:

- unit SHA-256 unchanged (`b4b59e5eac259f78372c7a4d39f20b80725ab605b70e859fe38839d47ca05db6`)
- same `MainPID=581`
- same `ActiveEnterTimestamp` (`10:53:51`)
- same unit mtime and size
- same binary mtime and size
- zero `managed by pbmanager` markers in the unit
- service still active; `http://127.0.0.1:18093/api/health` still 200
- manager inventory shows `legacy-a`, `managed:false`, `source:cli-import`
- monitor later reported `running / healthy`

This directly verifies that importing an installation the manager did not create performs no OS-level changes and does not restart or rewrite the app.

## Provisioning

A new instance `clinic-b` was provisioned from the official PocketBase 0.23.6 archive via a local checksum-verified mirror:

```bash
pbmanager instances create --name clinic-b --hostname clinic-b \
  --version 0.23.6 --port 18091 \
  --base-dir /opt/clinic-b --data-dir /opt/clinic-b/pb_data \
  --service-name clinic-b --service-file /etc/systemd/system/clinic-b.service \
  --run-as pb-clinic-b --backup --backup-retention 3 --manage-caddy=false
```

All steps succeeded:

- validation
- dedicated user creation
- directory creation with 0755 parent traversal
- checksum-verified download
- atomic install
- unit validation/install/enable
- health start
- finalize

The manager reported `running / healthy`.

A later `clinic-c` provisioning exercised the new automatic PocketBase superuser step. Observed:

- `superuser` step succeeded.
- `PB auth: linked`.
- Backup method became `pocketbase_native`.
- Instance finalized as `running / healthy`.

## Native backup with uploads

For `clinic-b`, a linked superuser was created with the official CLI, then linked through the API. A marker file was placed in `pb_data/uploads/`.

Backup result:

- method: `pocketbase_native`
- archive listing:
  - `data/data.db` (159744 bytes)
  - `data/auxiliary.db` (24576 bytes)
  - `data/uploads/restore-marker.txt`
  - `config/systemd.service`
  - `config/instance.json`
  - `manifest.json`

The upload marker was deleted, then the backup was restored through the API. Observed:

- restore job succeeded
- upload marker restored with the instance user's ownership
- `clinic-b` active and healthy
- previous data directory retained

Full restore log:

```
restore succeeded Restore completed and instance verified healthy
```

## Crash recovery

A `backup.create` job was inserted as `running` with a running step, simulating a manager that died mid-job. The manager was killed with SIGKILL via systemd. systemd restarted it.

Observed:

- new main PID
- job `3617fc761732469db39401a44b22bd58` recovered (`recoveredCount=1`)
- attempts incremented to 2
- job completed successfully
- a new verified native backup was created

## Graceful shutdown

`systemctl restart pbmanager` produced:

```
Stopping pbmanager.service...
{"level":"INFO","msg":"shutting down"}
Deactivated successfully.
Started pbmanager.service...
```

No 30-second timeout and no orphaned worker.

## CLI enforcement and recovery

- Non-root invocation prints `error: this command must be run as root (or via sudo)` and exits 1 (verified with a script to avoid shell status masking).
- CLI flags are accepted before or after positional arguments.
- `instances update clinic-c --version 0.23.6 --yes` completed and finalized `running / healthy`.
- Delete of `clinic-c` with `--purge-data --yes` stopped/disabled the service, removed the managed unit, archived data, and removed inventory. It did not recursively delete data.
- Recovery was exercised with `pbmanager.service` stopped: `recover` reported a healthy DB, one admin, two instances, and `serviceActive:false`; `recover --reset-password admin` reset the password and revoked sessions via a pty; the service restarted and HTTPS health returned 200.
- `admin reset-password` and `recover` are covered by the same code path; password prompts are terminal-only and never accept command-line secrets.

## Upgrade script

`scripts/upgrade.sh` was run twice:

- took a SQLite snapshot to `/var/backups/pbmanager/upgrade-<timestamp>/`
- preserved binary, config, unit
- stopped, replaced, started, health-checked
- retained the prior binary

Observed manager version changed from `1.0.0` to `1.0.1-test` and then `1.0.2-test`, with instances healthy.

## UI verification

Captured and visually inspected:

- login
- overview
- instance list
- instance detail
- monitoring
- Caddy

The overview and instance detail initially exposed two issues that were fixed:

- disk free/used values were swapped in host info;
- instance detail used a top-level `portListening` that is only present in the metrics object.

The Caddy page initially crashed when the Caddyfile had no top-level site blocks because the API serialized nil slices as `null`; the backend now returns empty slices and the UI also defaults them. The final browser pass reported only the expected pre-login `401` for `/auth/me`; no dashboard render errors remained.

## Installer reconfiguration

The installer was rerun on an existing HTTPS deployment with `--domain manager.localhost --public-url https://manager.localhost`. It:

- preserved the existing database, data directory, admin, and inventory,
- updated `public_url` to the HTTPS URL,
- forced `secure_cookies` to true,
- left `data_dir`, `db_path`, and listen address unchanged,
- restarted the service, passed health checks, and reloaded the existing Caddy route.

Observed config after rerun:

```json
{
  "data_dir": "/var/lib/pbmanager",
  "db_path": "/var/lib/pbmanager/manager.db",
  "listen_addr": "127.0.0.1:8787",
  "public_url": "https://manager.localhost",
  "secure_cookies": true
}
```

`deploy-remote.sh` now rejects a remote invocation without `--domain`/`--public-url`, preventing the loopback-only-default mistake.

- Discovery modal executed in Chrome against the live manager: three tracked candidates, no console errors, no fatal screen. The earlier crash was caused by a nil `warnings` slice serializing as `null`; the backend now returns `[]` and the UI defaults both arrays.
- Settings page manager-update card executed in Chrome against the live manager: current version, GitHub check, and "No releases" state render without errors.

## Ephemeral superuser mode

`clinic-b` was unlinked through `DELETE /instances/{id}/credentials`. Its PocketBase `_superusers` table initially contained only `admin@clinic-b.pbmanager.local`.

A manual `backup create clinic-b` then:

- created a temporary `ephemeral-*@pbmanager.local` superuser through the official CLI,
- completed a native PocketBase backup,
- deleted the temporary superuser,
- left only `admin@clinic-b.pbmanager.local` in `_superusers`,
- left `/var/lib/pbmanager/ephemeral` empty,
- restored `clinic-b` to active/healthy,
- completed in about 3.1 seconds.

The same test was run with `clinic-b` initially stopped. The manager started it for the operation, created/used/deleted the temporary superuser, and **left it stopped afterward**, restoring the original state. It was then started manually for the demo.

A unit test covers journal recovery: an orphan journal causes `Recover` to stop the service, run `superuser delete`, restart the prior running state, and remove the journal. SMTP/S3 apply uses the same `pbauth.With` lifecycle, though a live SMTP/S3 apply was not available.

## What was not tested against real providers

- Cloudflare: tested with a fake API server, not a live token/zone. No token was available.
- SMTP: tested with a local fake SMTP server, not a real relay.
- S3: tested against a local SigV4-compatible fake, not a live bucket.
- DigitalOcean: no credentials/SSH access were available in the environment, so no remote server deployment was performed.
- Full WSL shutdown/reboot was performed: `pbmanager`, `caddy`, `clinic-a`, and `clinic-b` all came back `active` with `enabled`; HTTPS health returned 200 afterward.
- Update rollback on an actual health failure was not induced; the rollback code path and unit tests exist, but the same-version update was a no-op.

These are stated honestly in [LIMITATIONS.md](LIMITATIONS.md).
