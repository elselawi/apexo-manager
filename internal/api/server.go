package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	appcore "github.com/apexo/pbmanager/internal/app"
	"github.com/apexo/pbmanager/internal/store"
	webassets "github.com/apexo/pbmanager/web"
)

const sessionCookie = "pbmanager_session"

type ctxKey int

const (
	ctxAdmin ctxKey = iota
	ctxSession
	ctxRequestID
)

// Server is the REST API and SPA host.
type Server struct {
	App     *appcore.App
	Log     *slog.Logger
	Version string
	mux     *http.ServeMux
	limiter *ipLimiter
}

// New constructs the HTTP server.
func New(a *appcore.App, log *slog.Logger, version string) *Server {
	if a.Version == "" || a.Version == "dev" {
		a.Version = version
	}
	s := &Server{App: a, Log: log, Version: version, mux: http.NewServeMux(), limiter: newIPLimiter(20, 60)}
	s.routes()
	return s
}

// Handler returns the middleware-wrapped handler.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = s.rateLimit(h)
	h = s.accessLog(h)
	h = s.requestID(h)
	h = s.securityHeaders(h)
	h = s.recoverer(h)
	return h
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/auth/status", s.handleAuthStatus)
	s.mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)

	s.protected("POST /api/v1/auth/logout", "viewer", s.handleLogout)
	s.protected("GET /api/v1/auth/me", "viewer", s.handleMe)
	s.protected("GET /api/v1/auth/sessions", "viewer", s.handleOwnSessions)
	s.protected("POST /api/v1/auth/change-password", "viewer", s.handleChangePassword)
	s.protected("POST /api/v1/auth/sessions/{id}/revoke", "viewer", s.handleRevokeOwnSession)

	s.protected("GET /api/v1/overview", "viewer", s.handleOverview)
	s.protected("GET /api/v1/monitoring", "viewer", s.handleMonitoring)

	s.protected("GET /api/v1/instances", "viewer", s.handleListInstances)
	s.protected("GET /api/v1/instances/defaults", "viewer", s.handleInstanceDefaults)
	s.protected("GET /api/v1/instances/discover", "viewer", s.handleDiscover)
	s.protected("POST /api/v1/instances", "admin", s.handleCreateInstance)
	s.protected("POST /api/v1/instances/import", "admin", s.handleImportInstance)
	s.protected("GET /api/v1/instances/{id}", "viewer", s.handleGetInstance)
	s.protected("PATCH /api/v1/instances/{id}", "admin", s.handleUpdateInstance)
	s.protected("DELETE /api/v1/instances/{id}", "admin", s.handleDeleteInstance)
	s.protected("POST /api/v1/instances/{id}/start", "admin", s.handleStartInstance)
	s.protected("POST /api/v1/instances/{id}/stop", "admin", s.handleStopInstance)
	s.protected("POST /api/v1/instances/{id}/restart", "admin", s.handleRestartInstance)
	s.protected("POST /api/v1/instances/{id}/update", "admin", s.handleUpdatePocketBase)
	s.protected("POST /api/v1/instances/{id}/verify", "viewer", s.handleVerifyInstance)
	s.protected("POST /api/v1/instances/{id}/sync-caddy", "admin", s.handleSyncCaddy)
	s.protected("POST /api/v1/instances/{id}/smtp/apply", "admin", s.handleApplySMTP)
	s.protected("POST /api/v1/instances/{id}/storage/apply", "admin", s.handleApplyStorage)
	s.protected("GET /api/v1/instances/{id}/logs", "viewer", s.handleInstanceLogs)
	s.protected("GET /api/v1/instances/{id}/metrics", "viewer", s.handleInstanceMetrics)
	s.protected("PUT /api/v1/instances/{id}/credentials", "admin", s.handleSetCredentials)
	s.protected("DELETE /api/v1/instances/{id}/credentials", "admin", s.handleClearCredentials)
	s.protected("POST /api/v1/instances/{id}/credentials/test", "admin", s.handleTestCredentials)
	s.protected("GET /api/v1/instances/{id}/backups", "viewer", s.handleInstanceBackups)

	s.protected("GET /api/v1/domains", "viewer", s.handleListDomains)
	s.protected("GET /api/v1/domains/{id}", "viewer", s.handleGetDomain)
	s.protected("POST /api/v1/instances/{id}/domains", "admin", s.handleAttachDomain)
	s.protected("DELETE /api/v1/domains/{id}", "admin", s.handleDetachDomain)
	s.protected("POST /api/v1/domains/{id}/check", "viewer", s.handleCheckDomain)

	s.protected("GET /api/v1/caddy", "viewer", s.handleCaddy)
	s.protected("POST /api/v1/caddy/ensure-import", "admin", s.handleCaddyEnsureImport)
	s.protected("POST /api/v1/caddy/validate", "admin", s.handleCaddyValidate)
	s.protected("GET /api/v1/caddy/logs", "viewer", s.handleCaddyLogs)
	s.protected("DELETE /api/v1/caddy/routes/{domain}", "admin", s.handleCaddyRemoveRoute)

	s.protected("GET /api/v1/cloudflare/accounts", "admin", s.handleListCloudflare)
	s.protected("POST /api/v1/cloudflare/accounts", "admin", s.handleCreateCloudflare)
	s.protected("PATCH /api/v1/cloudflare/accounts/{id}", "admin", s.handleUpdateCloudflare)
	s.protected("DELETE /api/v1/cloudflare/accounts/{id}", "admin", s.handleDeleteCloudflare)
	s.protected("POST /api/v1/cloudflare/accounts/{id}/validate", "admin", s.handleValidateCloudflare)
	s.protected("GET /api/v1/cloudflare/accounts/{id}/zones", "admin", s.handleCloudflareZones)
	s.protected("GET /api/v1/cloudflare/accounts/{id}/zones/{zone}/records", "admin", s.handleCloudflareRecords)

	s.protected("GET /api/v1/smtp-profiles", "viewer", s.handleListSMTP)
	s.protected("POST /api/v1/smtp-profiles", "admin", s.handleCreateSMTP)
	s.protected("PATCH /api/v1/smtp-profiles/{id}", "admin", s.handleUpdateSMTP)
	s.protected("DELETE /api/v1/smtp-profiles/{id}", "admin", s.handleDeleteSMTP)
	s.protected("POST /api/v1/smtp-profiles/{id}/test", "admin", s.handleTestSMTP)

	s.protected("GET /api/v1/storage-profiles", "viewer", s.handleListStorage)
	s.protected("POST /api/v1/storage-profiles", "admin", s.handleCreateStorage)
	s.protected("PATCH /api/v1/storage-profiles/{id}", "admin", s.handleUpdateStorage)
	s.protected("DELETE /api/v1/storage-profiles/{id}", "admin", s.handleDeleteStorage)
	s.protected("POST /api/v1/storage-profiles/{id}/test", "admin", s.handleTestStorage)

	s.protected("GET /api/v1/backups", "viewer", s.handleListBackups)
	s.protected("POST /api/v1/backups", "admin", s.handleCreateBackup)
	s.protected("GET /api/v1/backups/{id}", "viewer", s.handleGetBackup)
	s.protected("GET /api/v1/backups/{id}/download", "admin", s.handleDownloadBackup)
	s.protected("POST /api/v1/backups/{id}/verify", "admin", s.handleVerifyBackup)
	s.protected("POST /api/v1/backups/{id}/restore", "admin", s.handleRestoreBackup)
	s.protected("DELETE /api/v1/backups/{id}", "admin", s.handleDeleteBackup)

	s.protected("GET /api/v1/jobs", "viewer", s.handleListJobs)
	s.protected("GET /api/v1/jobs/{id}", "viewer", s.handleGetJob)
	s.protected("POST /api/v1/jobs/{id}/cancel", "admin", s.handleCancelJob)
	s.protected("POST /api/v1/jobs/{id}/retry", "admin", s.handleRetryJob)

	s.protected("GET /api/v1/audit", "viewer", s.handleAudit)
	s.protected("GET /api/v1/settings", "admin", s.handleGetSettings)
	s.protected("PUT /api/v1/settings", "owner", s.handleUpdateSettings)
	s.protected("POST /api/v1/settings/detect-ip", "owner", s.handleDetectIP)
	s.protected("GET /api/v1/system/update", "admin", s.handleCheckUpdate)
	s.protected("POST /api/v1/system/update", "owner", s.handleInstallUpdate)

	s.protected("GET /api/v1/admins", "owner", s.handleListAdmins)
	s.protected("POST /api/v1/admins", "owner", s.handleCreateAdmin)
	s.protected("PATCH /api/v1/admins/{id}", "owner", s.handleUpdateAdmin)
	s.protected("DELETE /api/v1/admins/{id}", "owner", s.handleDeleteAdmin)
	s.protected("POST /api/v1/admins/{id}/revoke-sessions", "owner", s.handleRevokeAdminSessions)

	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "API endpoint not found", nil)
	})
	s.mux.Handle("/", s.staticHandler())
}

