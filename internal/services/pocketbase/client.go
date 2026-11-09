// Package pocketbase contains a minimal PocketBase HTTP client that uses only
// documented public APIs. It never reads or writes PocketBase's internal
// database structures.
package pocketbase

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/platform"
)

// ErrUnauthorized indicates rejected PocketBase superuser credentials.
var ErrUnauthorized = errors.New("PocketBase authentication failed or expired")

// Client talks to one instance over loopback.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Runner  platform.Runner
}

// New creates a client for a local instance port.
func New(port int) *Client {
	return &Client{
		BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("redirects are not followed for PocketBase API calls")
			},
		},
		Runner: platform.ExecRunner{},
	}
}

// APIError is a documented PocketBase API error.
type APIError struct {
	Status  int
	Message string
	Data    json.RawMessage
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("PocketBase API returned %d", e.Status)
	if e.Message != "" {
		msg += ": " + e.Message
	}
	return msg
}

func (c *Client) do(ctx context.Context, method, path, token string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "pbmanager/1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("PocketBase connection failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	if resp.StatusCode >= 400 {
		apiErr := &APIError{Status: resp.StatusCode, Data: json.RawMessage(raw)}
		var payload struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			apiErr.Message = payload.Message
		}
		return apiErr
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode PocketBase response: %w", err)
		}
	}
	return nil
}

// Health checks the documented health endpoint.
func (c *Client) Health(ctx context.Context) error {
	code, _, err := c.httpStatus(ctx, c.BaseURL+"/api/health")
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("health endpoint returned HTTP %d", code)
	}
	return nil
}

func (c *Client) httpStatus(ctx context.Context, target string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "pbmanager-health/1")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, raw, nil
}

// AuthSession is a superuser login result used for managed credentials.
type AuthSession struct {
	Token    string
	RecordID string
}

// Auth authenticates a superuser and returns the access token.
func (c *Client) Auth(ctx context.Context, email, password string) (string, error) {
	session, err := c.AuthSession(ctx, email, password)
	if err != nil {
		return "", err
	}
	return session.Token, nil
}

// AuthSession authenticates a superuser and returns the token and record ID.
func (c *Client) AuthSession(ctx context.Context, email, password string) (AuthSession, error) {
	var out struct {
		Token  string `json:"token"`
		Record struct {
			ID string `json:"id"`
		} `json:"record"`
	}
	err := c.do(ctx, http.MethodPost, "/api/collections/_superusers/auth-with-password", "", map[string]string{
		"identity": email,
		"password": password,
	}, &out)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && (apiErr.Status == http.StatusBadRequest || apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden) {
			return AuthSession{}, ErrUnauthorized
		}
		if errors.Is(err, ErrUnauthorized) {
			return AuthSession{}, ErrUnauthorized
		}
		return AuthSession{}, err
	}
	if out.Token == "" {
		return AuthSession{}, ErrUnauthorized
	}
	recordID := out.Record.ID
	if recordID == "" {
		recordID = jwtRecordID(out.Token)
	}
	return AuthSession{Token: out.Token, RecordID: recordID}, nil
}

// TestAuth verifies credentials and returns the token.
func (c *Client) TestAuth(ctx context.Context, email, password string) (string, error) {
	return c.Auth(ctx, email, password)
}

// Superuser is the documented superuser record shape needed for identity
// cleanup after an email rename.
type Superuser struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
	Created  string `json:"created"`
	Updated  string `json:"updated"`
}

