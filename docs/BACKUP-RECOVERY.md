# Backup and Recovery

The manager protects two different things:

1. **PocketBase instance data** — database and uploads.
2. **Manager state** — inventory, profiles, encrypted secrets, jobs, audit, schedules.

They use different backup paths and should both be protected.

## Instance backups

### Methods

#### `pocketbase_native` (preferred)

Requires a linked PocketBase superuser.

1. Creates a backup through PocketBase's documented `/api/backups` endpoint.
2. Waits for it to finish and downloads it with a file token.
3. Verifies the official ZIP and extracts `data.db` / `auxiliary.db`.
4. Bundles those files with the current `uploads/`, systemd unit, managed Caddy snippets, instance metadata, and a manifest.
5. Verifies the resulting `.tar.gz` and records SHA-256.
6. Deletes the temporary backup from PocketBase.

Why bundle? PocketBase's native artifact contains database files, not uploads. The manager keeps the official consistent DB snapshot and adds files that the official artifact omits.

#### `sqlite_snapshot`

Used when no superuser is linked or the native API fails.

1. Opens the live SQLite DB.
2. Uses `VACUUM INTO` to create a consistent snapshot without copying a live WAL set.
3. Bundles the snapshot with uploads and configuration.
4. Validates SQLite integrity and archive safety.

This is a safe infrastructure-level fallback, not a raw file copy.

### Archive layout

```
manifest.json
data/data.db
data/auxiliary.db
data/uploads/...
config/systemd.service
config/caddy/<domain>.caddy
config/instance.json
```

Manifest fields include format version, instance ID/name, creation time, method, PocketBase version, source data directory, database SHA-256, and notes.

### Verification

`backup verify` and every restore:

- Check status and path containment.
- Recompute the archive SHA-256.
- Reject symlinks, devices, and paths escaping the staging directory.
- Require a manifest and `data/data.db`.
- Open SQLite read-only and run `PRAGMA integrity_check`.
- Confirm the manifest database hash matches.

A backup is marked `verified` only after these checks.

### Schedule

Per instance:

- `backupEnabled`
- `backupCron` (five-field UTC)
- `backupRetention`
- `nextBackupAt`

The scheduler enqueues with a per-minute idempotency key so a restart cannot double-enqueue the same scheduled backup. Successful jobs update timestamps and prune old verified artifacts beyond retention.

Example: `15 4 * * *` means 04:15 UTC daily.

Enable from **Instance → Backups → Edit schedule** or `PATCH /instances/{id}`.

### Retention

Retention counts completed artifacts per instance. The newest are kept. Deleting a backup removes the file and row. The UI asks for confirmation.

### Restoration

A restore is intentionally conservative:

1. Validates the backup and the confirmation string (instance name).
2. Creates a **safety backup** of current state.
3. Writes a journal.
4. Stops the service and waits for it to stop.
5. Moves `pb_data` aside to `pb_data.pbmanager-pre-restore-<timestamp>`.
6. Extracts into `pb_data.pbmanager-restore-tmp-<timestamp>`, validates SQLite, then renames the `data` subtree into place.
7. Chowns to the instance user.
8. Starts the service, waits for `/api/health`.
9. Marks the restore complete and removes the journal.

Failure path:

- Stops the service if needed.
- Moves failed staging/data aside.
- Restores the previous data directory.
- Restarts and verifies.
- Reports the error.

The old data directory is never deleted automatically.

### Interrupted restore recovery

At startup, before workers claim jobs:

- Reads `<backupDir>/restores/*.json`.
- If the restore did not complete, stops the service, moves a partial data dir aside, restores `OldPath`, chowns, starts, and verifies.
- Removes completed journals.

This prevents a crash between rename and start from leaving an instance half-restored.

### Uploads consistency

The database snapshot is consistent. Uploads are copied while PocketBase may be serving; a file uploaded in the few seconds of copying could be newer or missing from the archive. For strict quiescence, schedule backups during low activity or stop the instance before a manual backup. Native backup cannot include uploads while the app is stopped because the API is unavailable; the SQLite fallback can be taken while stopped.

## Manager database backups

Manager state lives in `/var/lib/pbmanager/manager.db` (WAL mode).

### Online snapshot

```bash
sudo pbmanager --config /etc/pbmanager/config.json db snapshot /var/backups/pbmanager/manager.db
```

Uses `VACUUM INTO`. Safe while the service runs.

### Offline restore

```bash
sudo systemctl stop pbmanager
sudo pbmanager --config /etc/pbmanager/config.json db restore /var/backups/pbmanager/manager.db --yes
sudo systemctl start pbmanager
```

The command:

- Refuses while `pbmanager.service` is active.
- Validates a copy.
- Moves the current DB and `-wal`/`-shm` aside.
- Copies the snapshot with 0600.
- Audits the restore.
- Keeps the previous DB for rollback.

### Upgrade integration

`scripts/upgrade.sh` automatically takes a DB snapshot into `/var/backups/pbmanager/upgrade-<timestamp>/`, keeps the previous binary, and offers `--rollback`.

## Secret key

`<dataDir>/secret.key` encrypts SMTP passwords, S3 secret keys, Cloudflare tokens, and PocketBase superuser secrets. **A manager DB backup without the matching key cannot decrypt those secrets.** For disaster recovery, back up both:

- `manager.db`
- `secret.key`

Store them separately with root-only access, for example in an encrypted off-host vault. Losing the key does not lose PocketBase data, but profile credentials and linked PocketBase superusers must be re-entered.

Do not copy `manager.db` while the service is writing; use `db snapshot` or stop the service.

## Recommended backup policy

- PocketBase instances: native backup daily, retention 7–30, plus a monthly off-host copy.
- Manager database + secret key: daily snapshot, off-host encrypted copy.
- Caddyfile and managed snippets: included in instance archives and Caddy change backups.
- `cloud-init`/deployment configs and SSH keys: outside this manager.

## Disaster recovery scenarios

### Dashboard database lost, PocketBase instances intact

1. Stop `pbmanager`.
2. Restore `manager.db` + `secret.key` from backup.
3. Start `pbmanager`.
4. If the DB is unrecoverable, initialize a new manager and import existing instances again. PocketBase data is untouched.
5. Reset the owner through the CLI.

### Manager service broken but DB fine

The CLI works independently:

```bash
systemctl stop pbmanager
pbmanager --config /etc/pbmanager/config.json db check
pbmanager --config /etc/pbmanager/config.json admin list
pbmanager --config /etc/pbmanager/config.json recover
systemctl start pbmanager
```

### PocketBase instance broken

1. Use the dashboard restore, or CLI `backup restore <id> --yes`.
2. If the manager is down, restore the backup archive manually into a staging directory and follow the same stop/validate/rename/start sequence; keep the old directory.
3. Never overwrite a live `pb_data` with a raw unzip while the service is running.

### Both manager and instance lost

1. Rebuild the manager.
2. Recreate the system user, directories, and unit (or provision a new instance).
3. Restore the instance archive into an empty data directory while stopped.
4. Use a compatible PocketBase version or verify migrations after starting.
5. Start and verify health, then attach/import.

## What is not backed up

- The OS, Caddy binary, PocketBase binary, systemd units outside manager archives.
- DNS zone state (managed records are recreated from inventory).
- Secrets not stored in the manager DB (e.g., manually managed SSH keys).
- Live WAL state; snapshots are consistent, not byte-for-byte copies.

## Verification evidence

See [TESTING.md](TESTING.md) for the native backup + upload restore and SIGKILL recovery tests.
