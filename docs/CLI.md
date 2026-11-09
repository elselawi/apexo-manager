# CLI Reference

`pbmanager` is both the daemon and the root recovery tool. It verifies the **effective UID**, never a username. Commands that read or mutate server state require root; they fail with exit code 1 otherwise.

```bash
pbmanager [--config /etc/pbmanager/config.json] <command>
```

If `--config` is omitted, `/etc/pbmanager/config.json` is used. The flag can appear before or after positional arguments for subcommands.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success. |
| 1 | Operational or permission failure. |
| 2 | Usage error. |

## Daemon

### `serve`

```bash
pbmanager serve --config /etc/pbmanager/config.json
```

- Requires root unless `--allow-nonroot`.
- Enforces a loopback listen address unless explicitly overridden.
- Acquires `<dataDir>/worker.lock`; only one process consumes jobs.
- Recovers interrupted restores and running jobs before starting the scheduler.
- Runs the HTTP server, monitor, and scheduler.
- Handles SIGINT/SIGTERM, cancels jobs, closes the database, and releases the lock.

## Status and configuration

```bash
pbmanager status [--json]
pbmanager config show
pbmanager config validate
pbmanager config init --path /etc/pbmanager/config.json \
  --data-dir /var/lib/pbmanager --listen 127.0.0.1:8787 \
  --public-url https://manager.example.com
```

`status` reports manager version, database, listeners, running/total instances, and job counts. `config validate` checks paths, permissions, and templates without changing anything.

## Instances

```bash
pbmanager instances list
pbmanager instances inspect NAME_OR_ID
pbmanager instances discover [--json]
pbmanager instances import [flags]
pbmanager instances create [flags]
pbmanager instances start|stop|restart NAME_OR_ID
pbmanager instances update NAME_OR_ID --version VERSION [--checksum SHA] [--yes]
pbmanager instances logs NAME_OR_ID [--lines 200]
pbmanager instances set-credentials NAME_OR_ID --email EMAIL
pbmanager instances delete NAME_OR_ID [--purge-data] [--remove-caddy] [--remove-dns] [--yes]
```

### `instances discover`

Read-only scan of:

- units under the configured service directory
- `ExecStart` commands containing a PocketBase binary
- running processes whose executable basename is PocketBase
- filesystem artifacts under discovery roots
- Caddy upstreams where matching

Output columns show name, service, binary, data dir, port, running, tracked, and domains. `--json` returns evidence and warnings. Discovery never writes.

### `instances import`

```bash
pbmanager instances import \
  --name clinic-a \
  --hostname clinic-a \
  --base-dir /opt/clinic-a \
  --binary /opt/clinic-a/pocketbase \
  --data-dir /opt/clinic-a/pb_data \
  --port 8090 \
  --service clinic-a \
  --service-file /etc/systemd/system/clinic-a.service \
  --run-as pb-clinic-a \
  --version 0.23.6 \
  --domains app.example.com,www.example.com \
  --notes "legacy server"
```

- Validates absolute paths, port, service name, and ownership conflicts.
- Does not touch files, services, Caddy, DNS, or PocketBase data.
- Defaults to unmanaged, no schedule enabled, Caddy unmanaged.
- Audits the action as `cli-root`.

### `instances create`

```bash
pbmanager instances create \
  --name clinic-b \
  --hostname clinic-b \
  --version 0.23.6 \
  --arch amd64 \
  --port 8091 \
  --base-dir /opt/clinic-b \
  --data-dir /opt/clinic-b/pb_data \
  --service-name clinic-b \
  --service-file /etc/systemd/system/clinic-b.service \
  --run-as pb-clinic-b \
  --create-user \
  --domain app.example.com \
  --cloudflare-account <id> \
  --manage-caddy --manage-dns --proxied \
  --backup --backup-cron "0 3 * * *" --backup-retention 7 \
  --checksum <sha256> \
  --notes "new tenant"
```

- Enqueues `instance.provision` and waits for the worker. Provisioning is additive: it refuses to overwrite unmanaged paths or units.
- If the daemon is running, the CLI waits for the daemon's worker. If not, it starts a local worker and releases the lock afterward.
- Output includes the job and the instance ID.
- Creates a generated PocketBase superuser and stores it encrypted.

### `instances update`

```bash
pbmanager instances update clinic-b --version 0.23.7 --yes
```

- Creates a safety backup.
- Downloads and verifies the release.
- Stops, replaces the binary atomically, and starts.
- Verifies the executable version and health.
- On failure, restores the `.previous` binary and restarts.
- Same-version updates are a no-op.

### `instances set-credentials`

