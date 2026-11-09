# Security Model

This manager runs as root and controls PocketBase processes, systemd units, Caddy, and DNS. That is a high-trust position. The design reduces the blast radius through typed operations, explicit ownership markers, secret isolation, and a recovery path that does not depend on the web app.

## Threat model

### In scope

- Remote unauthenticated attacker reaching the dashboard.
- Authenticated low-privilege user (`viewer`) attempting privilege escalation.
- Compromised or malicious Cloudflare/SMTP/S3 credentials limited to their provider.
- Accidental destructive operator action.
- Manager crash or host reboot during provisioning/backup/restore.
- Local unprivileged user reading manager secrets or modifying manager files.
- Malicious PocketBase backup archive or release ZIP (path traversal, symlink, oversized files).
- Caddyfile corruption or partial writes.
- DNS record conflicts.

### Out of scope

- Root compromise of the host.
- Supply-chain compromise of the Go toolchain, Node modules, Caddy, or PocketBase official binaries beyond checksum verification.
- Physical access.
- A malicious provider API returning crafted data beyond normal validation.
- Multi-tenant isolation between instances on the same host; root and instance users can read what their permissions allow.

## Authentication

- Argon2id with 64 MiB memory, 3 iterations, 2 lanes, 16-byte salt, 32-byte key.
- Password policy: 12–128 characters; no forced composition rules; common/weak values rejected.
- No default account and no seeded password.
- First owner created only by the root CLI or installer.
- Disabled accounts cannot log in.
- Password reset and account disable revoke sessions.
- Last owner cannot be demoted, disabled, or deleted.
- Login attempts are limited per IP (5 failures, 15-minute block).
- Login errors do not reveal whether the username exists.

## Sessions and CSRF

- Cookie: `pbmanager_session`, random 32-byte token, HttpOnly, SameSite=Strict, Secure when configured, Path=/, expiry.
- Only SHA-256 of the token is stored.
- Session is valid only if not revoked and not expired.
- CSRF token is random and bound to the session.
- All mutating API routes require `X-CSRF-Token`.
- Login checks the Origin header when present; SameSite=Strict provides defense in depth.
- Session listing returns IP/user agent; revocation is immediate.

## Secrets

- AES-256-GCM with a random 32-byte key at `<dataDir>/secret.key`, mode 0600, root-owned.
- The key file is created with `O_EXCL`; symlinks and non-regular files are rejected on load.
- Every encrypted value is bound to an AAD purpose, e.g. `smtp:<id>`, `storage:<id>`, `cloudflare:<id>`, `pb_superuser:<instanceID>`. A ciphertext moved to another row fails to decrypt.
- Secrets are never returned by the API, never written to audit detail, and redacted from logs.
- PocketBase superuser passwords created during provisioning are generated with `crypto/rand`, passed to the official CLI, and stored encrypted. They are not printed.
- Managed superuser passwords are rotated on every repair. The old account is deleted first; the new password is stored only after successful authentication.
- The secret key is required to restore profile credentials and linked PocketBase auth. Back it up separately.

## Managed superuser ownership

A managed superuser is deliberately a long-lived, manager-owned credential. This is powerful and should be an explicit policy choice:

- enabling it means an operator password change or deletion is treated as drift and undone;
- **Stop managing** first deletes the account and clears the encrypted credential;
- every create/repair/delete is written to the audit log;
- the watchdog only acts after health is OK and two consecutive auth failures;
- a service outage, 429, or 5xx is not treated as credential drift;
- the manager uses only the official CLI for account mutation and the documented auth/list APIs for verification.

Do not manually rotate or delete a managed account unless you intend to stop managing it.

## PocketBase administration surface

The PocketBase admin tab is intentionally allowlisted:

- Settings are typed; there is no arbitrary settings JSON proxy.
- Application collection records and collection schema are not exposed.
- Auth records are limited to standard identity fields.
- Superuser mutations use the official CLI because PocketBase 0.23 does not expose record mutation routes for `_superusers`; the service briefly stops.
- The last superuser cannot be deleted.
- Native backup restore/delete and settings changes require explicit confirmation.
- Every write is audited without secret values.
- Editing a managed superuser is warned but allowed; the watchdog may rebuild it.
- SMTP/S3 password and secret fields are write-only; blank values preserve the existing secret.

## Manager self-update

- Releases are discovered from GitHub over HTTPS; the repository is fixed in the build (`elselawi/apexo-manager`), not supplied by the request.
- Only non-draft, non-prerelease releases are considered.
- The platform asset is downloaded over HTTPS (loopback HTTP is allowed only for tests), size-limited, and SHA-256 verified against the GitHub asset digest or `checksums.txt`.
- The staged binary is executed only after its hash and version are checked.
- Installation and restart run in a detached systemd `oneshot` unit, not through a shell in the web process.
- The previous binary is retained as `pbmanager.previous`; a failed health check automatically restores it and restarts.
- The update endpoint is owner-only; check-only access is admin.
- The manager cannot downgrade automatically or install from an arbitrary URL.
- The first installation of a self-updating build must use the signed installer/deploy path; older builds have no update endpoint.

