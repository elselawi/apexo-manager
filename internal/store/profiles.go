package store

import (
	"context"
	"strings"
)

const smtpColumns = `p.id, p.name, p.host, p.port, p.username, p.password, p.auth_method, p.tls_mode,
	p.local_name, p.from_address, p.from_name, p.status, p.last_tested_at, p.last_error, p.created_at, p.updated_at,
	(SELECT COUNT(*) FROM instances i WHERE i.smtp_profile_id=p.id)`

func scanSMTP(sc interface{ Scan(...any) error }) (*SMTPProfile, error) {
	var v SMTPProfile
	err := sc.Scan(&v.ID, &v.Name, &v.Host, &v.Port, &v.Username, &v.Password, &v.AuthMethod, &v.TLSMode,
		&v.LocalName, &v.FromAddress, &v.FromName, &v.Status, &v.LastTestedAt, &v.LastError,
		&v.CreatedAt, &v.UpdatedAt, &v.UsageCount)
	if err != nil {
		return nil, err
	}
	v.HasPassword = v.Password != ""
	return &v, nil
}

// CreateSMTPProfile stores an SMTP profile. Password must already be encrypted.
func (s *Store) CreateSMTPProfile(ctx context.Context, p *SMTPProfile) error {
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.CreatedAt == "" {
		p.CreatedAt = Now()
	}
	p.UpdatedAt = Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO smtp_profiles(id,name,host,port,username,password,auth_method,tls_mode,
		local_name,from_address,from_name,status,last_tested_at,last_error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Host, p.Port, p.Username, p.Password, p.AuthMethod, p.TLSMode,
		p.LocalName, p.FromAddress, p.FromName, p.Status, p.LastTestedAt, p.LastError, p.CreatedAt, p.UpdatedAt)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrDuplicate("SMTP profile", p.Name)
	}
	return err
}

// UpdateSMTPProfile updates all fields.
func (s *Store) UpdateSMTPProfile(ctx context.Context, p *SMTPProfile) error {
	p.UpdatedAt = Now()
	res, err := s.db.ExecContext(ctx, `UPDATE smtp_profiles SET name=?,host=?,port=?,username=?,password=?,auth_method=?,
		tls_mode=?,local_name=?,from_address=?,from_name=?,status=?,last_tested_at=?,last_error=?,updated_at=? WHERE id=?`,
		p.Name, p.Host, p.Port, p.Username, p.Password, p.AuthMethod, p.TLSMode, p.LocalName,
		p.FromAddress, p.FromName, p.Status, p.LastTestedAt, p.LastError, p.UpdatedAt, p.ID)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrDuplicate("SMTP profile", p.Name)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetSMTPProfile loads a profile.
func (s *Store) GetSMTPProfile(ctx context.Context, id string) (*SMTPProfile, error) {
	v, err := scanSMTP(s.db.QueryRowContext(ctx, `SELECT `+smtpColumns+` FROM smtp_profiles p WHERE p.id=?`, id))
	return v, notFound(err)
}

// ListSMTPProfiles lists all profiles.
func (s *Store) ListSMTPProfiles(ctx context.Context) ([]SMTPProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+smtpColumns+` FROM smtp_profiles p ORDER BY p.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SMTPProfile{}
	for rows.Next() {
		v, err := scanSMTP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// DeleteSMTPProfile removes the profile; assignments become unset.
func (s *Store) DeleteSMTPProfile(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM smtp_profiles WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetSMTPTestState records test outcome.
func (s *Store) SetSMTPTestState(ctx context.Context, id, status, testedAt, errText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE smtp_profiles SET status=?, last_tested_at=?, last_error=?, updated_at=? WHERE id=?`,
		status, testedAt, errText, Now(), id)
	return err
}

const storageColumns = `p.id,p.name,p.provider,p.endpoint,p.region,p.bucket,p.access_key,p.secret_key,p.force_path_style,
	p.status,p.last_tested_at,p.last_error,p.created_at,p.updated_at,
	(SELECT COUNT(*) FROM instances i WHERE i.storage_profile_id=p.id)`

func scanStorage(sc interface{ Scan(...any) error }) (*StorageProfile, error) {
	var v StorageProfile
	var forcePath int
	err := sc.Scan(&v.ID, &v.Name, &v.Provider, &v.Endpoint, &v.Region, &v.Bucket, &v.AccessKey, &v.SecretKey,
		&forcePath, &v.Status, &v.LastTestedAt, &v.LastError, &v.CreatedAt, &v.UpdatedAt, &v.UsageCount)
	if err != nil {
		return nil, err
	}
	v.ForcePathStyle = forcePath != 0
	v.HasSecret = v.SecretKey != ""
	return &v, nil
}

