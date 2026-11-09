package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const instanceColumns = `i.id, i.name, i.hostname, i.base_dir, i.binary_path, i.data_dir, i.port,
	i.service_name, i.service_file, i.run_as_user, i.pb_version, i.status, i.health,
	i.managed, i.source, i.notes, i.discovered_json, COALESCE(i.cloudflare_account_id,''),
	COALESCE(i.smtp_profile_id,''), COALESCE(i.storage_profile_id,''), i.caddy_mode,
	i.smtp_apply_status, i.smtp_applied_at, i.storage_apply_status, i.storage_applied_at,
	i.pb_superuser_email, i.pb_superuser_secret, i.pb_auth_status, i.pb_auth_checked_at, i.pb_auth_error,
	i.pb_superuser_managed, i.pb_superuser_record_id, i.pb_superuser_last_verified_at,
	i.pb_superuser_last_repaired_at, i.pb_superuser_reconcile_error, i.pb_superuser_failures,
	i.backup_enabled, i.backup_cron, i.backup_retention, i.next_backup_at, i.last_backup_at,
	i.last_health_at, i.last_health_error, i.last_seen_at, i.created_at, i.updated_at,
	COALESCE(sp.name,''), COALESCE(stp.name,''), COALESCE(cfa.name,''),
	COALESCE((SELECT b.status FROM backups b WHERE b.instance_id=i.id ORDER BY b.created_at DESC LIMIT 1),''),
	COALESCE((SELECT b.size FROM backups b WHERE b.instance_id=i.id ORDER BY b.created_at DESC LIMIT 1),0)`

const instanceFrom = ` FROM instances i
	LEFT JOIN smtp_profiles sp ON sp.id=i.smtp_profile_id
	LEFT JOIN storage_profiles stp ON stp.id=i.storage_profile_id
	LEFT JOIN cloudflare_accounts cfa ON cfa.id=i.cloudflare_account_id`

func scanInstance(sc interface{ Scan(...any) error }) (*Instance, error) {
	var v Instance
	var managed, backupEnabled, pbManaged int
	err := sc.Scan(
		&v.ID, &v.Name, &v.Hostname, &v.BaseDir, &v.BinaryPath, &v.DataDir, &v.Port,
		&v.ServiceName, &v.ServiceFile, &v.RunAsUser, &v.PBVersion, &v.Status, &v.Health,
		&managed, &v.Source, &v.Notes, &v.DiscoveredJSON, &v.CloudflareAccountID,
		&v.SMTPProfileID, &v.StorageProfileID, &v.CaddyMode,
		&v.SMTPApplyStatus, &v.SMTPAppliedAt, &v.StorageApplyStatus, &v.StorageAppliedAt,
		&v.PBSuperuserEmail, &v.PBSuperuserSecret, &v.PBAuthStatus, &v.PBAuthCheckedAt, &v.PBAuthError,
		&pbManaged, &v.PBSuperuserRecordID, &v.PBLastVerifiedAt, &v.PBLastRepairedAt, &v.PBReconcileError, &v.PBFailureCount,
		&backupEnabled, &v.BackupCron, &v.BackupRetention, &v.NextBackupAt, &v.LastBackupAt,
		&v.LastHealthAt, &v.LastHealthError, &v.LastSeenAt, &v.CreatedAt, &v.UpdatedAt,
		&v.SMTPProfileName, &v.StorageProfileName, &v.CloudflareName,
		&v.LastBackupStatus, &v.LastBackupSize,
	)
	if err != nil {
		return nil, err
	}
	v.Managed = managed != 0
	v.BackupEnabled = backupEnabled != 0
	v.PBSuperuserManaged = pbManaged != 0
	return &v, nil
}

