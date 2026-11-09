package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/auth"
	"github.com/apexo/pbmanager/internal/store"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		writeError(w, r, http.StatusForbidden, "origin_failed", "Cross-origin login refused", nil)
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	ip := clientIP(r, s.App.Config.TrustProxy)
	res, err := s.App.Auth.Login(r.Context(), in.Username, in.Password, ip, r.UserAgent())
	if err != nil {
		_ = s.App.Store.CreateAudit(r.Context(), &store.AuditEntry{
			ActorType: "anonymous", ActorName: strings.TrimSpace(in.Username), Action: "auth.login_failed",
			IP: ip, RequestID: requestIDFrom(r), Detail: fmt.Sprintf(`{"reason":%q}`, truncate(err.Error(), 160)),
		})
		if errors.Is(err, auth.ErrRateLimited) {
			writeError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again later.", nil)
			return
		}
		if errors.Is(err, auth.ErrAccountDisabled) {
			writeError(w, r, http.StatusForbidden, "account_disabled", "This administrator account is disabled", nil)
			return
		}
		writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "Invalid username or password", nil)
		return
	}
	s.setSessionCookie(w, res.Token, res.ExpiresAt)
	_ = s.App.Store.CreateAudit(r.Context(), &store.AuditEntry{
		ActorType: "admin", ActorID: res.Admin.ID, ActorName: res.Admin.Username, Action: "auth.login",
		IP: ip, RequestID: requestIDFrom(r), Detail: `{}`,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"admin": res.Admin, "csrfToken": res.CSRF, "expiresAt": store.FormatTime(res.ExpiresAt),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r)
	token := sessionToken(r)
	_ = s.App.Auth.Logout(r.Context(), token)
	clearSessionCookie(w, s.App.Config.SecureCookies)
	s.audit(r, "auth.logout", "admin", a.ID, map[string]any{})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r)
	sess := sessionFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"admin": a, "csrfToken": sess.CSRFToken, "expiresAt": sess.ExpiresAt,
		"sessionId": sess.ID, "serverTime": time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleOwnSessions(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r)
	sessions, err := s.App.Auth.Sessions(r.Context(), a.ID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list sessions", nil)
		return
	}
	current := sessionFrom(r)
	writeJSON(w, http.StatusOK, map[string]any{"items": sessions, "currentSessionId": current.ID})
}

func (s *Server) handleRevokeOwnSession(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r)
	id := r.PathValue("id")
	sess, err := s.App.Store.GetSession(r.Context(), id)
	if notFoundOr(w, r, err, "Session") {
		return
	}
	if sess.AdminID != a.ID {
		writeError(w, r, http.StatusForbidden, "forbidden", "That session belongs to another administrator", nil)
		return
	}
	if err := s.App.Store.RevokeSession(r.Context(), id); err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not revoke session", nil)
		return
	}
	s.audit(r, "auth.session_revoked", "session", id, map[string]any{})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	a := adminFrom(r)
	var in struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if err := s.App.Auth.ChangePassword(r.Context(), a.ID, in.CurrentPassword, in.NewPassword); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeError(w, r, http.StatusBadRequest, "invalid_password", "Current password is incorrect", nil)
			return
		}
		writeError(w, r, http.StatusBadRequest, "password_change_failed", err.Error(), nil)
		return
	}
	current := sessionFrom(r)
	_ = s.App.Auth.RevokeAdminSessions(r.Context(), a.ID, current.ID)
	s.audit(r, "auth.password_changed", "admin", a.ID, map[string]any{})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Password changed; other sessions were revoked"})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
