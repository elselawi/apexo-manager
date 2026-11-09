# Architecture

## Design goals

The manager is a single-node control plane, not a deployment orchestrator. It assumes one Linux host with systemd, an optional Caddy service, and any number of PocketBase installations under paths the operator controls. It is deliberately conservative: it reads more than it writes, prefers dedicated files over shared files, and keeps an audit trail.

## Process model

One `pbmanager` process serves the API and dashboard, runs the scheduler, and consumes durable jobs. The process is managed by systemd as root because it creates system users, writes units under `/etc/systemd/system`, reads instance data under `/opt`, and manages Caddy under `/etc/caddy`.

The HTTP listener is loopback-only by default. Caddy terminates TLS and proxies to it. The manager never exposes the Caddy admin API.

A root-owned `flock` at `<dataDir>/worker.lock` ensures exactly one process consumes jobs. `serve` acquires it at startup. CLI commands that need a job try to acquire it; if the daemon owns it, the CLI waits for the daemon's worker instead of starting a duplicate.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/pbmanager` | Entrypoint, umask 0077, version injection. |
| `internal/cli` | Root commands, recovery, interactive confirmations, local worker coordination. |
| `internal/api` | HTTP routes, auth/CSRF/rate-limit middleware, handlers, security headers, SPA serving. |
| `internal/app` | Shared wiring, job handler registration, scheduler, monitoring loop, startup recovery. |
| `internal/auth` | Argon2id hashing, password policy, sessions, roles, last-owner protection, login limiter. |
| `internal/config` | Bootstrap JSON (data dir, DB path, listen address, public URL, cookie policy). |
| `internal/store` | SQLite migrations and typed repositories. WAL, foreign keys, busy timeout, immediate transactions. |
| `internal/jobs` | Durable queue, claims, retries, backoff, cancellation, checkpoints, recovery. |
| `internal/secret` | AES-256-GCM secret box with purpose binding and 0600 key. |
| `internal/platform` | Argument-only process execution, path validation, atomic writes, copies, backups. |
| `internal/settings` | Editable server settings stored in the database and validated independently. |
| `internal/services/backup` | Snapshot/native backups, archive validation, restore journal, retention. |
| `internal/services/caddy` | Conservative parser, snippet management, staged validation, reload/rollback, logs. |
| `internal/services/cloudflare` | Token verification, zones, DNS CRUD, ownership markers. |
| `internal/services/dns` | Manager-owned A record reconciliation with conflict refusal. |
| `internal/services/discovery` | Read-only unit/process/filesystem/Caddy discovery. |
| `internal/services/instances` | Validation, provisioning, update, delete, settings, domains, credentials. |
| `internal/services/monitor` | Low-frequency host/instance CPU, memory, disk, port, health, TLS samples. |
| `internal/services/pocketbase` | PocketBase API client, verified release downloads, safe archive extraction. |
| `internal/services/smtp` | SMTP connectivity/authentication tests. |
| `internal/services/storage` | SigV4 S3 `ListObjectsV2` probe. |
| `internal/services/systemd` | Status/actions/logs plus managed unit rendering and validation. |

## Data model

SQLite tables:

- `admins`, `sessions`
- `server_settings`
- `cloudflare_accounts`, `smtp_profiles`, `storage_profiles`
- `instances`, `domains`
- `jobs`, `job_steps`
- `backups`
- `audit_logs`
- `server_metrics`, `instance_metrics`

Migrations are embedded and applied under a transaction with a schema version row. Reopening the database does not reapply migrations.

Important constraints:

- Instance name, hostname, and port are unique.
- Domain hostnames are unique and belong to one instance.
- Foreign keys cascade where ownership requires it; profile deletion clears assignments through explicit updates.
- Jobs support an optional idempotency key.
- Backup paths are stored only inside the configured backup root; secrets are never stored in plaintext.

## Authentication and authorization

- Passwords: Argon2id, 64 MiB, 3 iterations, 2 lanes, 16-byte salt, 32-byte key. Parameters and salt are encoded in the PHC string.
- Sessions: random 32-byte token, only its SHA-256 hash is stored. Cookie is HttpOnly, Secure when the public URL is HTTPS, SameSite=Strict, path `/`, with a server-side expiry.
- CSRF: random token stored with the session. Mutating API requests must send `X-CSRF-Token`. Login is protected by Origin checking and SameSite cookies.
- Roles:
  - `owner`: all actions, administrator management.
  - `admin`: infrastructure and profile changes, no administrator management.
  - `viewer`: read-only.