// CreateInstance inserts an inventory record. It never touches the filesystem.
func (s *Store) CreateInstance(ctx context.Context, i *Instance) error {
	if i.ID == "" {
		i.ID = NewID()
	}
	if i.CreatedAt == "" {
		i.CreatedAt = Now()
	}
	i.UpdatedAt = Now()
	if i.Status == "" {
		i.Status = "unknown"
	}
	if i.Health == "" {
		i.Health = "unknown"
	}
	if i.Source == "" {
		i.Source = "manual"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO instances(
		id,name,hostname,base_dir,binary_path,data_dir,port,service_name,service_file,run_as_user,
		pb_version,status,health,managed,source,notes,discovered_json,cloudflare_account_id,
		smtp_profile_id,storage_profile_id,caddy_mode,smtp_apply_status,smtp_applied_at,
		storage_apply_status,storage_applied_at,pb_superuser_email,pb_superuser_secret,
		pb_auth_status,pb_auth_checked_at,pb_auth_error,backup_enabled,backup_cron,backup_retention,
		next_backup_at,last_backup_at,last_health_at,last_health_error,last_seen_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		i.ID, i.Name, i.Hostname, i.BaseDir, i.BinaryPath, i.DataDir, i.Port, i.ServiceName, i.ServiceFile, i.RunAsUser,
		i.PBVersion, i.Status, i.Health, boolInt(i.Managed), i.Source, i.Notes, i.DiscoveredJSON, nullIfEmpty(i.CloudflareAccountID),
		nullIfEmpty(i.SMTPProfileID), nullIfEmpty(i.StorageProfileID), i.CaddyMode, i.SMTPApplyStatus, i.SMTPAppliedAt,
		i.StorageApplyStatus, i.StorageAppliedAt, i.PBSuperuserEmail, i.PBSuperuserSecret,
		i.PBAuthStatus, i.PBAuthCheckedAt, i.PBAuthError, boolInt(i.BackupEnabled), i.BackupCron, i.BackupRetention,
		i.NextBackupAt, i.LastBackupAt, i.LastHealthAt, i.LastHealthError, i.LastSeenAt, i.CreatedAt, i.UpdatedAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("an instance with that name, hostname, or port already exists: %w", err)
		}
		return err
	}
	return nil
}

// UpdateInstance updates all editable inventory fields. Callers must explicitly
// decide whether path edits are permitted; no files are moved here.
func (s *Store) UpdateInstance(ctx context.Context, i *Instance) error {
	i.UpdatedAt = Now()
	res, err := s.db.ExecContext(ctx, `UPDATE instances SET
		name=?, hostname=?, base_dir=?, binary_path=?, data_dir=?, port=?, service_name=?, service_file=?,
		run_as_user=?, pb_version=?, managed=?, source=?, notes=?, discovered_json=?,
		cloudflare_account_id=?, smtp_profile_id=?, storage_profile_id=?, caddy_mode=?,
		smtp_apply_status=?, smtp_applied_at=?, storage_apply_status=?, storage_applied_at=?,
		pb_superuser_email=?, pb_superuser_secret=?, pb_auth_status=?, pb_auth_checked_at=?, pb_auth_error=?,
		backup_enabled=?, backup_cron=?, backup_retention=?, next_backup_at=?, updated_at=?
		WHERE id=?`,
		i.Name, i.Hostname, i.BaseDir, i.BinaryPath, i.DataDir, i.Port, i.ServiceName, i.ServiceFile,
		i.RunAsUser, i.PBVersion, boolInt(i.Managed), i.Source, i.Notes, i.DiscoveredJSON,
		nullIfEmpty(i.CloudflareAccountID), nullIfEmpty(i.SMTPProfileID), nullIfEmpty(i.StorageProfileID), i.CaddyMode,
		i.SMTPApplyStatus, i.SMTPAppliedAt, i.StorageApplyStatus, i.StorageAppliedAt,
		i.PBSuperuserEmail, i.PBSuperuserSecret, i.PBAuthStatus, i.PBAuthCheckedAt, i.PBAuthError,
		boolInt(i.BackupEnabled), i.BackupCron, i.BackupRetention, i.NextBackupAt, i.UpdatedAt, i.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("an instance with that name, hostname, or port already exists: %w", err)
		}
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetInstance loads one instance by ID.
func (s *Store) GetInstance(ctx context.Context, id string) (*Instance, error) {
	v, err := scanInstance(s.db.QueryRowContext(ctx, `SELECT `+instanceColumns+instanceFrom+` WHERE i.id=?`, id))
	return v, notFound(err)
}

// GetInstanceByName loads one instance by name.
func (s *Store) GetInstanceByName(ctx context.Context, name string) (*Instance, error) {
	v, err := scanInstance(s.db.QueryRowContext(ctx, `SELECT `+instanceColumns+instanceFrom+` WHERE i.name=? COLLATE NOCASE`, name))
	return v, notFound(err)
}

// GetInstanceByHostname loads one instance by its identifier hostname.
func (s *Store) GetInstanceByHostname(ctx context.Context, hostname string) (*Instance, error) {
	v, err := scanInstance(s.db.QueryRowContext(ctx, `SELECT `+instanceColumns+instanceFrom+` WHERE i.hostname=? COLLATE NOCASE`, hostname))
	return v, notFound(err)
}

// ListInstances returns every inventory record ordered by name.
func (s *Store) ListInstances(ctx context.Context) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+instanceColumns+instanceFrom+` ORDER BY i.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Instance{}
	for rows.Next() {
		v, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// DeleteInstance removes the inventory row. It never deletes installation data.
func (s *Store) DeleteInstance(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM instances WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateInstanceRuntime persists the latest observed runtime state.
func (s *Store) UpdateInstanceRuntime(ctx context.Context, id, status, health, lastHealthAt, healthErr, lastSeen string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET status=?, health=?, last_health_at=?, last_health_error=?, last_seen_at=?, updated_at=? WHERE id=?`,
		status, health, lastHealthAt, healthErr, lastSeen, Now(), id)
	return err
}

