ALTER TABLE instances ADD COLUMN pb_superuser_managed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE instances ADD COLUMN pb_superuser_record_id TEXT NOT NULL DEFAULT '';
ALTER TABLE instances ADD COLUMN pb_superuser_last_verified_at TEXT NOT NULL DEFAULT '';
ALTER TABLE instances ADD COLUMN pb_superuser_last_repaired_at TEXT NOT NULL DEFAULT '';
ALTER TABLE instances ADD COLUMN pb_superuser_reconcile_error TEXT NOT NULL DEFAULT '';
ALTER TABLE instances ADD COLUMN pb_superuser_failures INTEGER NOT NULL DEFAULT 0;
