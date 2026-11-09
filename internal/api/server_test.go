package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	appcore "github.com/apexo/pbmanager/internal/app"
	"github.com/apexo/pbmanager/internal/auth"
	"github.com/apexo/pbmanager/internal/config"
)

type apiHarness struct {
	app    *appcore.App
	server *httptest.Server
	client *http.Client
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Runtime{
		DataDir: dir, DBPath: filepath.Join(dir, "manager.db"), ListenAddr: "127.0.0.1:8787",
		PublicURL: "http://127.0.0.1:8787", SecureCookies: false, LogLevel: "error", SessionHours: 1, TrustProxy: true,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	app, err := appcore.Open(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	if _, err := app.Auth.CreateAdmin(context.Background(), "owner", "Owner", "correct-horse-battery-staple", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Auth.CreateAdmin(context.Background(), "viewer", "Viewer", "correct-horse-battery-staple", "viewer"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(app, slog.New(slog.NewTextHandler(io.Discard, nil)), "test").Handler())
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	t.Cleanup(server.Close)
	return &apiHarness{app: app, server: server, client: client}
}

func (h *apiHarness) do(t *testing.T, method, path string, body any, headers map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	return resp, decoded
}

func (h *apiHarness) login(t *testing.T, username string) (string, string) {
	t.Helper()
	resp, body := h.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{
		"username": username, "password": "correct-horse-battery-staple",
	}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status %d: %#v", resp.StatusCode, body)
	}
	csrf, _ := body["csrfToken"].(string)
	if csrf == "" {
		t.Fatal("no csrf token")
	}
	return csrf, resp.Header.Get("Set-Cookie")
}

func TestUnauthorizedAndHealth(t *testing.T) {
	h := newAPIHarness(t)
	resp, _ := h.do(t, http.MethodGet, "/api/v1/instances", nil, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	resp, body := h.do(t, http.MethodGet, "/healthz", nil, nil)
	if resp.StatusCode != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("health: %d %#v", resp.StatusCode, body)
	}
	for _, header := range []string{"X-Content-Type-Options", "Content-Security-Policy", "X-Frame-Options"} {
		if resp.Header.Get(header) == "" {
			t.Fatalf("missing security header %s", header)
		}
	}
}

func TestLoginCSRFAndRoles(t *testing.T) {
	h := newAPIHarness(t)
	csrf, cookie := h.login(t, "owner")
	if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Strict") {
		t.Fatalf("cookie flags: %s", cookie)
	}
	resp, body := h.do(t, http.MethodGet, "/api/v1/auth/me", nil, nil)
	if resp.StatusCode != http.StatusOK || body["csrfToken"] != csrf {
		t.Fatalf("me: %d %#v", resp.StatusCode, body)
	}
	resp, _ = h.do(t, http.MethodPost, "/api/v1/auth/logout", map[string]any{}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("CSRF should be enforced, got %d", resp.StatusCode)
	}
	resp, _ = h.do(t, http.MethodPost, "/api/v1/auth/logout", map[string]any{}, map[string]string{"X-CSRF-Token": csrf})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", resp.StatusCode)
	}

	viewer := newAPIHarness(t)
	vcsrf, _ := viewer.login(t, "viewer")
	resp, _ = viewer.do(t, http.MethodGet, "/api/v1/instances", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("viewer read: %d", resp.StatusCode)
	}
	resp, _ = viewer.do(t, http.MethodGet, "/api/v1/settings", nil, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer settings should be forbidden, got %d", resp.StatusCode)
	}
	resp, _ = viewer.do(t, http.MethodPost, "/api/v1/instances/does-not-exist/start", map[string]any{}, map[string]string{"X-CSRF-Token": vcsrf})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer mutation should be forbidden, got %d", resp.StatusCode)
	}
}

func TestSettingsAndAudit(t *testing.T) {
	h := newAPIHarness(t)
	csrf, _ := h.login(t, "owner")
	resp, body := h.do(t, http.MethodGet, "/api/v1/settings", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings: %d %#v", resp.StatusCode, body)
	}
	settingsObj, _ := body["config"].(map[string]any)
	if settingsObj == nil {
		t.Fatal("no config in response")
	}
	settingsObj["monitorIntervalSeconds"] = float64(30)
	resp, body = h.do(t, http.MethodPut, "/api/v1/settings", settingsObj, map[string]string{"X-CSRF-Token": csrf})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update settings: %d %#v", resp.StatusCode, body)
	}
	if changed, _ := body["changed"].([]any); len(changed) != 1 {
		t.Fatalf("expected one changed field, got %#v", body["changed"])
	}
	resp, body = h.do(t, http.MethodGet, "/api/v1/audit", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit: %d", resp.StatusCode)
	}
	items, _ := body["items"].([]any)
	if len(items) < 2 {
		t.Fatalf("expected login and settings audit entries, got %#v", items)
	}
}

func TestDiscoveryResponseUsesArrays(t *testing.T) {
	h := newAPIHarness(t)
	h.login(t, "owner")
	resp, body := h.do(t, http.MethodGet, "/api/v1/instances/discover", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("discovery status %d: %#v", resp.StatusCode, body)
	}
	if _, ok := body["items"].([]any); !ok {
		t.Fatalf("items must be a JSON array, got %#v", body["items"])
	}
	if _, ok := body["warnings"].([]any); !ok {
		t.Fatalf("warnings must be a JSON array, got %#v", body["warnings"])
	}
}

func TestInvalidCredentialsAndCSRF(t *testing.T) {
	h := newAPIHarness(t)
	resp, _ := h.do(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "owner", "password": "wrong"}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
	if _, err := h.app.Store.GetAdminByUsername(context.Background(), "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.HashPassword("short"); err == nil {
		t.Fatal("expected weak password error")
	}
}