// SetPBAuthState records application-level authentication linkage status.
func (s *Store) SetPBAuthState(ctx context.Context, id, email, secret, status, checkedAt, errText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET pb_superuser_email=?, pb_superuser_secret=?, pb_auth_status=?, pb_auth_checked_at=?, pb_auth_error=?, updated_at=? WHERE id=?`,
		email, secret, status, checkedAt, errText, Now(), id)
	return err
}

// SetPBUnmanaged clears the manager-owned flag without touching credentials.
func (s *Store) SetPBUnmanaged(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET pb_superuser_managed=0, pb_superuser_record_id='', pb_superuser_failures=0, pb_superuser_reconcile_error='', updated_at=? WHERE id=?`, Now(), id)
	return err
}

// SetManagedPBAuth records a manager-owned superuser.
func (s *Store) SetManagedPBAuth(ctx context.Context, id, email, secret, recordID, checkedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET
		pb_superuser_email=?, pb_superuser_secret=?, pb_superuser_record_id=?, pb_superuser_managed=1,
		pb_auth_status='linked', pb_auth_checked_at=?, pb_auth_error='', pb_superuser_failures=0,
		pb_superuser_last_verified_at=?, pb_superuser_reconcile_error='', updated_at=? WHERE id=?`,
		email, secret, recordID, checkedAt, checkedAt, Now(), id)
	return err
}

// MarkPBAuthVerified records a successful probe.
func (s *Store) MarkPBAuthVerified(ctx context.Context, id, recordID, checkedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET
		pb_superuser_record_id=?, pb_auth_status='linked', pb_auth_checked_at=?, pb_auth_error='',
		pb_superuser_last_verified_at=?, pb_superuser_failures=0, pb_superuser_reconcile_error='', updated_at=?
		WHERE id=?`, recordID, checkedAt, checkedAt, Now(), id)
	return err
}

// MarkPBAuthFailure increments the consecutive failure counter.
func (s *Store) MarkPBAuthFailure(ctx context.Context, id, errText string) (int, error) {
	if _, err := s.db.ExecContext(ctx, `UPDATE instances SET
		pb_superuser_failures=pb_superuser_failures+1, pb_auth_status='error', pb_auth_checked_at=?,
		pb_auth_error=?, pb_superuser_reconcile_error=?, updated_at=? WHERE id=?`,
		Now(), errText, errText, Now(), id); err != nil {
		return 0, err
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT pb_superuser_failures FROM instances WHERE id=?`, id).Scan(&count)
	return count, err
}

// MarkPBAuthRepaired records a successful delete/recreate repair.
func (s *Store) MarkPBAuthRepaired(ctx context.Context, id, email, secret, recordID, repairedAt string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET
		pb_superuser_email=?, pb_superuser_secret=?, pb_superuser_record_id=?, pb_superuser_managed=1,
		pb_auth_status='linked', pb_auth_checked_at=?, pb_auth_error='', pb_superuser_failures=0,
		pb_superuser_last_verified_at=?, pb_superuser_last_repaired_at=?, pb_superuser_reconcile_error='', updated_at=?
		WHERE id=?`, email, secret, recordID, repairedAt, repairedAt, repairedAt, Now(), id)
	return err
}