func (s *Server) protected(pattern, minRole string, h http.HandlerFunc) {
	s.mux.HandleFunc(pattern, s.withAuth(minRole, h))
}

// withAuth resolves a session cookie, enforces CSRF on unsafe methods, and
// applies role checks.
func (s *Server) withAuth(minRole string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := sessionToken(r)
		if token == "" {
			writeError(w, r, http.StatusUnauthorized, "unauthenticated", "Sign in to continue", nil)
			return
		}
		sess, admin, err := s.App.Auth.Authenticate(r.Context(), token)
		if err != nil {
			clearSessionCookie(w, s.App.Config.SecureCookies)
			writeError(w, r, http.StatusUnauthorized, "unauthenticated", "Your session has expired; sign in again", nil)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			csrf := r.Header.Get("X-CSRF-Token")
			if csrf == "" || subtle.ConstantTimeCompare([]byte(csrf), []byte(sess.CSRFToken)) != 1 {
				writeError(w, r, http.StatusForbidden, "csrf_failed", "Missing or invalid CSRF token", nil)
				return
			}
			if !s.originAllowed(r) {
				writeError(w, r, http.StatusForbidden, "origin_failed", "Cross-origin request refused", nil)
				return
			}
		}
		if roleRank(admin.Role) < roleRank(minRole) {
			writeError(w, r, http.StatusForbidden, "forbidden", "You do not have permission to perform this action", nil)
			return
		}
		if admin.Role == "viewer" && r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, r, http.StatusForbidden, "forbidden", "Viewer accounts cannot modify infrastructure", nil)
			return
		}
		ctx := context.WithValue(r.Context(), ctxAdmin, admin)
		ctx = context.WithValue(ctx, ctxSession, sess)
		next(w, r.WithContext(ctx))
	}
}