## Privileged execution

- No endpoint accepts a shell command.
- `platform.Runner` executes an executable plus an argument vector. There is no shell interpolation.
- Service names, unit names, paths, domains, versions, and architectures are validated before use.
- Paths must be absolute and normalized; `..` traversal is rejected.
- Managed writes refuse symlinks and non-regular files.
- Unit writes require the manager marker; unmanaged unit files are never overwritten.
- Caddy writes are confined to the snippet directory and a validated import line.
- Deletion never recursively deletes instance data; purge moves it to a protected archive.
- The manager validates the effective UID for CLI mutations. `sudo -u`/username spoofing cannot bypass it.

## Web security

- Listener defaults to `127.0.0.1`; non-loopback bind requires an explicit unsafe opt-in.
- Caddy terminates TLS; the manager never exposes Caddy's admin API.
- Strict CSP: `default-src 'self'`, no inline scripts, no remote assets, `object-src 'none'`, `base-uri 'self'`, `frame-ancestors 'none'`.
- HSTS when the public URL is HTTPS.
- `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`, COOP/CORP.
- API responses use `Cache-Control: no-store`.
- Request body limits and JSON decoding with unknown-field rejection where appropriate.
- SPA routing serves only embedded assets; no arbitrary file server.
- Backup downloads require admin, are path-contained, and reject symlinks.
- Rate limiting is per client IP, with separate read/write buckets.
- `X-Forwarded-For` is trusted only when the immediate peer is loopback or private.

## Authorization

- `viewer`: read-only. Cannot mutate, download backups, see settings, or manage admins.
- `admin`: infrastructure, profiles, domains, backups, jobs. Cannot manage administrators or server settings (settings update is owner-only; settings read is admin).
- `owner`: everything, including administrators and settings.
- Ownership checks are server-side on every route; the UI is not the boundary.
- Audit actions record actor type (`admin`, `cli-root`, `system`) and never trust client-supplied actor names.

## DNS and Caddy safety

- Cloudflare records are created/updated/deleted only when the record comment carries `pbmanager:<instanceID>`.
- Existing conflicting A records are reported, not overwritten.
- Caddy snippets carry a marker. Unmarked files are preserved.
- Every Caddy change validates the full candidate config before writing.
- Backups are taken before writes; reload failure triggers restore.
- The manager does not edit existing site blocks.
- TLS verification after domain attach checks the actual certificate and HTTPS response.

## Audit

Audited events include:

- login success/failure/logout, password change, session revocation
- admin create/update/disable/delete
- instance import/provision/update/delete/start/stop/restart
- credentials linked/unlinked/tested
- domain attach/detach/check
- Caddy ensure-import/route removed/validated
- Cloudflare account and DNS changes
- SMTP/storage profile changes and tests
- backup create/verify/restore/delete
- settings updates
- CLI recovery actions and DB restore

Audit detail is structured JSON and excludes passwords/tokens. Retention default: 180 days.

## Filesystem permissions

- Process umask 0077.
- Data dir 0700, DB 0600, secret key 0600, backups 0700/0600.
- Installer config 0600, unit 0644.
- Parent directories created 0755 as needed for traversal, not 0777.
- Instance files owned by the instance user; manager operations chown after restore.

## Hardening recommendations

1. Put the dashboard behind Caddy with a public CA certificate.
2. Restrict 8787 to loopback and firewall 80/443 only.
3. Keep Caddy admin on `localhost:2019` or disable it and use systemctl restart.
4. Use a least-privilege Cloudflare token: Zone.DNS edit for required zones and Zone read; avoid global keys.
5. Use provider-specific SMTP/S3 credentials limited to one bucket/relay.
6. Store `secret.key` and DB snapshots in an encrypted off-host vault.
7. Enable unattended security updates and monitor `pbmanager`, `caddy`, and each PocketBase unit.
8. Keep the manager binary and PocketBase versions current.
9. Do not grant shell access to the manager service account; it runs as root and has no interactive login.
10. Test restores regularly, not just backups.

## Known security limitations

- The manager must run as root because it manages host systemd, users, and `/opt`. systemd hardening is applied but cannot fully sandbox a root service.
- Root CLI commands are intentionally powerful. Physical/root access is game over.
- Generated PocketBase superuser passwords are passed as CLI arguments to the official PocketBase binary; a local root process could observe them in the process list during the brief command. This is a limitation of PocketBase's CLI interface. The service is stopped during creation.
- Native backup uploads are copied live; this is an integrity/consistency nuance, not an authentication issue.
- TLS certificate and DNS checks depend on external DNS/CA behavior.
- No hardware-backed key storage. The AES key is a root-only file.
- No per-instance OS-level isolation beyond a dedicated user and systemd hardening; all instances share the host kernel.
