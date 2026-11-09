# Upgrade, Rollback, and Uninstall

## Dashboard one-click manager update

Owners can use **Server configuration → Manager updates**:

1. Click **Check GitHub**. The manager reads the latest non-draft, non-prerelease release from `elselawi/apexo-manager`.
2. Review the release notes and version.
3. Click **Install x.y.z**.

The manager:

- downloads the platform asset over HTTPS,
- verifies the SHA-256 from the GitHub asset digest or `checksums.txt`,
- stages it under `/var/lib/pbmanager/updates` (0700),
- starts a detached `systemd-run` unit,
- the detached updater verifies the staged binary again, preserves `/usr/local/bin/pbmanager.previous`, atomically installs the new binary, restarts `pbmanager`, and polls `/healthz`,
- if the new build is unhealthy, restores `.previous`, restarts, and records the failure in `/var/lib/pbmanager/updates/last-result.json`.

The browser page may disconnect for a few seconds while the service restarts; it reloads automatically. The job result indicates that the update was **scheduled**; the detached updater writes the final success or rollback result to `last-result.json`.

Requirements:

- the repository has a published GitHub release,
- the release includes `pbmanager_<version>_linux_amd64` or `_arm64` and either asset digests or `checksums.txt`,
- the server can reach `api.github.com` and the GitHub release asset host,
- the installation was upgraded at least once to a build containing the self-update endpoint (1.0.3+). Older builds must first use `deploy-remote.sh` or the installer.

Publishing a release is handled by `.github/workflows/release.yml`:

```bash
git tag v1.0.8
git push origin v1.0.8
```

The workflow builds the embedded frontend, runs the tests, cross-compiles Linux amd64/arm64, writes `checksums.txt`, and creates the GitHub release.

## Manager upgrade with the script

### 1. Build the new binary

On the build host:

```bash
VERSION=1.1.0 ./scripts/build.sh
```

Record `dist/pbmanager.sha256`. The build runs tests and embeds the current frontend.

### 2. Run the upgrade

```bash
sudo ./scripts/upgrade.sh --binary ./dist/pbmanager --listen http://127.0.0.1:8787
```

The script:

1. Creates `/var/backups/pbmanager/upgrade-<timestamp>/`.
2. Copies the current binary, bootstrap config, and unit.
3. Takes a consistent SQLite snapshot with `pbmanager db snapshot`.
4. Stops the service.
5. Installs the new binary (0755).
6. Starts the service.
7. Waits for `/healthz`.
8. On failure, restores the previous binary and starts it again.

It never overwrites bootstrap config, data, or instance inventory.

### 3. Verify

```bash
curl -sS http://127.0.0.1:8787/healthz
sudo pbmanager --config /etc/pbmanager/config.json status --json
sudo pbmanager --config /etc/pbmanager/config.json db check
sudo pbmanager --config /etc/pbmanager/config.json config validate
journalctl -u pbmanager -n 80 --no-pager
```

Open the dashboard and check:

- Overview loads.
- Instances are healthy.
- Jobs page shows the worker running.
- Caddy still validates.
- Scheduled backups retained their `nextBackupAt`.

### 4. Roll back

```bash
sudo ./scripts/upgrade.sh --rollback
```

It selects the most recent `upgrade-*/pbmanager.binary`, stops the service, installs it, starts, and checks health. Database migrations are forward-only in general. If a new migration ran, the older binary may still start (SQLite schema additions are usually compatible), but restore the DB snapshot if not:

```bash
sudo systemctl stop pbmanager
sudo pbmanager --config /etc/pbmanager/config.json db restore \
  /var/backups/pbmanager/upgrade-<timestamp>/manager.db --yes
sudo systemctl start pbmanager
```

Always test a rollback in staging before relying on it in production.

## PocketBase upgrade

Per instance:

```bash
sudo pbmanager --config /etc/pbmanager/config.json instances update clinic-a \
  --version 0.23.7 --yes
```

Flow:

1. Preflight checks service/binary.
2. Creates a safety backup.
3. Downloads and verifies the release.
4. Stops the service gracefully.
5. Extracts and atomically replaces the binary; keeps `.previous`.
6. Starts and waits for health.
7. Verifies the executable version.
8. Records the version.

If start/health/version verification fails, it restores `.previous`, restarts, and reports failure. The safety backup remains available.

Notes:

- PocketBase data migrations run on first start of the new version. A binary rollback may not reverse data migrations. Use the safety backup if the older version cannot read the migrated DB.
- Back up uploads too; native safety backups include them.
- Test major versions on a clone first.

## Frontend changes

The frontend is embedded in the Go binary. There is no separate asset deployment and no Node.js in production. After upgrading, a hard refresh may be needed if a browser cached the old service worker; the app does not use a service worker, so normal cache headers apply.

## Uninstall

```bash
sudo ./scripts/uninstall.sh
```

Default behavior:

- Stops and disables `pbmanager.service`.
- Removes the unit **only if it carries the installer marker**.
- Removes the binary.
- Preserves `/etc/pbmanager` and `/var/lib/pbmanager`.

Purge:

```bash
sudo ./scripts/uninstall.sh --purge --yes
```

This also removes:

- `/etc/pbmanager`
- `/var/lib/pbmanager` (DB, secrets, backups, job history)

It does **not** remove:

- PocketBase installations under `/opt`, `/srv`, or elsewhere
- PocketBase system users
- Caddy itself or existing sites
- Managed Caddy snippets (remove them first from the Caddy page or manually)
- Managed DNS records (remove them from Cloudflare or the dashboard first)

To remove Caddy artifacts before purging:

```bash
sudo pbmanager --config /etc/pbmanager/config.json caddy status
# For each managed route:
sudo pbmanager --config /etc/pbmanager/config.json caddy route --help
# Use the dashboard's Caddy → Remove action, or delete the marked snippet and reload Caddy.
```

## Manual upgrade

If scripts are unavailable:

```bash
sudo install -m 0755 dist/pbmanager /usr/local/bin/pbmanager.new
sudo systemctl stop pbmanager
sudo mv /usr/local/bin/pbmanager /usr/local/bin/pbmanager.previous
sudo mv /usr/local/bin/pbmanager.new /usr/local/bin/pbmanager
sudo systemctl start pbmanager
curl -fsS http://127.0.0.1:8787/healthz
```

Never replace the binary while the service is running.

## Backup before every upgrade

At minimum:

```bash
sudo pbmanager --config /etc/pbmanager/config.json db snapshot \
  /var/backups/pbmanager/manager-before-upgrade.db
sudo cp -a /var/lib/pbmanager/secret.key /var/backups/pbmanager/secret.key
```

The secret key is required to decrypt stored credentials after restoring the DB. Protect both files.