func roleRank(role string) int {
	switch role {
	case "owner":
		return 3
	case "admin":
		return 2
	case "viewer":
		return 1
	default:
		return 0
	}
}

func adminFrom(r *http.Request) *store.Admin {
	v, _ := r.Context().Value(ctxAdmin).(*store.Admin)
	return v
}

func sessionFrom(r *http.Request) *store.Session {
	v, _ := r.Context().Value(ctxSession).(*store.Session)
	return v
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.App.Config.SecureCookies,
		SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(time.Until(expires).Seconds()),
	})
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	if len(c.Value) > 512 {
		return ""
	}
	return c.Value
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func (s *Server) originAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	allowed := map[string]bool{
		s.App.Config.Origin():                true,
		"http://" + r.Host:                   true,
		"https://" + r.Host:                  true,
		"http://localhost:" + portOf(r.Host): true,
		"http://127.0.0.1:" + portOf(r.Host): true,
	}
	return allowed[strings.TrimRight(origin, "/")]
}

func portOf(host string) string {
	_, port, err := net.SplitHostPort(host)
	if err != nil {
		return "80"
	}
	return port
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.App.Store.DB().PingContext(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unhealthy"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "pbmanager", "version": s.Version})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.App.Store.CountAdmins(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not read authentication state", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"initialized": n > 0, "version": s.Version})
}

