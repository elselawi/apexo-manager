# Limitations and Environment Notes

## No remote server deployment

The original request targeted an existing DigitalOcean/Linux server. This environment had **no SSH keys, hostname, credentials, or network access to that server**, so a remote deployment was not possible. No attempt was made to invent or guess access.

What was delivered instead:

- The complete production system.
- Build/install/upgrade/uninstall scripts.
- A real systemd deployment on Ubuntu 24.04 under WSL2.
- A real Caddy HTTPS reverse proxy with a managed route.
- A real PocketBase installation imported without changes.
- A real provisioned PocketBase instance with a generated superuser.
- Real backup, upload restore, update, delete, and SIGKILL recovery exercises.
- Deployment documentation for the target server.

To deploy on the actual server:

```bash
scp dist/pbmanager deploy/ scripts/ user@server:/tmp/pbmanager/
ssh user@server
sudo /tmp/pbmanager/scripts/install.sh --domain manager.example.com \
  --public-url https://manager.example.com --admin-username admin
```

See [DEPLOYMENT.md](DEPLOYMENT.md). The installer refuses to overwrite unmanaged units and preserves existing data.

## Provider integrations were tested with fakes

- **Cloudflare:** API client, token verification, zones, longest-zone selection, record CRUD, ownership markers, and failure handling are covered with a local HTTP fake. No live token was available. Real Cloudflare behavior (rate limits, proxied records, zone permissions) may differ.
- **SMTP:** STARTTLS/TLS/plain and PLAIN/LOGIN code paths are implemented; tests use a local fake SMTP server. No real relay credentials were available. Some providers require specific EHLO names, app passwords, or TLS versions; validate on the target.
- **S3:** SigV4 `ListObjectsV2` is implemented and tested against a local signing-compatible fake. No live bucket was available. Some providers need path-style or region `auto`; both are configurable.
- **PocketBase:** tested against 0.23.6, including auth, settings, native backups, file tokens, and superuser CLI. Newer versions may change API/CLI details; the manager uses documented endpoints and returns clear errors.

## WSL2-specific constraints

- Windows had a process listening on `::1:443`, blocking WSL's localhost forwarding for 443. Verification used the WSL IP (`192.168.85.31`) with Chrome's host-resolver rule and ignored the local CA. A normal server with a public domain does not have this issue.
- Caddy's internal CA is not trusted by Windows. The HTTPS screenshots used `ignoreHTTPSErrors`. Production uses a public CA.
- The root CLI, systemd, Caddy, and PocketBase are real; WSL is only the host.
- WSL disk was ~94% full during development; the manager stores backups under `/var/lib/pbmanager/backups`. Monitor disk and retention on the real server.

## Functional limits

- **Single host.** No clustering, remote agents, or multi-node scheduling.
- **systemd/Linux only.** No launchd, OpenRC, Windows service, or Docker mode.
- **Caddy parser is conservative.** It handles common site addresses, `reverse_proxy`, imports, comments, and one-line blocks. It does not understand arbitrary Caddy snippets/matchers for ownership decisions. Unrecognized constructs are left untouched; managed domain attachment may still add a snippet.
- **Caddy reload.** Prefers `systemctl reload caddy`; falls back to `caddy reload`. If Caddy is configured with `admin off` and the unit lacks a working reload, changes roll back. Configure a loopback admin endpoint or a restart-based reload.
- **DNS provider.** Cloudflare only.
- **Object storage.** S3-compatible only.
- **Mail.** SMTP with PLAIN/LOGIN; no OAuth2 or provider-specific APIs.
- **PocketBase application auth.** One superuser per instance; no impersonation or end-user management.
- **Backups.** Native DB + uploads bundle. Uploads are copied while the app is live, so a file changed during the copy can be inconsistent by seconds. There is no continuous WAL archiving.
- **Restore.** Whole-instance restore only; no point-in-time recovery or single-collection restore.
- **Manager DB.** SQLite single writer. Fine for one host; not a high-availability control plane.
- **Jobs.** One worker. Long downloads/restores serialize other jobs. Priorities reduce but do not eliminate waiting.
- **Monitoring.** Sampled; no per-second metrics, alert delivery, email/pager integration, or long-term time-series database. Alerts are dashboard/audit-only.
- **Sessions.** Server-side sessions in SQLite; no SSO/OIDC/MFA.
- **Audit.** Append-only in SQLite but root can modify it. No external immutable log sink.
- **Recovery.** Root CLI needs a readable DB and secret key for secrets; if the key is lost, credentials must be re-entered. PocketBase data remains.
- **No arbitrary shell.** This is intentional, but it also means any operation not modeled by the manager must be done manually on the host.
- **No automatic data relocation.** Changing default paths never moves existing instances.
- **No host firewall/SSH management.** Use your normal configuration management.

## UI limits

- Desktop-first, responsive down to tablet/phone but optimized for a server admin desktop.
- English-only.
- No dark theme.
- Polling rather than live push; job progress updates on a timer.
- Some long lists are not virtualized; the UI uses pagination/limits.
- Screenshots were captured at 1500×1000; small-screen layout was not exhaustively tested.

## Known code-level risks

- The manager runs as root by design. systemd hardening is defense in depth, not a sandbox.
- Generated PocketBase superuser passwords are briefly visible in the process argument list during `superuser upsert`, a limitation of PocketBase's CLI. The service is stopped around the command.
- The secret key is a file, not an HSM/KMS.
- Server settings are owner-editable; an owner can point paths at sensitive locations, but path validation and managed-file markers limit unintended overwrites. Root-equivalent trust is assumed for owners.
- The legacy 0.23.6 checksums are pinned from official release artifacts; only the three listed architectures are covered. Other old versions require `checksums.txt` or an explicit checksum.
- `scripts/uninstall.sh --purge` removes manager paths only; managed Caddy snippets/DNS records should be removed first.

## Production checklist

- [ ] Deploy with HTTPS and `secure_cookies: true`.
- [ ] Firewall 8787 and PocketBase ports.
- [ ] Back up `manager.db` **and** `secret.key` off-host.
- [ ] Enable scheduled instance backups and verify at least one restore.
- [ ] Test root CLI recovery while the web service is stopped.
- [ ] Confirm Caddy admin is loopback-only.
- [ ] Confirm unmanaged PocketBase units/directories are untouched.
- [ ] Set retention and monitor disk.
- [ ] Test reboot and systemd enablement.
- [ ] Review audit retention and access.
