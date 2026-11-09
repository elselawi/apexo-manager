# HTTP API Reference

Base path: `/api/v1`. The dashboard uses the same API.

## Authentication

### Status

```http
GET /api/v1/auth/status
```

Public. Returns whether an owner exists and the manager version.

### Login

```http
POST /api/v1/auth/login
Content-Type: application/json

{"username":"admin","password":"..."}
```

Success:

- Sets an HttpOnly, Secure (when HTTPS), SameSite=Strict session cookie.
- Returns `admin` and a `csrfToken`.

Errors: `401 invalid_credentials`, `403 account_disabled`, `429 rate_limited`.

Login is also protected by Origin validation when an `Origin` header is present.

### Authenticated requests

Send the session cookie. Every mutating request must include:

```http
X-CSRF-Token: <token from login or /auth/me>
```

Missing/incorrect CSRF returns `403`.

### Session endpoints

| Method | Path | Role | Purpose |
| --- | --- | --- | --- |
| POST | `/auth/logout` | any | Revoke current session. |
| GET | `/auth/me` | any | Current admin + CSRF token. |
| GET | `/auth/sessions` | any | List own sessions. |
| POST | `/auth/change-password` | any | Change own password; revokes other sessions. |
| POST | `/auth/sessions/{id}/revoke` | any | Revoke one own session. |

## Conventions

- JSON in, JSON out.
- Errors:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "Human-readable message",
    "details": {}
  },
  "requestId": "..."
}
```

- List endpoints may return `items`, `total`, `page`, `pageSize`.
- IDs are random 32-character lowercase hex strings except numeric metric IDs.
- Timestamps are UTC RFC 3339.
- Mutations that take time return `202` with a `job`.
- Secret values are never returned (`password`, `secretKey`, `apiToken`, `pbSuperuserSecret`, backup paths).

## Overview and monitoring

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/overview` | viewer | Instance counts, server info, latest metrics, alerts, recent jobs/audit, backup posture. |
| GET | `/monitoring` | viewer | Server/instance metric series, Caddy status, backup/job status. |

## Instances

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/instances` | viewer | Search, status filters, pagination. |
| GET | `/instances/defaults` | viewer | Suggested port, paths, version, service/user names. |
| GET | `/instances/discover` | viewer | Read-only discovery candidates. |
| POST | `/instances` | admin | Enqueue provisioning. |
| POST | `/instances/import` | admin | Import an existing installation; no OS changes. |
| GET | `/instances/{id}` | viewer | Instance, domains, backups, jobs, metrics, capabilities. |
| PATCH | `/instances/{id}` | admin | Notes, backup schedule, profile/Cloudflare assignments. |
| DELETE | `/instances/{id}` | admin | Enqueue safe deletion; confirmation + purge flags. |
| POST | `/instances/{id}/start` | admin | Start and verify. |
| POST | `/instances/{id}/stop` | admin | Stop. |
| POST | `/instances/{id}/restart` | admin | Restart and verify. |
| POST | `/instances/{id}/update` | admin | Enqueue PocketBase update. |
| POST | `/instances/{id}/verify` | viewer | Live health check. |
| POST | `/instances/{id}/sync-caddy` | admin | Reconcile managed Caddy routes. |
| POST | `/instances/{id}/smtp/apply` | admin | Apply SMTP profile via PocketBase settings API. |
| POST | `/instances/{id}/storage/apply` | admin | Apply S3 profile via PocketBase settings API. |
| GET | `/instances/{id}/logs` | viewer | systemd journal lines. |
| GET | `/instances/{id}/metrics` | viewer | Instance metric history. |
| PUT | `/instances/{id}/credentials` | admin | Link PocketBase superuser `{email,password}`. |
| DELETE | `/instances/{id}/credentials` | admin | Unlink. |
| POST | `/instances/{id}/credentials/test` | admin | Retest linked credentials. |
| GET | `/instances/{id}/backups` | viewer | Instance backup list. |

### Create payload

```json
{
  "name": "clinic-b",
  "hostname": "clinic-b",
  "pbVersion": "0.23.6",
  "arch": "amd64",
  "port": 8091,
  "baseDir": "/opt/clinic-b",
  "dataDir": "/opt/clinic-b/pb_data",
  "serviceName": "clinic-b",
  "serviceFile": "/etc/systemd/system/clinic-b.service",
  "runAsUser": "pb-clinic-b",
  "createSystemUser": true,
  "allowRootUser": false,
  "domain": "app.example.com",
  "cloudflareAccountId": "",
  "manageCaddy": true,
  "manageDns": false,
  "proxied": true,
  "smtpProfileId": "",
  "storageProfileId": "",
  "backupEnabled": true,
  "backupCron": "0 3 * * *",
  "backupRetention": 7,
  "notes": "",
  "checksum": ""
}
```

Returns `202`:

```json
{"instance": {...}, "job": {...}}
```

### Update payload

```json
{
  "version": "0.23.7",
  "checksum": ""
}
```

### Delete payload

```json
{
  "confirmation": "clinic-b",
  "purgeData": true,
  "removeCaddy": true,
  "removeDns": false
}
```

## Domains

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/domains` | viewer | All domains with status. |
| GET | `/domains/{id}` | viewer | One domain. |
| POST | `/instances/{id}/domains` | admin | Attach `{hostname, cloudflareAccountId, proxied, manageCaddy, manageDns}`; may enqueue. |
| DELETE | `/domains/{id}` | admin | Detach `{confirmation, removeCaddy, removeDns}`; enqueues job. |
| POST | `/domains/{id}/check` | viewer | DNS/TLS/health check. |