// ListSuperusers lists superuser records using the documented collection API.
func (c *Client) ListSuperusers(ctx context.Context, token string) ([]Superuser, error) {
	var out struct {
		Items []Superuser `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/collections/_superusers/records?perPage=200&skipTotal=1", token, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// Collection is the documented collection metadata shape used by the admin UI.
type Collection struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name"`
	Type   string                 `json:"type"`
	Fields []CollectionField      `json:"fields"`
	Schema []CollectionField      `json:"schema"`
	Raw    map[string]interface{} `json:"-"`
}

type CollectionField struct {
	Name     string                 `json:"name"`
	Type     string                 `json:"type"`
	Required bool                   `json:"required"`
	Options  map[string]interface{} `json:"options"`
}

// ListCollections lists collections using the documented management API.
func (c *Client) ListCollections(ctx context.Context, token string) ([]Collection, error) {
	var out struct {
		Items []Collection `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/collections?perPage=200&skipTotal=1", token, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ListRecords lists records for a collection using the documented API.
func (c *Client) ListRecords(ctx context.Context, token, collectionID string, perPage int) ([]map[string]interface{}, error) {
	if perPage <= 0 || perPage > 200 {
		perPage = 100
	}
	var out struct {
		Items []map[string]interface{} `json:"items"`
	}
	path := "/api/collections/" + url.PathEscape(collectionID) + "/records?perPage=" + strconv.Itoa(perPage) + "&skipTotal=1&sort=-created"
	if err := c.do(ctx, http.MethodGet, path, token, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// CreateRecord creates a record through the documented collection API.
func (c *Client) CreateRecord(ctx context.Context, token, collectionID string, data map[string]interface{}) (map[string]interface{}, error) {
	var out map[string]interface{}
	err := c.do(ctx, http.MethodPost, "/api/collections/"+url.PathEscape(collectionID)+"/records", token, data, &out)
	return out, err
}

// UpdateRecord updates a record through the documented collection API.
func (c *Client) UpdateRecord(ctx context.Context, token, collectionID, recordID string, data map[string]interface{}) (map[string]interface{}, error) {
	var out map[string]interface{}
	err := c.do(ctx, http.MethodPatch, "/api/collections/"+url.PathEscape(collectionID)+"/records/"+url.PathEscape(recordID), token, data, &out)
	return out, err
}

// DeleteRecord deletes a record through the documented collection API.
func (c *Client) DeleteRecord(ctx context.Context, token, collectionID, recordID string) error {
	return c.do(ctx, http.MethodDelete, "/api/collections/"+url.PathEscape(collectionID)+"/records/"+url.PathEscape(recordID), token, nil, nil)
}

func jwtRecordID(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		ID  string `json:"id"`
		Sub string `json:"sub"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	if claims.ID != "" {
		return claims.ID
	}
	return claims.Sub
}

// GetSettings fetches documented application settings.
func (c *Client) GetSettings(ctx context.Context, token string) (map[string]any, error) {
	var out map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/settings", token, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PatchSettings applies a partial settings update; PocketBase merges it onto
// existing settings, so unrelated configuration is preserved.
func (c *Client) PatchSettings(ctx context.Context, token string, patch map[string]any) (map[string]any, error) {
	var out map[string]any
	if err := c.do(ctx, http.MethodPatch, "/api/settings", token, patch, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SMTPPatch builds the documented SMTP update payload.
func SMTPPatch(host string, port int, username, password, authMethod string, tlsOn bool, localName, fromAddress, fromName string) map[string]any {
	smtp := map[string]any{
		"enabled":    true,
		"host":       host,
		"port":       port,
		"username":   username,
		"password":   password,
		"authMethod": authMethod,
		"tls":        tlsOn,
		"localName":  localName,
	}
	patch := map[string]any{"smtp": smtp}
	if fromAddress != "" || fromName != "" {
		patch["meta"] = map[string]any{"senderAddress": fromAddress, "senderName": fromName}
	}
	return patch
}

// S3Patch builds the documented S3 update payload.
func S3Patch(endpoint, region, bucket, accessKey, secret string, forcePathStyle bool) map[string]any {
	return map[string]any{"s3": map[string]any{
		"enabled":        true,
		"endpoint":       endpoint,
		"region":         region,
		"bucket":         bucket,
		"accessKey":      accessKey,
		"secret":         secret,
		"forcePathStyle": forcePathStyle,
	}}
}

// SettingsSMTP extracts a string field from a settings response.
func SettingsSMTP(settings map[string]any) map[string]any {
	if m, ok := settings["smtp"].(map[string]any); ok {
		return m
	}
	return nil
}

// SettingsS3 extracts the S3 section.
func SettingsS3(settings map[string]any) map[string]any {
	if m, ok := settings["s3"].(map[string]any); ok {
		return m
	}
	return nil
}

// BackupInfo is a documented backup list entry.
type BackupInfo struct {
	Key      string `json:"key"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// CreateBackup starts an official PocketBase backup.
func (c *Client) CreateBackup(ctx context.Context, token, name string) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,100}\.zip$`).MatchString(name) {
		return errors.New("invalid backup name")
	}
	return c.do(ctx, http.MethodPost, "/api/backups", token, map[string]string{"name": name}, nil)
}

// ListBackups lists official backups.
func (c *Client) ListBackups(ctx context.Context, token string) ([]BackupInfo, error) {
	var out []BackupInfo
	if err := c.do(ctx, http.MethodGet, "/api/backups", token, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// WaitBackup waits until a named backup appears.
func (c *Client) WaitBackup(ctx context.Context, token, name string, timeout time.Duration) (BackupInfo, error) {
	deadline := time.Now().Add(timeout)
	for {
		list, err := c.ListBackups(ctx, token)
		if err != nil {
			return BackupInfo{}, err
		}
		for _, b := range list {
			if b.Key == name {
				return b, nil
			}
		}
		if time.Now().After(deadline) {
			return BackupInfo{}, errors.New("timed out waiting for PocketBase backup")
		}
		select {
		case <-ctx.Done():
			return BackupInfo{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// FileToken obtains a short-lived file token for backup downloads.
func (c *Client) FileToken(ctx context.Context, token string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/files/token", token, nil, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("empty file token")
	}
	return out.Token, nil
}

// DownloadBackup writes an official backup file to dest.
func (c *Client) DownloadBackup(ctx context.Context, token, fileToken, key, dest string) error {
	if key == "" || strings.ContainsAny(key, `/\`) {
		return errors.New("invalid backup key")
	}
	target := c.BaseURL + "/api/backups/" + url.PathEscape(key) + "?token=" + url.QueryEscape(fileToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("User-Agent", "pbmanager/1")
	client := &http.Client{Timeout: 15 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("backup download returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".backup-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 8<<30)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dest)
}

// RestoreBackup asks PocketBase to restore a backup and restart internally.
func (c *Client) RestoreBackup(ctx context.Context, token, key string) error {
	if key == "" || strings.ContainsAny(key, `/\`) {
		return errors.New("invalid backup key")
	}
	return c.do(ctx, http.MethodPost, "/api/backups/"+url.PathEscape(key)+"/restore", token, nil, nil)
}

// DeleteBackup deletes an official backup.
func (c *Client) DeleteBackup(ctx context.Context, token, key string) error {
	if key == "" || strings.ContainsAny(key, `/\`) {
		return errors.New("invalid backup key")
	}
	return c.do(ctx, http.MethodDelete, "/api/backups/"+url.PathEscape(key), token, nil, nil)
}

// ExecutableVersion runs the binary's documented version command.
func ExecutableVersion(ctx context.Context, binary string) (string, error) {
	if err := platform.ValidateAbsolutePath(binary, false); err != nil {
		return "", err
	}
	out, stderr, err := (platform.ExecRunner{}).Run(ctx, binary, "--version")
	if err != nil {
		if len(stderr) > 0 {
			return "", fmt.Errorf("read version: %s", strings.TrimSpace(string(stderr)))
		}
		return "", err
	}
	text := strings.TrimSpace(string(out))
	re := regexp.MustCompile(`(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)`)
	m := re.FindStringSubmatch(text)
	if len(m) == 2 {
		return m[1], nil
	}
	if len(text) > 0 && len(text) < 64 {
		return strings.TrimPrefix(text, "v"), nil
	}
	return "", errors.New("unrecognized version output")
}

// WaitHealthy polls the health endpoint.
func (c *Client) WaitHealthy(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		if err := c.Health(ctx); err == nil {
			return nil
		} else {
			last = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("PocketBase did not become healthy: %w", last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// ParsePort parses a port string.
func ParsePort(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