// ClearManagedPBAuth removes manager-owned credential metadata.
func (s *Store) ClearManagedPBAuth(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET
		pb_superuser_email='', pb_superuser_secret='', pb_superuser_record_id='', pb_superuser_managed=0,
		pb_auth_status='unlinked', pb_auth_error='', pb_superuser_failures=0, pb_superuser_reconcile_error='',
		pb_superuser_last_verified_at='', pb_superuser_last_repaired_at='', updated_at=? WHERE id=?`, Now(), id)
	return err
}

// ListManagedInstances returns instances whose application credential is owned
// by the manager.
func (s *Store) ListManagedInstances(ctx context.Context) ([]Instance, error) {
	all, err := s.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	var out []Instance
	for _, inst := range all {
		if inst.PBSuperuserManaged {
			out = append(out, inst)
		}
	}
	return out, nil
}

// SetApplyState records successful SMTP/S3 application.
func (s *Store) SetApplyState(ctx context.Context, id, kind, status, appliedAt, errText string) error {
	var err error
	switch kind {
	case "smtp":
		_, err = s.db.ExecContext(ctx, `UPDATE instances SET smtp_apply_status=?, smtp_applied_at=?, updated_at=? WHERE id=?`, status, appliedAt, Now(), id)
	case "storage":
		_, err = s.db.ExecContext(ctx, `UPDATE instances SET storage_apply_status=?, storage_applied_at=?, updated_at=? WHERE id=?`, status, appliedAt, Now(), id)
	default:
		return fmt.Errorf("unknown apply kind %q", kind)
	}
	return err
}

// SetBackupSchedule updates the per-instance schedule.
func (s *Store) SetBackupSchedule(ctx context.Context, id string, enabled bool, cron string, retention int, next string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET backup_enabled=?, backup_cron=?, backup_retention=?, next_backup_at=?, updated_at=? WHERE id=?`,
		boolInt(enabled), cron, retention, next, Now(), id)
	return err
}

// RecordBackupRun updates last/next backup timestamps.
func (s *Store) RecordBackupRun(ctx context.Context, id, last, next string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET last_backup_at=?, next_backup_at=?, updated_at=? WHERE id=?`, last, next, Now(), id)
	return err
}

// SetNextBackup advances the schedule without changing last-backup metadata.
func (s *Store) SetNextBackup(ctx context.Context, id, next string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE instances SET next_backup_at=?, updated_at=? WHERE id=?`, next, Now(), id)
	return err
}

