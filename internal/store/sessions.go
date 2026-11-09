package store

import (
	"context"
	"database/sql"
	"time"
)

const sessionSelect = `SELECT s.id, s.admin_id, s.token_hash, s.csrf_token, s.created_at, s.expires_at,
	s.last_seen_at, s.ip, s.user_agent, s.revoked_at,
	a.display_name, a.username, a.role
	FROM sessions s JOIN admins a ON a.id = s.admin_id`

func scanSession(sc interface{ Scan(...any) error }) (*Session, error) {
	var v Session
	var revoked sql.NullString
	err := sc.Scan(&v.ID, &v.AdminID, &v.TokenHash, &v.CSRFToken, &v.CreatedAt, &v.ExpiresAt,
		&v.LastSeenAt, &v.IP, &v.UserAgent, &revoked, &v.AdminName, &v.AdminUser, &v.AdminRole)
	if err != nil {
		return nil, err
	}
	if revoked.Valid {
		v.RevokedAt = revoked.String
	}
	return &v, nil
}

// CreateSession stores a hashed session token.
func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	if sess.ID == "" {
		sess.ID = NewID()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id, admin_id, token_hash, csrf_token, created_at, expires_at, last_seen_at, ip, user_agent, revoked_at)
		VALUES(?,?,?,?,?,?,?,?,?,'')`,
		sess.ID, sess.AdminID, sess.TokenHash, sess.CSRFToken, sess.CreatedAt, sess.ExpiresAt, sess.LastSeenAt, sess.IP, sess.UserAgent)
	return err
}

// GetSessionByTokenHash loads a session (even expired) for validation.
func (s *Store) GetSessionByTokenHash(ctx context.Context, hash string) (*Session, error) {
	sess, err := scanSession(s.db.QueryRowContext(ctx, sessionSelect+` WHERE s.token_hash = ?`, hash))
	return sess, notFound(err)
}

// GetSession returns a session by ID.
func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	sess, err := scanSession(s.db.QueryRowContext(ctx, sessionSelect+` WHERE s.id = ?`, id))
	return sess, notFound(err)
}

// TouchSession updates the idle timestamp.
func (s *Store) TouchSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at=? WHERE id=?`, Now(), id)
	return err
}

// RevokeSession invalidates a single session.
func (s *Store) RevokeSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE id=? AND revoked_at=''`, Now(), id)
	return err
}

// RevokeSessionByTokenHash invalidates the session matching a token hash.
func (s *Store) RevokeSessionByTokenHash(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE token_hash=? AND revoked_at=''`, Now(), hash)
	return err
}

// RevokeAdminSessions invalidates all of an administrator's sessions, optionally
// preserving one session ID.
func (s *Store) RevokeAdminSessions(ctx context.Context, adminID, exceptID string) error {
	if exceptID != "" {
		_, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE admin_id=? AND id<>? AND revoked_at=''`, Now(), adminID, exceptID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET revoked_at=? WHERE admin_id=? AND revoked_at=''`, Now(), adminID)
	return err
}

// ListSessions returns a user's sessions, including revoked history.
func (s *Store) ListSessions(ctx context.Context, adminID string) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, sessionSelect+` WHERE s.admin_id=? ORDER BY s.created_at DESC LIMIT 50`, adminID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		v, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// CleanupSessions deletes expired/revoked rows older than the retention cutoff.
func (s *Store) CleanupSessions(ctx context.Context, cutoff time.Time) (int64, error) {
	now := FormatTime(time.Now().UTC())
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ? OR (revoked_at <> '' AND revoked_at < ?)`, now, FormatTime(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