- Only an effective UID of 0 can run mutating CLI commands. The username is never trusted for privilege.
- Login attempts are limited per client IP. API reads/writes have separate burst limits.
- Last owner cannot be demoted, disabled, or deleted. Disabling or resetting revokes sessions.

## Provisioning state machine

The durable job contains ordered steps:

1. `validate`: name, paths, port, service ownership, disk/parent permissions.
2. `system-user`: create `pb-<name>` if requested.
3. `directories`: create base/data directories, ownership marker.
4. `download`: download release, verify SHA-256 from release metadata/pinned legacy digest, safe ZIP extraction.
5. `install`: atomically install the binary, preserving `.previous`.
6. `systemd`: render a managed unit, validate with `systemd-analyze verify`, install, enable, reload.
7. `start`: start and wait for `/api/health`.
8. `superuser`: stop briefly, run PocketBase's supported `superuser upsert` as the instance user, start, authenticate, and store the generated password encrypted as a managed credential.
9. `caddy` (optional): create a managed snippet and reload.
10. `dns` (optional): create a manager-owned A record.
11. `verify` (optional): wait for health, DNS propagation, HTTPS and certificate.
12. `finalize`: persist runtime state and version.

Each step is checkpointed. A retry skips completed steps. A crash requeues the running job and resets the running step to pending. Provisioning refuses to overwrite unmanaged directories, unit files, or files without the manager marker.

## Managed PocketBase superusers

A provisioned instance gets a dedicated manager-owned superuser. Imported instances can link an existing account or create a managed one from the instance page.

The manager keeps the account durable and encrypted, and a watchdog verifies it every two minutes for running instances:

1. Check `/api/health`; an unhealthy service is not treated as credential drift.
2. Authenticate with the stored account.
3. On success, record the record ID and last-verified time.
4. On two consecutive auth failures, queue a serialized repair job.
5. The repair deletes the managed email, creates a new random password through `superuser upsert`, starts the service, authenticates, and stores the new encrypted password.
6. If the old record ID still exists under a renamed email, the manager resolves its current email through the authenticated superuser list and removes it through the official CLI.
7. A journal makes interrupted creates/repairs recoverable at startup.

This is intentionally a manager-owned account. An operator should not manually rotate or delete it unless they first use **Stop managing**. A manually linked credential, by contrast, is never auto-healed.

## Self-update

The manager can update itself from official GitHub releases:

1. `internal/services/update` requests the latest non-draft, non-prerelease release for `elselawi/apexo-manager`.
2. It selects the asset for `linux/<arch>`, obtains a SHA-256 from the asset digest or `checksums.txt`, and reports release notes.
3. A `manager.update` job downloads the asset to `<dataDir>/updates`, enforces HTTPS (loopback HTTP is allowed only for tests), and verifies the hash.
4. `systemd-run` starts a transient `oneshot` unit running the **staged new binary** with `self-upgrade`.
5. The detached helper verifies the staged binary’s version and hash, preserves the current binary as `.previous`, atomically installs the replacement, restarts `pbmanager`, and polls the loopback health endpoint.
6. On failure it restores `.previous`, restarts, and writes `updates/last-result.json`.

The API exposes only a typed check/install endpoint; it cannot pass an arbitrary URL or shell command. The release repository is fixed at build time and release assets are independently hash-verified.

## Job engine

- Atomic claim in an immediate SQLite transaction.
- Priority + `available_at` ordering.
- Bounded attempts (default 4) with exponential backoff capped at 15 minutes.
- Retryable errors use `jobs.Retryable`/`RetryableAfter`; permanent errors fail immediately.
- Cancellation requests set a flag and cancel the running context; the job becomes `canceled`.
- Steps store status, output, attempt, and timestamps. Output is used as a checkpoint.
- Startup recovery:
  - Restore journals are rolled back before jobs are claimed.
  - Running jobs become queued, `recovered_count` increments, running steps reset to pending.
- Old jobs, steps, and audit rows are pruned according to retention.

## Backups

Two methods:

### Native bundle (`pocketbase_native`)

