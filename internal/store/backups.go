package store

import (
	"context"
	"database/sql"
	"strings"
)

const backupColumns = `b.id,b.instance_id,i.name,b.job_id,b.method,b.filename,b.path,b.size,b.sha256,b.status,b.error,b.note,b.automatic,b.verified_at,b.restored_at,b.created_at`

func scanBackup(sc interface{ Scan(...any) error }) (*Backup, error) {
	var v Backup
	var automatic int
	err := sc.Scan(&v.ID, &v.InstanceID, &v.InstanceName, &v.JobID, &v.Method, &v.Filename, &v.Path,
		&v.Size, &v.SHA256, &v.Status, &v.Error, &v.Note, &automatic, &v.VerifiedAt, &v.RestoredAt, &v.CreatedAt)
	if err != nil {
		return nil, err
	}
	v.Automatic = automatic != 0
	return &v, nil
}

// CreateBackup inserts a backup record.
func (s *Store) CreateBackup(ctx context.Context, b *Backup) error {
	if b.ID == "" {
		b.ID = NewID()
	}
	if b.CreatedAt == "" {
		b.CreatedAt = Now()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO backups(id,instance_id,job_id,method,filename,path,size,sha256,status,error,note,automatic,verified_at,restored_at,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.InstanceID, b.JobID, b.Method, b.Filename, b.Path, b.Size, b.SHA256, b.Status, b.Error, b.Note,
		boolInt(b.Automatic), b.VerifiedAt, b.RestoredAt, b.CreatedAt)
	return err
}

// UpdateBackup updates status and artifact metadata.
func (s *Store) UpdateBackup(ctx context.Context, b *Backup) error {
	res, err := s.db.ExecContext(ctx, `UPDATE backups SET method=?,filename=?,path=?,size=?,sha256=?,status=?,error=?,note=?,
		automatic=?,verified_at=?,restored_at=? WHERE id=?`,
		b.Method, b.Filename, b.Path, b.Size, b.SHA256, b.Status, b.Error, b.Note,
		boolInt(b.Automatic), b.VerifiedAt, b.RestoredAt, b.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetBackup loads a backup.
func (s *Store) GetBackup(ctx context.Context, id string) (*Backup, error) {
	v, err := scanBackup(s.db.QueryRowContext(ctx, `SELECT `+backupColumns+` FROM backups b JOIN instances i ON i.id=b.instance_id WHERE b.id=?`, id))
	return v, notFound(err)
}

// ListBackups returns backups, optionally for one instance.
func (s *Store) ListBackups(ctx context.Context, instanceID string, limit int) ([]Backup, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var rows *sql.Rows
	var err error
	if instanceID != "" {
		rows, err = s.db.QueryContext(ctx, `SELECT `+backupColumns+` FROM backups b JOIN instances i ON i.id=b.instance_id WHERE b.instance_id=? ORDER BY b.created_at DESC LIMIT ?`, instanceID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT `+backupColumns+` FROM backups b JOIN instances i ON i.id=b.instance_id ORDER BY b.created_at DESC LIMIT ?`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Backup{}
	for rows.Next() {
		v, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// ListBackupsForRetention returns completed backups for one instance, newest first.
func (s *Store) ListBackupsForRetention(ctx context.Context, instanceID string) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+backupColumns+` FROM backups b JOIN instances i ON i.id=b.instance_id
		WHERE b.instance_id=? AND b.status IN ('completed','verified') ORDER BY b.created_at DESC`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Backup{}
	for rows.Next() {
		v, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// SetBackupVerified records integrity verification.
func (s *Store) SetBackupVerified(ctx context.Context, id, status, at, errText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backups SET status=?, verified_at=?, error=? WHERE id=?`, status, at, errText, id)
	return err
}

// SetBackupRestored records a successful restore.
func (s *Store) SetBackupRestored(ctx context.Context, id, at string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE backups SET restored_at=? WHERE id=?`, at, id)
	return err
}

// DeleteBackup removes a record.
func (s *Store) DeleteBackup(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM backups WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// LastSuccessfulBackup returns the newest completed backup for an instance.
func (s *Store) LastSuccessfulBackup(ctx context.Context, instanceID string) (*Backup, error) {
	v, err := scanBackup(s.db.QueryRowContext(ctx, `SELECT `+backupColumns+` FROM backups b JOIN instances i ON i.id=b.instance_id
		WHERE b.instance_id=? AND b.status IN ('completed','verified') ORDER BY b.created_at DESC LIMIT 1`, instanceID))
	return v, notFound(err)
}

// BackupStats returns total count, bytes, and last successful backup time.
func (s *Store) BackupStats(ctx context.Context) (count int, totalBytes int64, lastAt string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size),0), COALESCE(MAX(created_at),'') FROM backups WHERE status IN ('completed','verified')`).
		Scan(&count, &totalBytes, &lastAt)
	return
}

// CountBackups returns the total number of backup records.
func (s *Store) CountBackups(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backups`).Scan(&n)
	return n, err
}

// ActiveBackupsForInstance counts creating/restoring backups.
func (s *Store) ActiveBackupsForInstance(ctx context.Context, instanceID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backups WHERE instance_id=? AND status IN ('creating','restoring')`, instanceID).Scan(&n)
	return n, err
}

// SearchBackupFiles finds backups whose filename contains a query.
func (s *Store) SearchBackupFiles(ctx context.Context, query string) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+backupColumns+` FROM backups b JOIN instances i ON i.id=b.instance_id
		WHERE b.filename LIKE ? ORDER BY b.created_at DESC LIMIT 50`, "%"+strings.ReplaceAll(query, "%", "")+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Backup{}
	for rows.Next() {
		v, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}