```bash
pbmanager instances set-credentials clinic-b --email admin@clinic-b.pbmanager.local
```

Prompts for the PocketBase superuser password without echo, authenticates against the app, encrypts the secret, and links it. Linking is optional; without it, backups use the safe SQLite snapshot and SMTP/S3 cannot be applied.

### `instances delete`

- Creates a final safety backup.
- Stops and disables the service.
- Removes only the manager-marked unit.
- `--purge-data` moves data into the protected purge archive; it never recursively deletes it.
- `--remove-caddy` / `--remove-dns` remove only manager-owned artifacts.
- Requires the exact instance name interactively, or `--yes`.

## Administrators

```bash
pbmanager admin list
pbmanager admin create --username admin --role owner [--display-name "Admin"] [--password-stdin]
pbmanager admin reset-password USERNAME
pbmanager admin disable USERNAME
pbmanager admin enable USERNAME
pbmanager admin delete USERNAME
pbmanager admin revoke-sessions USERNAME
```

- `owner`, `admin`, `viewer` roles.
- The last owner cannot be demoted, disabled, or deleted.
- Password reset revokes sessions and is audited.
- For automation, `--password-stdin` reads one line. Do not pass passwords as arguments.

## Jobs

```bash
pbmanager jobs list [--limit 50] [--status STATUS]
pbmanager jobs inspect JOB_ID
pbmanager jobs retry JOB_ID
pbmanager jobs cancel JOB_ID
```

Jobs are persistent. `retry` requeues failed/canceled jobs with a fresh attempt budget; `cancel` requests cancellation of queued/running work. `inspect` prints the job, timestamps, errors, result, and every step with output.

## Backups

```bash
pbmanager backup create NAME_OR_ID
pbmanager backup list [NAME_OR_ID]
pbmanager backup verify BACKUP_ID
pbmanager backup restore BACKUP_ID [--yes]
```

- Create waits for the job and prints backup ID, method, SHA-256, and size.
- Restore requires the instance name interactively, or `--yes` for automation (still root-only).
- Restore always creates a safety backup, keeps the previous data directory, and verifies health.
- Use the dashboard for per-instance download and restore.

## Recovery and database

```bash
pbmanager recover
pbmanager db check
pbmanager db snapshot /var/backups/pbmanager/manager-$(date +%F).db
pbmanager db restore /var/backups/pbmanager/manager-2026-11-09.db [--yes]
```

### `recover`

Safe workflow for a broken dashboard or forgotten owner:

1. Opens/validates the database.
2. Stops the web service? No — it works while the service is stopped and is intended for that state. If the service is running, recovery still opens the DB safely but may ask you to stop it for some repairs.
3. Creates an owner if none exists, or resets a chosen owner password.
4. Revokes all sessions.
5. Reports service state.

Use:

```bash
systemctl stop pbmanager
pbmanager --config /etc/pbmanager/config.json recover
systemctl start pbmanager
```

### `db snapshot`

Uses SQLite `VACUUM INTO` while the service is running. Safe for backups and upgrades.

### `db restore`

- Refuses if `pbmanager.service` is active.
- Validates a copy of the snapshot.
- Moves the current DB and its `-wal`/`-shm` sidecars aside.
- Copies the snapshot to the DB path with 0600.
- Writes an audit entry in the restored DB.
- Retains the previous database for rollback.

## Diagnostics

```bash
pbmanager diagnose
pbmanager version
```

`diagnose` is read-only: host, kernel, systemd, Caddy, paths, permissions, database integrity, and configured binaries. Do not paste raw output containing private paths into public issues without review.

## Caddy helper

```bash
pbmanager caddy status
pbmanager caddy validate
pbmanager caddy ensure-import
pbmanager caddy route --domain manager.example.com --upstream 127.0.0.1:8787 --name manager
pbmanager caddy logs --lines 200
```

These use the same staged validation and rollback as the dashboard. `route` accepts only loopback upstreams.

## Non-interactive operation

- `--yes` skips confirmations for update, delete, backup restore, and DB restore.
- `--password-stdin` for admin create.
- Installer accepts `PBMANAGER_ADMIN_PASSWORD` for first-owner bootstrap.
- All other password prompts read from the terminal and never echo.

## Systemd examples

```bash
systemctl status pbmanager
journalctl -u pbmanager -n 100 --no-pager
systemctl restart pbmanager
systemctl stop pbmanager
```

If the manager cannot start, the CLI still works:

```bash
pbmanager --config /etc/pbmanager/config.json db check
pbmanager --config /etc/pbmanager/config.json admin list
pbmanager --config /etc/pbmanager/config.json config validate
```
