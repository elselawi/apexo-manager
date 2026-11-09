# Troubleshooting

Start with:

```bash
sudo systemctl status pbmanager --no-pager
sudo journalctl -u pbmanager -n 120 --no-pager
sudo pbmanager --config /etc/pbmanager/config.json config validate
sudo pbmanager --config /etc/pbmanager/config.json db check
sudo pbmanager --config /etc/pbmanager/config.json diagnose
```

## Service will not start

### Port already in use

```
listen tcp 127.0.0.1:8787: bind: address already in use
```

Find the process:

```bash
ss -ltnp | grep 8787
```

Stop the old manager or change `listen_addr`. If a stale manual process is running, stop it before starting systemd. The manager now cancels startup on bind failure instead of deadlocking.

### Permission denied on config/data

```bash
ls -ld /etc/pbmanager /var/lib/pbmanager /var/lib/pbmanager/manager.db
sudo chown -R root:root /etc/pbmanager /var/lib/pbmanager
sudo chmod 700 /var/lib/pbmanager
sudo chmod 600 /etc/pbmanager/config.json /var/lib/pbmanager/manager.db /var/lib/pbmanager/secret.key
```

The service runs as root, but an earlier non-root run or copied backup can leave wrong ownership.

### Database corrupt

```bash
sudo systemctl stop pbmanager
sudo pbmanager --config /etc/pbmanager/config.json db check
sudo cp -a /var/lib/pbmanager/manager.db /var/lib/pbmanager/manager.db.corrupt
sudo pbmanager --config /etc/pbmanager/config.json db restore <snapshot> --yes
sudo systemctl start pbmanager
```

If no snapshot exists, move the DB aside and restart to create a fresh one, then re-import instances. PocketBase data is unaffected.

### "worker.lock" busy

Only one process may own the job worker. If the daemon is stuck:

```bash
sudo systemctl restart pbmanager
```

`flock` is released automatically when the process exits. Do not delete lock files manually; the file itself is harmless.

## Login problems

- No default account exists. Create one:

```bash
sudo pbmanager --config /etc/pbmanager/config.json admin create --username admin --role owner
```

- Forgot password:

```bash
sudo systemctl stop pbmanager
sudo pbmanager --config /etc/pbmanager/config.json admin reset-password admin
sudo systemctl start pbmanager
```

- "Invalid CSRF token": the session cookie and CSRF token are out of sync. Reload the page or sign in again. Reverse proxies must pass cookies and headers unchanged.
- Secure cookie not sent: you are opening an `http://` URL while the public URL is HTTPS. Use the HTTPS domain or temporarily set `secure_cookies:false` for local testing and restart (not recommended for production).
- 429: wait for the login block to expire (15 minutes) or restart the service to clear the in-memory limiter.
- Account disabled: enable it as another owner or use `admin enable`.

## Dashboard is blank or serves an old build

The frontend is embedded. Verify:

```bash
curl -sS http://127.0.0.1:8787/ | head
curl -sS http://127.0.0.1:8787/healthz
```

A hard refresh clears cached assets. If a reverse proxy caches HTML, disable caching for the dashboard. Rebuild and upgrade if the embedded asset hash does not change.

## Caddy

### Caddy service inactive

```bash
systemctl status caddy
caddy validate --config /etc/caddy/Caddyfile
journalctl -u caddy -n 80 --no-pager
```

The manager will not modify anything if validation fails.

### "managed import missing"

Use **Caddy → Install managed import** or:

```bash
sudo pbmanager --config /etc/pbmanager/config.json caddy ensure-import
```

This adds one line to the Caddyfile after staging and validation. Existing sites are preserved.

### Reload fails and configuration rolls back

Check Caddy logs. Common causes:

- Port 80/443 already used.
- DNS not pointing at the host during automatic HTTPS.
- Conflicting site addresses.
- Caddy admin disabled while the reload fallback expects it.

The manager restores the previous Caddyfile/snippets and attempts a reload.

### "snippet is not manager-owned"

A file with the same domain exists without the manager marker. The manager refuses to overwrite it. Rename/remove the custom file or attach a different hostname. This is intentional.

### Caddy route creation succeeds but HTTPS fails

- Confirm DNS resolves to the server.
- Confirm ports 80/443 are open.
- Check certificate issuance in Caddy logs.
- For Cloudflare, use proxied or DNS-only consistently; DNS-only is simpler for origin issuance.

## Provisioning

### "target directory already exists"

The manager only provisions into a new/empty directory or one carrying its own marker. Choose a different name/path or remove the leftover after verifying it is not a real installation.

### "unit file exists and is not managed"

An existing `/etc/systemd/system/<name>.service` belongs to someone else. Import that instance instead of provisioning over it.

### Download failed

```
HTTP 504 / checksum unavailable
```

