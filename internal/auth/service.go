package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/store"
)

var (
	ErrLastOwner    = errors.New("cannot remove or disable the last enabled owner")
	ErrRateLimited  = errors.New("too many login attempts; try again later")
	ErrInvalidToken = errors.New("invalid session token")
)

// LoginResult carries the raw session token (cookie value) and CSRF token.
type LoginResult struct {
	Admin     *store.Admin
	Token     string
	CSRF      string
	ExpiresAt time.Time
}

// Service coordinates password verification and sessions.
type Service struct {
	store   *store.Store
	ttl     time.Duration
	limiter *LoginLimiter
	now     func() time.Time
}

// NewService creates an auth service.
func NewService(st *store.Store, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &Service{store: st, ttl: ttl, limiter: NewLoginLimiter(), now: time.Now}
}

// Limiter returns the login rate limiter.
func (s *Service) Limiter() *LoginLimiter { return s.limiter }

// TTL returns the absolute session lifetime.
func (s *Service) TTL() time.Duration { return s.ttl }

// CreateAdmin validates fields, hashes the password, and stores an admin.
func (s *Service) CreateAdmin(ctx context.Context, username, displayName, password, role string) (*store.Admin, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if err := ValidateUsername(username); err != nil {
		return nil, err
	}
	if !ValidRole(role) {
		return nil, fmt.Errorf("role must be owner, admin, or viewer")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	if displayName == "" {
		displayName = username
	}
	a := &store.Admin{
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: hash,
		Role:         role,
	}
	if err := s.store.CreateAdmin(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// Login verifies credentials and creates a new session.
func (s *Service) Login(ctx context.Context, username, password, ip, userAgent string) (*LoginResult, error) {
	if ok, retry := s.limiter.Allow(ip); !ok {
		return nil, fmt.Errorf("%w (retry in %s)", ErrRateLimited, retry.Round(time.Second))
	}
	username = strings.TrimSpace(username)
	if username == "" || password == "" || len(password) > 1024 {
		s.limiter.Failure(ip)
		return nil, ErrInvalidCredentials
	}
	a, err := s.store.GetAdminByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_, _ = VerifyPassword(dummyHash, password)
			s.limiter.Failure(ip)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	ok, err := VerifyPassword(a.PasswordHash, password)
	if err != nil || !ok {
		s.limiter.Failure(ip)
		return nil, ErrInvalidCredentials
	}
	if a.Disabled {
		s.limiter.Failure(ip)
		return nil, ErrAccountDisabled
	}
	token, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	sess := &store.Session{
		AdminID:    a.ID,
		TokenHash:  HashToken(token),
		CSRFToken:  csrf,
		CreatedAt:  store.FormatTime(now),
		ExpiresAt:  store.FormatTime(now.Add(s.ttl)),
		LastSeenAt: store.FormatTime(now),
		IP:         ip,
		UserAgent:  truncate(userAgent, 300),
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	if err := s.store.TouchAdminLogin(ctx, a.ID); err != nil {
		return nil, err
	}
	s.limiter.Success(ip)
	a.LastLoginAt = store.FormatTime(now)
	return &LoginResult{Admin: a, Token: token, CSRF: csrf, ExpiresAt: now.Add(s.ttl)}, nil
}

// Authenticate resolves a raw cookie token. It writes the session last-seen
// timestamp, so callers should use it once per request.
func (s *Service) Authenticate(ctx context.Context, token string) (*store.Session, *store.Admin, error) {
	if token == "" || len(token) > 512 {
		return nil, nil, ErrInvalidToken
	}
	sess, err := s.store.GetSessionByTokenHash(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, ErrInvalidToken
		}
		return nil, nil, err
	}
	if sess.RevokedAt != "" {
		return nil, nil, ErrSessionExpired
	}
	if exp := store.ParseTime(sess.ExpiresAt); exp.IsZero() || s.now().UTC().After(exp) {
		return nil, nil, ErrSessionExpired
	}
	a, err := s.store.GetAdminByID(ctx, sess.AdminID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, ErrInvalidToken
		}
		return nil, nil, err
	}
	if a.Disabled {
		return nil, nil, ErrAccountDisabled
	}
	if err := s.store.TouchSession(ctx, sess.ID); err != nil {
		return nil, nil, err
	}
	sess.Admin = a
	return sess, a, nil
}

// Logout revokes the current token.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.RevokeSessionByTokenHash(ctx, HashToken(token))
}

// AdminByID returns an administrator.
func (s *Service) AdminByID(ctx context.Context, id string) (*store.Admin, error) {
	return s.store.GetAdminByID(ctx, id)
}

// AdminByUsername returns an administrator.
func (s *Service) AdminByUsername(ctx context.Context, username string) (*store.Admin, error) {
	return s.store.GetAdminByUsername(ctx, username)
}

// ListAdmins lists dashboard administrators.
func (s *Service) ListAdmins(ctx context.Context) ([]store.Admin, error) {
	return s.store.ListAdmins(ctx)
}

// ResetPassword changes a password by administrator ID. The root CLI uses this
// without knowing the old password. API self-service requires the old password.
func (s *Service) ResetPassword(ctx context.Context, adminID, newPassword string) error {
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	return s.store.SetAdminPassword(ctx, adminID, hash)
}

// ChangePassword verifies the old password first, then resets.
func (s *Service) ChangePassword(ctx context.Context, adminID, oldPassword, newPassword string) error {
	a, err := s.store.GetAdminByID(ctx, adminID)
	if err != nil {
		return err
	}
	ok, err := VerifyPassword(a.PasswordHash, oldPassword)
	if err != nil || !ok {
		return ErrInvalidCredentials
	}
	return s.ResetPassword(ctx, adminID, newPassword)
}

// UpdateAdmin mutates display name, role, and disabled state while protecting
// the last enabled owner.
func (s *Service) UpdateAdmin(ctx context.Context, id, displayName, role string, disabled bool) error {
	if !ValidRole(role) {
		return fmt.Errorf("role must be owner, admin, or viewer")
	}
	target, err := s.store.GetAdminByID(ctx, id)
	if err != nil {
		return err
	}
	losingOwner := target.Role == "owner" && !target.Disabled && (role != "owner" || disabled)
	if losingOwner {
		owners, err := s.store.CountOwners(ctx)
		if err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	if err := s.store.UpdateAdmin(ctx, id, strings.TrimSpace(displayName), role, disabled); err != nil {
		return err
	}
	if disabled || role != target.Role {
		_ = s.store.RevokeAdminSessions(ctx, id, "")
	}
	return nil
}

// DeleteAdmin removes an administrator while protecting the last owner.
func (s *Service) DeleteAdmin(ctx context.Context, id string) error {
	target, err := s.store.GetAdminByID(ctx, id)
	if err != nil {
		return err
	}
	if target.Role == "owner" && !target.Disabled {
		owners, err := s.store.CountOwners(ctx)
		if err != nil {
			return err
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	return s.store.DeleteAdmin(ctx, id)
}

// RevokeAdminSessions invalidates every session for an administrator.
func (s *Service) RevokeAdminSessions(ctx context.Context, adminID, exceptSessionID string) error {
	return s.store.RevokeAdminSessions(ctx, adminID, exceptSessionID)
}

// Sessions lists an administrator's sessions.
func (s *Service) Sessions(ctx context.Context, adminID string) ([]store.Session, error) {
	return s.store.ListSessions(ctx, adminID)
}

// HashToken returns the SHA-256 hex digest stored in the database.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