func (s *Server) staticHandler() http.Handler {
	sub, err := fs.Sub(webassets.Dist, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	index, _ := fs.ReadFile(sub, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := path.Clean("/" + r.URL.Path)
		if strings.HasPrefix(clean, "/api/") || strings.Contains(clean, "..") {
			writeError(w, r, http.StatusNotFound, "not_found", "Not found", nil)
			return
		}
		if f, err := sub.Open(strings.TrimPrefix(clean, "/")); err == nil {
			_ = f.Close()
			if strings.Contains(clean, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(index)
		}
	})
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		id := hex.EncodeToString(b)
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), ctxRequestID, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestIDFrom(r *http.Request) string {
	v, _ := r.Context().Value(ctxRequestID).(string)
	return v
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		if s.App.Config.SecureCookies || strings.HasPrefix(s.App.Config.PublicURL, "https://") {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic in HTTP handler", "error", rec, "request_id", requestIDFrom(r), "path", r.URL.Path)
				writeError(w, r, http.StatusInternalServerError, "internal", "Internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		level := slog.LevelInfo
		if sw.status >= 500 {
			level = slog.LevelError
		} else if sw.status >= 400 {
			level = slog.LevelWarn
		}
		s.Log.Log(r.Context(), level, "http request",
			"method", r.Method, "path", r.URL.Path, "status", sw.status, "bytes", sw.bytes,
			"duration_ms", time.Since(start).Milliseconds(), "ip", clientIP(r, s.App.Config.TrustProxy),
			"request_id", requestIDFrom(r))
	})
}

func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		ip := clientIP(r, s.App.Config.TrustProxy)
		if ok, retry := s.limiter.Allow(ip); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			writeError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests; slow down", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request, trustProxy bool) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if trustProxy {
		peer := net.ParseIP(ip)
		if peer != nil && (peer.IsLoopback() || peer.IsPrivate()) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				if candidate := strings.TrimSpace(parts[0]); net.ParseIP(candidate) != nil {
					return candidate
				}
			}
		}
	}
	return ip
}

type ipLimiter struct {
	mu      sync.Mutex
	entries map[string]*bucket
	rate    float64
	burst   float64
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newIPLimiter(ratePerSec, burst float64) *ipLimiter {
	return &ipLimiter{entries: map[string]*bucket{}, rate: ratePerSec, burst: burst}
}

func (l *ipLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.entries[key]
	if !ok {
		if len(l.entries) > 4096 {
			for k, v := range l.entries {
				if now.Sub(v.last) > 10*time.Minute {
					delete(l.entries, k)
				}
			}
		}
		l.entries[key] = &bucket{tokens: l.burst - 1, last: now}
		return true, 0
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	}
	b.tokens--
	return true, 0
}

// requireRole re-checks a role inside a handler when necessary.
func (s *Server) requireOwner(r *http.Request) bool {
	a := adminFrom(r)
	return a != nil && a.Role == "owner"
}

func (s *Server) audit(r *http.Request, action, targetType, targetID string, detail any) {
	a := adminFrom(r)
	s.App.EnqueueAudit(r.Context(), "admin", a.ID, a.Username, action, targetType, targetID, detail)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) {
	type errBody struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details,omitempty"`
	}
	writeJSON(w, status, map[string]any{"error": errBody{Code: code, Message: message, Details: details}, "requestId": requestIDFrom(r)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = jsonEncoder(w).Encode(v)
}

func jsonEncoder(w http.ResponseWriter) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func parsePage(r *http.Request) (page, perPage, offset int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ = strconv.Atoi(r.URL.Query().Get("perPage"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 50
	}
	return page, perPage, (page - 1) * perPage
}

func notFoundOr(w http.ResponseWriter, r *http.Request, err error, what string) bool {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", what+" not found", nil)
		return true
	}
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", err.Error(), nil)
		return true
	}
	return false
}
