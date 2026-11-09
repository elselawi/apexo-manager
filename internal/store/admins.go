package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const adminColumns = `id, username, display_name, password_hash, role, disabled, created_at, updated_at, last_login_at`

func scanAdmin(sc interface{ Scan(...any) error }) (*Admin, error) {
	var a Admin
	var disabled int
	err := sc.Scan(&a.ID, &a.Username, &a.DisplayName, &a.PasswordHash, &a.Role, &disabled, &a.CreatedAt, &a.UpdatedAt, &a.LastLoginAt)
	if err != nil {
		return nil, err
	}
	a.Disabled = disabled != 0
	return &a, nil
}

// CreateAdmin inserts an administrator. The caller must hash the password.
func (s *Store) CreateAdmin(ctx context.Context, a *Admin) error {
	if a.ID == "" {
		a.ID = NewID()
	}
	if a.CreatedAt == "" {
		a.CreatedAt = Now()
	}
	a.UpdatedAt = Now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO admins(id, username, display_name, password_hash, role, disabled, created_at, updated_at, last_login_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, a.ID, a.Username, a.DisplayName, a.PasswordHash, a.Role, boolInt(a.Disabled), a.CreatedAt, a.UpdatedAt, a.LastLoginAt)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return fmt.Errorf("administrator %q already exists", a.Username)
		}
		return err
	}
	return nil
}

// GetAdminByID returns an administrator or ErrNotFound.
func (s *Store) GetAdminByID(ctx context.Context, id string) (*Admin, error) {
	a, err := scanAdmin(s.db.QueryRowContext(ctx, `SELECT `+adminColumns+` FROM admins WHERE id = ?`, id))
	return a, notFound(err)
}

// GetAdminByUsername performs a case-insensitive lookup.
func (s *Store) GetAdminByUsername(ctx context.Context, username string) (*Admin, error) {
	a, err := scanAdmin(s.db.QueryRowContext(ctx, `SELECT `+adminColumns+` FROM admins WHERE username = ? COLLATE NOCASE`, username))
	return a, notFound(err)
}

// ListAdmins returns all administrators ordered by username.
func (s *Store) ListAdmins(ctx context.Context) ([]Admin, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+adminColumns+` FROM admins ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Admin{}
	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// UpdateAdmin updates mutable profile fields.
func (s *Store) UpdateAdmin(ctx context.Context, id, displayName, role string, disabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE admins SET display_name=?, role=?, disabled=?, updated_at=? WHERE id=?`,
		displayName, role, boolInt(disabled), Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetAdminPassword replaces the password hash and bumps updated_at.
func (s *Store) SetAdminPassword(ctx context.Context, id, hash string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE admins SET password_hash=?, updated_at=? WHERE id=?`, hash, Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAdmin removes an administrator. Sessions cascade.
func (s *Store) DeleteAdmin(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM admins WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountAdmins counts enabled administrators.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins WHERE disabled=0`).Scan(&n)
	return n, err
}

// CountOwners counts enabled owners.
func (s *Store) CountOwners(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admins WHERE disabled=0 AND role='owner'`).Scan(&n)
	return n, err
}

// TouchAdminLogin records successful login time.
func (s *Store) TouchAdminLogin(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE admins SET last_login_at=?, updated_at=? WHERE id=?`, Now(), Now(), id)
	return err
}

// SetAdminDisabled toggles account state.
func (s *Store) SetAdminDisabled(ctx context.Context, id string, disabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE admins SET disabled=?, updated_at=? WHERE id=?`, boolInt(disabled), Now(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