// ListDueBackups returns enabled instances whose next backup time is due.
func (s *Store) ListDueBackups(ctx context.Context) ([]Instance, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+instanceColumns+instanceFrom+`
		WHERE i.backup_enabled=1 AND i.next_backup_at<>'' AND i.next_backup_at <= ?
		ORDER BY i.next_backup_at`, Now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Instance{}
	for rows.Next() {
		v, err := scanInstance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// CountInstances returns the inventory size.
func (s *Store) CountInstances(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM instances`).Scan(&n)
	return n, err
}

const domainColumns = `d.id, d.instance_id, i.name, d.hostname, COALESCE(d.cloudflare_account_id,''),
	d.zone_id, d.zone_name, d.dns_record_id, d.proxied, d.dns_managed, d.caddy_managed, d.snippet_path,
	d.dns_status, d.caddy_status, d.tls_status, d.tls_expires_at, d.last_checked_at, d.last_error,
	d.hosted_active, d.hosted_dismissable, d.hosted_message, d.created_at, d.updated_at`

func scanDomain(sc interface{ Scan(...any) error }) (*Domain, error) {
	var v Domain
	var proxied, dnsManaged, caddyManaged, hostedActive, hostedDismissable int
	err := sc.Scan(&v.ID, &v.InstanceID, &v.InstanceName, &v.Hostname, &v.CloudflareAccountID,
		&v.ZoneID, &v.ZoneName, &v.DNSRecordID, &proxied, &dnsManaged, &caddyManaged, &v.SnippetPath,
		&v.DNSStatus, &v.CaddyStatus, &v.TLSStatus, &v.TLSExpiresAt, &v.LastCheckedAt, &v.LastError,
		&hostedActive, &hostedDismissable, &v.HostedMessage, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, err
	}
	v.Proxied = proxied != 0
	v.DNSManaged = dnsManaged != 0
	v.CaddyManaged = caddyManaged != 0
	v.HostedActive = hostedActive != 0
	v.HostedDismissable = hostedDismissable != 0
	return &v, nil
}

// CreateDomain inserts a domain mapping.
func (s *Store) CreateDomain(ctx context.Context, d *Domain) error {
	if d.ID == "" {
		d.ID = NewID()
	}
	d.HostedActive = true
	d.HostedDismissable = true
	if d.CreatedAt == "" {
		d.CreatedAt = Now()
	}
	d.UpdatedAt = Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO domains(id,instance_id,hostname,cloudflare_account_id,zone_id,zone_name,
		dns_record_id,proxied,dns_managed,caddy_managed,snippet_path,dns_status,caddy_status,tls_status,
		tls_expires_at,last_checked_at,last_error,hosted_active,hosted_dismissable,hosted_message,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.InstanceID, d.Hostname, nullIfEmpty(d.CloudflareAccountID), d.ZoneID, d.ZoneName,
		d.DNSRecordID, boolInt(d.Proxied), boolInt(d.DNSManaged), boolInt(d.CaddyManaged), d.SnippetPath,
		d.DNSStatus, d.CaddyStatus, d.TLSStatus, d.TLSExpiresAt, d.LastCheckedAt, d.LastError,
		boolInt(d.HostedActive), boolInt(d.HostedDismissable), d.HostedMessage, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("domain %q is already mapped to an instance", d.Hostname)
		}
		return err
	}
	return nil
}

// UpdateDomain updates mutable DNS/Caddy association fields.
func (s *Store) UpdateDomain(ctx context.Context, d *Domain) error {
	d.UpdatedAt = Now()
	res, err := s.db.ExecContext(ctx, `UPDATE domains SET instance_id=?, hostname=?, cloudflare_account_id=?, zone_id=?,
		zone_name=?, dns_record_id=?, proxied=?, dns_managed=?, caddy_managed=?, snippet_path=?, dns_status=?,
		caddy_status=?, tls_status=?, tls_expires_at=?, last_checked_at=?, last_error=?, hosted_active=?,
		hosted_dismissable=?, hosted_message=?, updated_at=? WHERE id=?`,
		d.InstanceID, d.Hostname, nullIfEmpty(d.CloudflareAccountID), d.ZoneID, d.ZoneName, d.DNSRecordID,
		boolInt(d.Proxied), boolInt(d.DNSManaged), boolInt(d.CaddyManaged), d.SnippetPath, d.DNSStatus,
		d.CaddyStatus, d.TLSStatus, d.TLSExpiresAt, d.LastCheckedAt, d.LastError,
		boolInt(d.HostedActive), boolInt(d.HostedDismissable), d.HostedMessage, d.UpdatedAt, d.ID)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("domain %q is already mapped to an instance", d.Hostname)
		}
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetDomain returns one domain mapping.
func (s *Store) GetDomain(ctx context.Context, id string) (*Domain, error) {
	v, err := scanDomain(s.db.QueryRowContext(ctx, `SELECT `+domainColumns+` FROM domains d JOIN instances i ON i.id=d.instance_id WHERE d.id=?`, id))
	return v, notFound(err)
}

// GetDomainByHostname returns the mapping for a hostname.
func (s *Store) GetDomainByHostname(ctx context.Context, hostname string) (*Domain, error) {
	v, err := scanDomain(s.db.QueryRowContext(ctx, `SELECT `+domainColumns+` FROM domains d JOIN instances i ON i.id=d.instance_id WHERE d.hostname=? COLLATE NOCASE`, hostname))
	return v, notFound(err)
}

// ListDomains returns all mappings.
func (s *Store) ListDomains(ctx context.Context) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainColumns+` FROM domains d JOIN instances i ON i.id=d.instance_id ORDER BY d.hostname COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		v, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// ListDomainsByInstance returns mappings for one instance.
func (s *Store) ListDomainsByInstance(ctx context.Context, instanceID string) ([]Domain, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+domainColumns+` FROM domains d JOIN instances i ON i.id=d.instance_id WHERE d.instance_id=? ORDER BY d.hostname`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		v, err := scanDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// DeleteDomain removes a mapping record.
func (s *Store) DeleteDomain(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM domains WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetDomainStatus(ctx context.Context, id string, active, dismissable bool, message string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE domains SET hosted_active=?, hosted_dismissable=?, hosted_message=?, updated_at=? WHERE id=?`,
		boolInt(active), boolInt(dismissable), message, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateDomainChecks persists DNS/Caddy/TLS observations.
func (s *Store) UpdateDomainChecks(ctx context.Context, id, dnsStatus, caddyStatus, tlsStatus, tlsExpiry, checkedAt, errText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE domains SET dns_status=?, caddy_status=?, tls_status=?, tls_expires_at=?, last_checked_at=?, last_error=?, updated_at=? WHERE id=?`,
		dnsStatus, caddyStatus, tlsStatus, tlsExpiry, checkedAt, errText, Now(), id)
	return err
}

// ClearDomainError resets the error after a successful operation.
func (s *Store) ClearDomainError(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE domains SET last_error='', updated_at=? WHERE id=?`, Now(), id)
	return err
}

// InstanceCounts returns status counts for the overview.
func (s *Store) InstanceCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM instances GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// Ensure sql import is used even when build tags vary.
var _ = sql.ErrNoRows