1. Authenticate with the linked PocketBase superuser.
2. Call the documented `/api/backups` create endpoint.
3. Wait for completion, obtain a file token, and download the database-only official backup.
4. Verify the ZIP, extract `data.db` and `auxiliary.db`.
5. Build a manager `.tar.gz` containing `data/data.db`, `data/auxiliary.db`, uploads, the systemd unit, Caddy snippets, instance metadata, and a manifest with the database SHA-256.
6. Verify the resulting tar and mark it `verified`.
7. Remove the temporary backup from PocketBase.

The official API supplies a consistent database; the manager adds uploads and configuration because PocketBase's backup artifact contains only database files.

### SQLite snapshot (`sqlite_snapshot`)

Used when no superuser is linked or the native API fails:

1. Open the live SQLite database read-only.
2. `VACUUM INTO` a temporary snapshot. The live DB is never copied while files are in an unsafe state.
3. Add uploads and configuration to a manifest and tar.
4. Validate the archive and mark it `verified`.

### Retention and schedules

Instances store `backup_enabled`, a five-field UTC cron expression, retention count, and `next_backup_at`. The scheduler enqueues due backups with an idempotency key based on the instance and scheduled minute. Successful runs update last/next timestamps; retention prunes old verified artifacts and records.

### Restore

1. Verify the backup status, path containment, SHA-256, manifest, SQLite integrity, and archive paths.
2. Require the exact instance-name confirmation.
3. Create a safety backup of the current state.
4. Write a restore journal.
5. Stop and wait for the service.
6. Move the current data directory aside.
7. Extract into a sibling staging directory, validate SQLite, and rename atomically.
8. Chown to the instance user, start the service, wait for health.
9. Mark restored and delete the journal.
10. On failure: stop, move failed staging aside, restore the previous data directory, restart, and report.

At startup, any incomplete restore journal is rolled back before the scheduler starts.

## Caddy management

The parser reads the Caddyfile and import directives, identifies top-level sites and reverse proxy upstreams, and marks snippets carrying the manager marker. It does not attempt to rewrite arbitrary Caddy syntax.

Managed change flow:

1. Validate the domain and upstream.
2. Refuse to overwrite a snippet without the marker.
3. Build a candidate Caddyfile by adding the managed import only if missing.
4. Create a temporary snippet directory containing all existing snippets plus the new one.
5. Substitute only the import path and run `caddy validate`.
6. Back up the Caddyfile and snippets.
7. Atomically write the snippet and Caddyfile, reload Caddy.
8. On write or reload failure, restore the backup and reload again.

Removing a route deletes only the managed snippet and validates/reloads.

## DNS management

Cloudflare tokens are verified with `/user/tokens/verify`, encrypted with an account-purpose AAD, and never returned. Zones are listed through the API. Manager records include a comment marker `pbmanager:<instanceID>` and are the only records the manager updates or deletes. A conflicting non-manager A record is refused rather than overwritten. Apex and subdomain selection uses the longest matching zone.

## Monitoring

A low-frequency loop samples:

- Host CPU, memory, disk, load, uptime.
- Per-instance CPU (from `/proc`), cgroup memory, data directory size, port listening, systemd state, HTTP health.
- Caddy service/config state.
- TLS certificate expiry and HTTPS response for managed domains.
- Backup success age and job failures.

Samples are pruned automatically. Dashboard pages poll a small number of aggregate endpoints; there is no per-second streaming.

## Failure handling

| Failure | Behavior |
| --- | --- |
| Manager crash during job | `flock` releases; systemd restarts; running job is requeued; completed steps are skipped. |
| Manager crash during restore | Journal rollback restores previous data before jobs start. |
| Caddy validation failure | No files changed. |
| Caddy reload failure | Caddyfile/snippets restored and reload attempted. |
| Package download failure | Retryable with bounded backoff. |
| New PocketBase fails health | Update rolls back to `.previous`; provisioning fails with diagnostics. |
| PocketBase superuser creation fails | Provisioning retries within job policy; native backup falls back to SQLite snapshot if unlinked. |
| Database corruption | Root CLI `db check`, `db snapshot`, `db restore`; service remains recoverable. |
| Lost owner password | Root CLI reset/recover; sessions revoked. |

## Known architectural limits

- Single host only; no multi-node scheduling.
- systemd and Linux are required; WSL2 was used for verification.
- Caddy parser handles common site/import/reverse_proxy forms, not every advanced Caddyfile construct.
- Native backup captures uploads while the app is running; the DB is consistent, but a file uploaded during the copy may differ by seconds. The fallback snapshot has the same property.
- Cloudflare management uses API tokens, not OAuth.
- No Docker deployment path by design.