- Try again; transient GitHub edge failures happen.
- Configure a trusted mirror in **Server configuration** (HTTPS; loopback HTTP allowed).
- For versions without `checksums.txt`, the manager has pinned 0.23.6 digests. Provide an explicit checksum if using a custom mirror.
- Do not disable checksum verification in production.

### Permission denied creating `/opt/<name>`

The process umask is 0077. Parent directories are created 0755, but an existing root directory with 0700 can block the unprivileged instance user. Check:

```bash
namei -l /opt/<name>/pb_data
```

Fix ownership/modes deliberately; do not chmod `/opt` 0777.

### Service starts but health fails

```bash
systemctl status <name> --no-pager
journalctl -u <name> -n 100 --no-pager
ss -ltnp | grep <port>
sudo -u <run-as> test -r <data-dir>/data.db
```

Common causes: wrong data directory ownership, port conflict, no execute permission on the binary, or a unit hardening directive that hides a required path.

### Superuser step failed

PocketBase's `superuser upsert` must run as the instance user while the service is stopped. Check:

```bash
journalctl -u pbmanager -n 80 --no-pager
sudo -u <run-as> /opt/<name>/pocketbase superuser upsert <email> '<password>' --dir <data-dir>
```

The generated password is stored encrypted; if the step failed, fix the cause and retry the job. Never log the password.

## Existing instance import

- Port 0 or data dir missing in discovery: the process/unit may use a wrapper or nonstandard flags. Import manually with explicit values.
- Duplicate discovery entries: the manager consolidates by data directory; if you see duplicates after an upgrade, refresh the page. Report evidence with `instances discover --json`.
- Import refuses duplicate name/port: choose a unique name; do not change the live port to satisfy the manager.
- Imported instance shows `unmanaged`: expected. It means the manager did not create the service and will not delete it as a managed unit.

## Backups

### "no PocketBase management credentials linked"

Native backup falls back to `sqlite_snapshot`. To enable native backups, link credentials on the instance page. Ensure the superuser can authenticate at `http://127.0.0.1:<port>`.

### Backup fails checksum

The archive changed after creation or the DB row points at a partially written file. Recreate the backup. Do not restore an unverified backup.

### Restore refused: status

Only `verified` backups restore. Run `backup verify <id>`; fix the archive or create a new one.

### Restore failed and instance is down

Check the journal and previous data:

```bash
ls -ld <data-dir>.pbmanager-pre-restore-*
journalctl -u <service> -n 100 --no-pager
journalctl -u pbmanager -n 120 --no-pager
```

At startup the manager attempts journal rollback. If it cannot, manually stop the service, move the failed data dir aside, move the `pre-restore` directory back, chown, and start. Keep all copies.

### Uploads missing after restore

Use a backup created after uploads existed. The manifest lists uploads; verify with:

```bash
tar -tzf /var/lib/pbmanager/backups/<instance>/<file>.tar.gz | grep uploads
```

Native backups bundle uploads, but a fresh instance with no uploads writes none.

## Jobs stuck

```bash
sudo pbmanager --config /etc/pbmanager/config.json jobs list
sudo pbmanager --config /etc/pbmanager/config.json jobs inspect <id>
```

- `queued` with a future `availableAt`: retry backoff; wait or fix the cause.
- `running` for a long time: check the journal; the job may be in a slow download or health wait.
- After a crash, `recoveredCount` should be > 0 and the job should be queued/running again.
- Cancel then retry if safe.
- Never edit `jobs` rows by hand unless you understand the engine; prefer the CLI/API.

## Monitoring shows stale data

- Monitor interval default 60s; wait one interval or restart.
- A closed port can mean the instance is actually down.
- Disk gauge reads the filesystem containing the data directory.
- ServerInfo disk free/used is computed from `statfs`; values are bytes.
- Metrics are sampled, not real-time.

## CLI says "must be run as root"

Use `sudo`. The effective UID is checked, not `$USER`. Rootless operation is unsupported for mutations.

## Exit code confusion in scripts

Use the binary directly and check `$?`; wrapper shells can mask status. The manager returns 0 success, 1 failure, 2 usage.

## WSL notes

- Windows may reserve/occupy port 443; use the WSL IP and a hosts rule for local testing.
- `manager.localhost` resolves inside WSL via `/etc/hosts`, not on Windows.
- Caddy's internal CA is not trusted by Windows; use `-k`/`ignoreHTTPSErrors` for local verification only.
- Ensure systemd is enabled in `/etc/wsl.conf` before relying on services across restarts.

## Collecting a support bundle safely

```bash
sudo pbmanager --config /etc/pbmanager/config.json diagnose
sudo systemctl status pbmanager caddy --no-pager
sudo journalctl -u pbmanager -u caddy -n 200 --no-pager
sudo pbmanager --config /etc/pbmanager/config.json db check
```

Review output for secrets and private paths before sharing. Never share `secret.key`, the manager DB, `.env` files, provider tokens, or PocketBase superuser passwords.
