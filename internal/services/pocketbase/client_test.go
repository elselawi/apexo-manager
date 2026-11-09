package pocketbase

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthSettingsAndHealth(t *testing.T) {
	var tokenSeen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "healthy"})
		case "/api/collections/_superusers/auth-with-password":
			var in map[string]string
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["identity"] != "admin@example.com" || in["password"] != "test-password" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "invalid"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "jwt-token", "record": map[string]any{"id": "rec-1"}})
		case "/api/collections/_superusers/records":
			if r.Header.Get("Authorization") != "jwt-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": "rec-1", "email": "admin@example.com"}}})
		case "/api/settings":
			if r.Header.Get("Authorization") != "jwt-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Method == http.MethodPatch {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"smtp": map[string]any{"enabled": true, "host": "smtp.example.com", "port": 587},
					"s3":   map[string]any{"enabled": true, "bucket": "b", "region": "r"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"smtp": map[string]any{"enabled": false}, "s3": map[string]any{"enabled": false}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := New(1)
	client.BaseURL = srv.URL
	if err := client.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	token, err := client.Auth(context.Background(), "admin@example.com", "test-password")
	if err != nil || token != "jwt-token" {
		t.Fatalf("auth: %q %v", token, err)
	}
	if _, err := client.Auth(context.Background(), "admin@example.com", "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong auth: %v", err)
	}
	session, err := client.AuthSession(context.Background(), "admin@example.com", "test-password")
	if err != nil || session.Token != "jwt-token" || session.RecordID != "rec-1" {
		t.Fatalf("auth session: %#v %v", session, err)
	}
	users, err := client.ListSuperusers(context.Background(), session.Token)
	if err != nil || len(users) != 1 || users[0].ID != "rec-1" {
		t.Fatalf("list superusers: %#v %v", users, err)
	}
	settings, err := client.GetSettings(context.Background(), token)
	if err != nil || settings["smtp"] == nil {
		t.Fatalf("settings: %v %#v", err, settings)
	}
	patched, err := client.PatchSettings(context.Background(), token, SMTPPatch("smtp.example.com", 587, "u", "p", "PLAIN", false, "", "from@example.com", "From"))
	if err != nil {
		t.Fatal(err)
	}
	if SettingsSMTP(patched)["host"] != "smtp.example.com" {
		t.Fatalf("patch result: %#v", patched)
	}
	if s3 := SettingsS3(patched); s3["bucket"] != "b" {
		t.Fatalf("s3 patch: %#v", s3)
	}
	tokenSeen = token
	_ = tokenSeen
}

func TestBackupDownloadFlow(t *testing.T) {
	payload := []byte("zip-content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/files/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "file-token"})
		case strings.HasPrefix(r.URL.Path, "/api/backups/") && r.Method == http.MethodGet:
			if r.URL.Query().Get("token") != "file-token" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write(payload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := New(1)
	client.BaseURL = srv.URL
	token, err := client.FileToken(context.Background(), "jwt")
	if err != nil || token != "file-token" {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "backup.zip")
	if err := client.DownloadBackup(context.Background(), "jwt", token, "pbmanager-test.zip", dest); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(dest)
	if string(raw) != string(payload) {
		t.Fatalf("download mismatch: %q", raw)
	}
}
