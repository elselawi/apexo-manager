PRAGMA foreign_keys = ON;

CREATE TABLE admins (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL COLLATE NOCASE UNIQUE,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('owner','admin','viewer')),
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    last_login_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    admin_id     TEXT NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    csrf_token   TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',
    revoked_at   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_sessions_admin ON sessions(admin_id);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

CREATE TABLE server_settings (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    data       TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE cloudflare_accounts (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL COLLATE NOCASE UNIQUE,
    api_token        TEXT NOT NULL,
    account_id       TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'unvalidated',
    last_validated_at TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE TABLE smtp_profiles (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL COLLATE NOCASE UNIQUE,
    host           TEXT NOT NULL,
    port           INTEGER NOT NULL,
    username       TEXT NOT NULL DEFAULT '',
    password       TEXT NOT NULL DEFAULT '',
    auth_method    TEXT NOT NULL DEFAULT 'PLAIN',
    tls_mode       TEXT NOT NULL DEFAULT 'starttls',
    local_name     TEXT NOT NULL DEFAULT '',
    from_address   TEXT NOT NULL DEFAULT '',
    from_name      TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'created',
    last_tested_at TEXT NOT NULL DEFAULT '',
    last_error     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

CREATE TABLE storage_profiles (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL COLLATE NOCASE UNIQUE,
    provider          TEXT NOT NULL DEFAULT 'custom',
    endpoint          TEXT NOT NULL,
    region            TEXT NOT NULL,
    bucket            TEXT NOT NULL,
    access_key        TEXT NOT NULL,
    secret_key        TEXT NOT NULL,
    force_path_style  INTEGER NOT NULL DEFAULT 1,
    status            TEXT NOT NULL DEFAULT 'created',
    last_tested_at    TEXT NOT NULL DEFAULT '',
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE TABLE instances (
    id                    TEXT PRIMARY KEY,
    name                  TEXT NOT NULL COLLATE NOCASE UNIQUE,
    hostname              TEXT NOT NULL COLLATE NOCASE UNIQUE,
    base_dir              TEXT NOT NULL,
    binary_path           TEXT NOT NULL,
    data_dir              TEXT NOT NULL,
    port                  INTEGER NOT NULL CHECK (port > 0 AND port < 65536),
    service_name          TEXT NOT NULL,
    service_file          TEXT NOT NULL,
    run_as_user           TEXT NOT NULL DEFAULT '',
    pb_version            TEXT NOT NULL DEFAULT '',
    status                TEXT NOT NULL DEFAULT 'unknown',
    health                TEXT NOT NULL DEFAULT 'unknown',
    managed               INTEGER NOT NULL DEFAULT 0,
    source                TEXT NOT NULL DEFAULT 'manual',
    notes                 TEXT NOT NULL DEFAULT '',
    discovered_json       TEXT NOT NULL DEFAULT '',
    cloudflare_account_id TEXT REFERENCES cloudflare_accounts(id) ON DELETE SET NULL,
    smtp_profile_id       TEXT REFERENCES smtp_profiles(id) ON DELETE SET NULL,
    storage_profile_id    TEXT REFERENCES storage_profiles(id) ON DELETE SET NULL,
    caddy_mode            TEXT NOT NULL DEFAULT 'unmanaged',
    smtp_apply_status     TEXT NOT NULL DEFAULT 'not_applied',
    smtp_applied_at       TEXT NOT NULL DEFAULT '',
    storage_apply_status  TEXT NOT NULL DEFAULT 'not_applied',
    storage_applied_at    TEXT NOT NULL DEFAULT '',
    pb_superuser_email    TEXT NOT NULL DEFAULT '',
    pb_superuser_secret   TEXT NOT NULL DEFAULT '',
    pb_auth_status        TEXT NOT NULL DEFAULT 'unlinked',
    pb_auth_checked_at    TEXT NOT NULL DEFAULT '',
    pb_auth_error         TEXT NOT NULL DEFAULT '',
    backup_enabled        INTEGER NOT NULL DEFAULT 1,
    backup_cron           TEXT NOT NULL DEFAULT '0 3 * * *',
    backup_retention      INTEGER NOT NULL DEFAULT 7,
    next_backup_at        TEXT NOT NULL DEFAULT '',
    last_backup_at        TEXT NOT NULL DEFAULT '',
    last_health_at        TEXT NOT NULL DEFAULT '',
    last_health_error     TEXT NOT NULL DEFAULT '',
    last_seen_at          TEXT NOT NULL DEFAULT '',
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL
);
CREATE INDEX idx_instances_status ON instances(status);
CREATE INDEX idx_instances_next_backup ON instances(next_backup_at);

CREATE TABLE domains (
    id                    TEXT PRIMARY KEY,
    instance_id           TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    hostname              TEXT NOT NULL COLLATE NOCASE UNIQUE,
    cloudflare_account_id TEXT REFERENCES cloudflare_accounts(id) ON DELETE SET NULL,
    zone_id               TEXT NOT NULL DEFAULT '',
    zone_name             TEXT NOT NULL DEFAULT '',
    dns_record_id         TEXT NOT NULL DEFAULT '',
    proxied               INTEGER NOT NULL DEFAULT 0,
    dns_managed           INTEGER NOT NULL DEFAULT 0,
    caddy_managed         INTEGER NOT NULL DEFAULT 0,
    snippet_path          TEXT NOT NULL DEFAULT '',
    dns_status            TEXT NOT NULL DEFAULT 'unknown',
    caddy_status          TEXT NOT NULL DEFAULT 'unknown',
    tls_status            TEXT NOT NULL DEFAULT 'unknown',
    tls_expires_at        TEXT NOT NULL DEFAULT '',
    last_checked_at       TEXT NOT NULL DEFAULT '',
    last_error            TEXT NOT NULL DEFAULT '',
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL
);
CREATE INDEX idx_domains_instance ON domains(instance_id);

CREATE TABLE jobs (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    payload         TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'queued',
    priority        INTEGER NOT NULL DEFAULT 0,
    attempts        INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 4,
    available_at    TEXT NOT NULL,
    started_at      TEXT NOT NULL DEFAULT '',
    finished_at     TEXT NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    progress        INTEGER NOT NULL DEFAULT 0,
    result          TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT UNIQUE,
    created_by      TEXT NOT NULL DEFAULT '',
    canceled        INTEGER NOT NULL DEFAULT 0,
    recovered_count INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX idx_jobs_queue ON jobs(status, available_at);

CREATE TABLE job_steps (
    id          TEXT PRIMARY KEY,
    job_id      TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    detail      TEXT NOT NULL DEFAULT '',
    output      TEXT NOT NULL DEFAULT '',
    attempt     INTEGER NOT NULL DEFAULT 0,
    started_at  TEXT NOT NULL DEFAULT '',
    finished_at TEXT NOT NULL DEFAULT '',
    UNIQUE(job_id, name)
);
CREATE INDEX idx_job_steps_job ON job_steps(job_id);

CREATE TABLE backups (
    id           TEXT PRIMARY KEY,
    instance_id  TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    job_id       TEXT NOT NULL DEFAULT '',
    method       TEXT NOT NULL,
    filename     TEXT NOT NULL,
    path         TEXT NOT NULL,
    size         INTEGER NOT NULL DEFAULT 0,
    sha256       TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'creating',
    error        TEXT NOT NULL DEFAULT '',
    note         TEXT NOT NULL DEFAULT '',
    automatic    INTEGER NOT NULL DEFAULT 0,
    verified_at  TEXT NOT NULL DEFAULT '',
    restored_at  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_backups_instance ON backups(instance_id, created_at DESC);

CREATE TABLE audit_logs (
    id          TEXT PRIMARY KEY,
    actor_type  TEXT NOT NULL DEFAULT 'system',
    actor_id    TEXT NOT NULL DEFAULT '',
    actor_name  TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    target_type TEXT NOT NULL DEFAULT '',
    target_id   TEXT NOT NULL DEFAULT '',
    detail      TEXT NOT NULL DEFAULT '',
    ip          TEXT NOT NULL DEFAULT '',
    request_id  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_audit_created ON audit_logs(created_at DESC);
CREATE INDEX idx_audit_target ON audit_logs(target_type, target_id);

CREATE TABLE server_metrics (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    captured_at  TEXT NOT NULL,
    cpu_percent  REAL NOT NULL DEFAULT 0,
    mem_used     INTEGER NOT NULL DEFAULT 0,
    mem_total    INTEGER NOT NULL DEFAULT 0,
    disk_used    INTEGER NOT NULL DEFAULT 0,
    disk_total   INTEGER NOT NULL DEFAULT 0,
    load1        REAL NOT NULL DEFAULT 0,
    uptime_seconds INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_server_metrics_time ON server_metrics(captured_at);

CREATE TABLE instance_metrics (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    instance_id   TEXT NOT NULL REFERENCES instances(id) ON DELETE CASCADE,
    captured_at   TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'unknown',
    health        TEXT NOT NULL DEFAULT 'unknown',
    mem_bytes     INTEGER NOT NULL DEFAULT 0,
    disk_bytes    INTEGER NOT NULL DEFAULT 0,
    cpu_percent   REAL NOT NULL DEFAULT 0,
    port_listening INTEGER NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_instance_metrics_time ON instance_metrics(instance_id, captured_at DESC);