// CreateStorageProfile inserts an S3-compatible profile.
func (s *Store) CreateStorageProfile(ctx context.Context, p *StorageProfile) error {
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.CreatedAt == "" {
		p.CreatedAt = Now()
	}
	p.UpdatedAt = Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO storage_profiles(id,name,provider,endpoint,region,bucket,access_key,secret_key,
		force_path_style,status,last_tested_at,last_error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.Provider, p.Endpoint, p.Region, p.Bucket, p.AccessKey, p.SecretKey,
		boolInt(p.ForcePathStyle), p.Status, p.LastTestedAt, p.LastError, p.CreatedAt, p.UpdatedAt)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrDuplicate("storage profile", p.Name)
	}
	return err
}

// UpdateStorageProfile updates all fields.
func (s *Store) UpdateStorageProfile(ctx context.Context, p *StorageProfile) error {
	p.UpdatedAt = Now()
	res, err := s.db.ExecContext(ctx, `UPDATE storage_profiles SET name=?,provider=?,endpoint=?,region=?,bucket=?,access_key=?,
		secret_key=?,force_path_style=?,status=?,last_tested_at=?,last_error=?,updated_at=? WHERE id=?`,
		p.Name, p.Provider, p.Endpoint, p.Region, p.Bucket, p.AccessKey, p.SecretKey, boolInt(p.ForcePathStyle),
		p.Status, p.LastTestedAt, p.LastError, p.UpdatedAt, p.ID)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrDuplicate("storage profile", p.Name)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetStorageProfile loads a profile.
func (s *Store) GetStorageProfile(ctx context.Context, id string) (*StorageProfile, error) {
	v, err := scanStorage(s.db.QueryRowContext(ctx, `SELECT `+storageColumns+` FROM storage_profiles p WHERE p.id=?`, id))
	return v, notFound(err)
}

// ListStorageProfiles lists all profiles.
func (s *Store) ListStorageProfiles(ctx context.Context) ([]StorageProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+storageColumns+` FROM storage_profiles p ORDER BY p.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StorageProfile{}
	for rows.Next() {
		v, err := scanStorage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// DeleteStorageProfile removes a profile.
func (s *Store) DeleteStorageProfile(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM storage_profiles WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStorageTestState records test outcome.
func (s *Store) SetStorageTestState(ctx context.Context, id, status, testedAt, errText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE storage_profiles SET status=?, last_tested_at=?, last_error=?, updated_at=? WHERE id=?`,
		status, testedAt, errText, Now(), id)
	return err
}

const cfColumns = `id,name,api_token,account_id,status,last_validated_at,last_error,created_at,updated_at`

func scanCF(sc interface{ Scan(...any) error }) (*CloudflareAccount, error) {
	var v CloudflareAccount
	err := sc.Scan(&v.ID, &v.Name, &v.APIToken, &v.AccountID, &v.Status, &v.LastValidatedAt, &v.LastError, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, err
	}
	v.HasToken = v.APIToken != ""
	return &v, nil
}

// CreateCloudflareAccount stores a Cloudflare account with an encrypted token.
func (s *Store) CreateCloudflareAccount(ctx context.Context, a *CloudflareAccount) error {
	if a.ID == "" {
		a.ID = NewID()
	}
	if a.CreatedAt == "" {
		a.CreatedAt = Now()
	}
	a.UpdatedAt = Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO cloudflare_accounts(id,name,api_token,account_id,status,last_validated_at,last_error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, a.ID, a.Name, a.APIToken, a.AccountID, a.Status, a.LastValidatedAt, a.LastError, a.CreatedAt, a.UpdatedAt)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrDuplicate("Cloudflare account", a.Name)
	}
	return err
}

// UpdateCloudflareAccount updates name/token/account id.
func (s *Store) UpdateCloudflareAccount(ctx context.Context, a *CloudflareAccount) error {
	a.UpdatedAt = Now()
	res, err := s.db.ExecContext(ctx, `UPDATE cloudflare_accounts SET name=?,api_token=?,account_id=?,status=?,last_validated_at=?,last_error=?,updated_at=? WHERE id=?`,
		a.Name, a.APIToken, a.AccountID, a.Status, a.LastValidatedAt, a.LastError, a.UpdatedAt, a.ID)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return ErrDuplicate("Cloudflare account", a.Name)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetCloudflareAccount loads an account.
func (s *Store) GetCloudflareAccount(ctx context.Context, id string) (*CloudflareAccount, error) {
	v, err := scanCF(s.db.QueryRowContext(ctx, `SELECT `+cfColumns+` FROM cloudflare_accounts WHERE id=?`, id))
	return v, notFound(err)
}

// ListCloudflareAccounts lists accounts.
func (s *Store) ListCloudflareAccounts(ctx context.Context) ([]CloudflareAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+cfColumns+` FROM cloudflare_accounts ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CloudflareAccount{}
	for rows.Next() {
		v, err := scanCF(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// DeleteCloudflareAccount removes an account.
func (s *Store) DeleteCloudflareAccount(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM cloudflare_accounts WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCloudflareValidation records token validation state.
func (s *Store) SetCloudflareValidation(ctx context.Context, id, status, at, errText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE cloudflare_accounts SET status=?, last_validated_at=?, last_error=?, updated_at=? WHERE id=?`,
		status, at, errText, Now(), id)
	return err
}