## Caddy

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/caddy` | viewer | Status, parsed sites, imports, snippets. |
| POST | `/caddy/ensure-import` | admin | Enqueue import injection with staging/validation. |
| POST | `/caddy/validate` | admin | Validate the live config. |
| GET | `/caddy/logs?lines=200` | viewer | Journal output. |
| DELETE | `/caddy/routes/{domain}` | admin | Remove only the managed snippet for a domain. |

## Cloudflare

| Method | Path | Role |
| --- | --- | --- |
| GET | `/cloudflare/accounts` | admin |
| POST | `/cloudflare/accounts` | admin |
| PATCH | `/cloudflare/accounts/{id}` | admin |
| DELETE | `/cloudflare/accounts/{id}` | admin |
| POST | `/cloudflare/accounts/{id}/validate` | admin |
| GET | `/cloudflare/accounts/{id}/zones` | admin |
| GET | `/cloudflare/accounts/{id}/zones/{zone}/records` | admin |

Tokens are accepted on create/update but never returned. The manager verifies the token before saving. DNS writes use manager ownership comments and refuse conflicts.

## SMTP profiles

| Method | Path | Role |
| --- | --- | --- |
| GET | `/smtp-profiles` | viewer |
| POST | `/smtp-profiles` | admin |
| PATCH | `/smtp-profiles/{id}` | admin |
| DELETE | `/smtp-profiles/{id}` | admin |
| POST | `/smtp-profiles/{id}/test` | admin |

Create payload includes `name`, `host`, `port`, `username`, `password`, `tlsMode` (`starttls`, `tls`, `none`), `authMethod` (`PLAIN`, `LOGIN`), `from`, `fromName`. Loopback plaintext is allowed for local relays; remote plaintext is refused.

## Storage profiles

| Method | Path | Role |
| --- | --- | --- |
| GET | `/storage-profiles` | viewer |
| POST | `/storage-profiles` | admin |
| PATCH | `/storage-profiles/{id}` | admin |
| DELETE | `/storage-profiles/{id}` | admin |
| POST | `/storage-profiles/{id}/test` | admin |

Fields: `name`, `provider`, `endpoint`, `region`, `bucket`, `accessKey`, `secretKey`, `forcePathStyle`.

## Backups

| Method | Path | Role | Notes |
| --- | --- | --- | --- |
| GET | `/backups` | viewer | Filters by instance/status. |
| POST | `/backups` | admin | `{instanceId, note}`; enqueues. |
| GET | `/backups/{id}` | viewer | Record. |
| GET | `/backups/{id}/download` | admin | Streams the verified archive. |
| POST | `/backups/{id}/verify` | admin | Hash + manifest + SQLite validation. |
| POST | `/backups/{id}/restore` | admin | `{confirmation}`; enqueues exclusive restore. |
| DELETE | `/backups/{id}` | admin | Deletes artifact + record. |

## Jobs

| Method | Path | Role |
| --- | --- | --- |
| GET | `/jobs?status=&type=&instanceId=&limit=` | viewer |
| GET | `/jobs/{id}` | viewer |
| POST | `/jobs/{id}/cancel` | admin |
| POST | `/jobs/{id}/retry` | admin |

Job JSON includes steps with status, output, attempts, timestamps, and errors.

## Audit

```http
GET /api/v1/audit?action=&actor=&limit=
```

Viewer. Returns most recent entries, including admin, CLI-root, and system actors. Detail values are structured JSON; secrets are never included.

## Settings

| Method | Path | Role |
| --- | --- | --- |
| GET | `/settings` | admin |
| PUT | `/settings` | owner |
| POST | `/settings/detect-ip` | owner |
| GET | `/system/update` | admin |
| POST | `/system/update` | owner |

`GET /system/update` checks `elselawi/apexo-manager` for the latest published GitHub release and returns current/latest versions, release notes, asset metadata, checksum, and availability. `POST` enqueues a checksum-verified `manager.update` job. The updater uses a detached systemd unit; the response indicates that the update was scheduled, not that the restart has completed.

`PUT` body is the full `config` object returned by `GET`; partial settings updates are not accepted. The response includes `changed` fields and `warnings`. Bootstrap paths are read-only.

## Administrators

| Method | Path | Role |
| --- | --- | --- |
| GET | `/admins` | owner |
| POST | `/admins` | owner |
| PATCH | `/admins/{id}` | owner |
| DELETE | `/admins/{id}` | owner |
| POST | `/admins/{id}/revoke-sessions` | owner |

Last-owner protection returns `409 last_owner`.

## Health

```http
GET /healthz
GET /api/health
```

Public, no auth. Returns `{"service":"pbmanager","status":"ok","version":"..."}` and pings SQLite. Used by systemd/load balancers/Caddy.

## Security headers

All responses include:

- Content-Security-Policy (self-only; no inline scripts)
- X-Content-Type-Options: nosniff
- X-Frame-Options: DENY / frame-ancestors 'none'
- Referrer-Policy: no-referrer
- Permissions-Policy
- Cross-Origin-Opener-Policy / Resource-Policy
- HSTS when served over HTTPS
- Cache-Control: no-store for API responses

## Example

```bash
BASE=https://manager.example.com
COOKIE=/tmp/pbm.cookies
LOGIN=$(curl -fsS -c "$COOKIE" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"..."}' "$BASE/api/v1/auth/login")
CSRF=$(printf '%s' "$LOGIN" | jq -r .csrfToken)
curl -fsS -b "$COOKIE" "$BASE/api/v1/instances"
curl -fsS -b "$COOKIE" -H "X-CSRF-Token: $CSRF" \
  -H 'Content-Type: application/json' -d '{"instanceId":"...","note":"manual"}' \
  "$BASE/api/v1/backups"
```

The UI uses relative `/api/v1` URLs so it works behind Caddy without configuration.
