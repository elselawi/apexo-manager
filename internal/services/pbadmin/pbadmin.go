// Package pbadmin exposes bounded, documented PocketBase administration
// operations. It never proxies arbitrary API responses and never writes data
// collections; application data remains outside this package.
package pbadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/apexo/pbmanager/internal/cron"
	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/secret"
	"github.com/apexo/pbmanager/internal/services/pocketbase"
	"github.com/apexo/pbmanager/internal/services/systemd"
	"github.com/apexo/pbmanager/internal/store"
)

type Manager struct {
	Store   *store.Store
	Secrets *secret.Box
	Systemd *systemd.Manager
	Runner  platform.Runner
	DataDir string
	Log     *slog.Logger

	mu sync.Mutex
}

type Settings struct {
	Meta         AppMeta        `json:"meta"`
	SMTP         SMTP           `json:"smtp"`
	S3           S3             `json:"s3"`
	Backups      BackupSettings `json:"backups"`
	RateLimits   RateLimits     `json:"rateLimits"`
	TrustedProxy TrustedProxy   `json:"trustedProxy"`
	Logs         LogSettings    `json:"logs"`
	Batch        BatchSettings  `json:"batch"`
}

type AppMeta struct {
	AppName       string `json:"appName"`
	AppURL        string `json:"appURL"`
	SenderName    string `json:"senderName"`
	SenderAddress string `json:"senderAddress"`
	HideControls  bool   `json:"hideControls"`
}

type SMTP struct {
	Enabled    bool   `json:"enabled"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"`
	AuthMethod string `json:"authMethod"`
	TLS        bool   `json:"tls"`
	LocalName  string `json:"localName"`
}

type S3 struct {
	Enabled        bool   `json:"enabled"`
	Bucket         string `json:"bucket"`
	Region         string `json:"region"`
	Endpoint       string `json:"endpoint"`
	AccessKey      string `json:"accessKey"`
	Secret         string `json:"secret,omitempty"`
	ForcePathStyle bool   `json:"forcePathStyle"`
}

type BackupS3 struct {
	Enabled        bool   `json:"enabled"`
	Bucket         string `json:"bucket"`
	Region         string `json:"region"`
	Endpoint       string `json:"endpoint"`
	AccessKey      string `json:"accessKey"`
	Secret         string `json:"secret,omitempty"`
	ForcePathStyle bool   `json:"forcePathStyle"`
}

type BackupSettings struct {
	Cron    string   `json:"cron"`
	MaxKeep int      `json:"cronMaxKeep"`
	S3      BackupS3 `json:"s3"`
}

type RateLimitRule struct {
	Label       string `json:"label"`
	Audience    string `json:"audience"`
	Duration    int    `json:"duration"`
	MaxRequests int    `json:"maxRequests"`
}

type RateLimits struct {
	Enabled bool            `json:"enabled"`
	Rules   []RateLimitRule `json:"rules"`
}

type TrustedProxy struct {
	Headers       []string `json:"headers"`
	UseLeftmostIP bool     `json:"useLeftmostIP"`
}

type LogSettings struct {
	MaxDays   int  `json:"maxDays"`
	MinLevel  int  `json:"minLevel"`
	LogIP     bool `json:"logIP"`
	LogAuthID bool `json:"logAuthId"`
}

type BatchSettings struct {
	Enabled     bool `json:"enabled"`
	MaxRequests int  `json:"maxRequests"`
	Timeout     int  `json:"timeout"`
	MaxBodySize int  `json:"maxBodySize"`
}

func (m *Manager) ReadSettings(ctx context.Context, id string) (*Settings, error) {
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return nil, err
	}
	raw, err := client.GetSettings(ctx, token)
	if err != nil {
		return nil, err
	}
	var out Settings
	encoded, _ := json.Marshal(raw)
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, err
	}
	if out.RateLimits.Rules == nil {
		out.RateLimits.Rules = []RateLimitRule{}
	}
	if out.TrustedProxy.Headers == nil {
		out.TrustedProxy.Headers = []string{}
	}
	return &out, nil
}

func (m *Manager) UpdateSettings(ctx context.Context, id string, in Settings) (*Settings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := validateSettings(in); err != nil {
		return nil, err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return nil, err
	}
	patch := map[string]interface{}{
		"meta":         in.Meta,
		"smtp":         in.SMTP,
		"s3":           in.S3,
		"backups":      in.Backups,
		"rateLimits":   in.RateLimits,
		"trustedProxy": in.TrustedProxy,
		"logs":         in.Logs,
		"batch":        in.Batch,
	}
	if _, err := client.PatchSettings(ctx, token, patch); err != nil {
		return nil, err
	}
	return m.ReadSettings(ctx, id)
}

func (m *Manager) ListBackups(ctx context.Context, id string) ([]pocketbase.BackupInfo, error) {
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return nil, err
	}
	list, err := client.ListBackups(ctx, token)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []pocketbase.BackupInfo{}
	}
	return list, nil
}

func (m *Manager) CreateBackup(ctx context.Context, id string) (pocketbase.BackupInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return pocketbase.BackupInfo{}, err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return pocketbase.BackupInfo{}, err
	}
	name := fmt.Sprintf("pbmanager-admin-%d.zip", time.Now().Unix())
	if err := client.CreateBackup(ctx, token, name); err != nil {
		return pocketbase.BackupInfo{}, err
	}
	return client.WaitBackup(ctx, token, name, 2*time.Minute)
}

func (m *Manager) DeleteBackup(ctx context.Context, id, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return err
	}
	return client.DeleteBackup(ctx, token, key)
}

func (m *Manager) RestoreBackup(ctx context.Context, id, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return err
	}
	if err := client.RestoreBackup(ctx, token, key); err != nil {
		return err
	}
	// PocketBase restarts itself after accepting the restore. Wait for the
	// documented health endpoint rather than assuming completion.
	return client.WaitHealthy(ctx, 2*time.Minute)
}

func (m *Manager) DownloadBackup(ctx context.Context, id, key string) (string, func(), error) {
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return "", nil, err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return "", nil, err
	}
	fileToken, err := client.FileToken(ctx, token)
	if err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp("", "pbmanager-backup-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	dest := filepath.Join(dir, filepath.Base(key))
	if err := client.DownloadBackup(ctx, token, fileToken, key, dest); err != nil {
		cleanup()
		return "", nil, err
	}
	return dest, cleanup, nil
}

func (m *Manager) ListSuperusers(ctx context.Context, id string) ([]pocketbase.Superuser, error) {
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return nil, err
	}
	list, err := client.ListSuperusers(ctx, token)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []pocketbase.Superuser{}
	}
	return list, nil
}

func (m *Manager) CreateSuperuser(ctx context.Context, id, email, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	email = strings.TrimSpace(strings.ToLower(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return errors.New("invalid email address")
	}
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return err
	}
	users, err := client.ListSuperusers(ctx, token)
	if err != nil {
		return err
	}
	for _, u := range users {
		if strings.EqualFold(u.Email, email) {
			return errors.New("a superuser with that email already exists")
		}
	}
	if err := m.withServiceStopped(ctx, inst, func() error {
		_, stderr, err := m.runSuperuser(ctx, inst, "upsert", email, password)
		if err != nil {
			return fmt.Errorf("create superuser: %s", commandError(err, stderr))
		}
		return nil
	}, true); err != nil {
		return err
	}
	return m.verifyGeneratedCredentials(ctx, inst, email, password)
}

func (m *Manager) verifyGeneratedCredentials(ctx context.Context, inst *store.Instance, email, password string) error {
	st, err := m.Systemd.Status(ctx, inst.ServiceName)
	if err != nil || !st.Running {
		return nil
	}
	_, err = pocketbase.New(inst.Port).AuthSession(ctx, email, password)
	if err != nil {
		return fmt.Errorf("created superuser could not authenticate: %w", err)
	}
	return nil
}

func (m *Manager) UpdateSuperuser(ctx context.Context, id, recordID, email, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	email = strings.TrimSpace(strings.ToLower(email))
	if _, err := mail.ParseAddress(email); err != nil {
		return errors.New("invalid email address")
	}
	if len(password) < 8 {
		return errors.New("a new password of at least 8 characters is required")
	}
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return err
	}
	users, err := client.ListSuperusers(ctx, token)
	if err != nil {
		return err
	}
	var current *pocketbase.Superuser
	for i := range users {
		if users[i].ID == recordID {
			current = &users[i]
			break
		}
	}
	if current == nil {
		return errors.New("superuser record not found")
	}
	if email != current.Email {
		for _, u := range users {
			if strings.EqualFold(u.Email, email) {
				return errors.New("another superuser already uses that email")
			}
		}
	}
	if err := m.withServiceStopped(ctx, inst, func() error {
		if _, stderr, err := m.runSuperuser(ctx, inst, "upsert", email, password); err != nil {
			return fmt.Errorf("update superuser: %s", commandError(err, stderr))
		}
		if !strings.EqualFold(email, current.Email) {
			if _, stderr, err := m.runSuperuser(ctx, inst, "delete", current.Email, ""); err != nil {
				return fmt.Errorf("remove old superuser email: %s", commandError(err, stderr))
			}
		}
		return nil
	}, true); err != nil {
		return err
	}
	return m.verifyGeneratedCredentials(ctx, inst, email, password)
}

func (m *Manager) DeleteSuperuser(ctx context.Context, id, recordID, confirmation string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if confirmation == "" {
		return errors.New("confirmation is required")
	}
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return err
	}
	users, err := client.ListSuperusers(ctx, token)
	if err != nil {
		return err
	}
	if len(users) <= 1 {
		return errors.New("refusing to delete the last superuser")
	}
	var current *pocketbase.Superuser
	for i := range users {
		if users[i].ID == recordID {
			current = &users[i]
			break
		}
	}
	if current == nil {
		return errors.New("superuser record not found")
	}
	if confirmation != current.Email {
		return errors.New("confirmation email does not match")
	}
	return m.withServiceStopped(ctx, inst, func() error {
		_, stderr, err := m.runSuperuser(ctx, inst, "delete", current.Email, "")
		if err != nil {
			return fmt.Errorf("delete superuser: %s", commandError(err, stderr))
		}
		return nil
	}, true)
}

func (m *Manager) ListAuthCollections(ctx context.Context, id string) ([]pocketbase.Collection, error) {
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return nil, err
	}
	all, err := client.ListCollections(ctx, token)
	if err != nil {
		return nil, err
	}
	var out []pocketbase.Collection
	for _, c := range all {
		if c.Type == "auth" && c.Name != "_superusers" {
			out = append(out, c)
		}
	}
	if out == nil {
		out = []pocketbase.Collection{}
	}
	return out, nil
}

func (m *Manager) ListAuthUsers(ctx context.Context, id, collectionID string) ([]map[string]interface{}, error) {
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return nil, err
	}
	if err := ensureAuthCollection(ctx, client, token, collectionID); err != nil {
		return nil, err
	}
	users, err := client.ListRecords(ctx, token, collectionID, 100)
	if err != nil {
		return nil, err
	}
	if users == nil {
		users = []map[string]interface{}{}
	}
	return users, nil
}

func (m *Manager) CreateAuthUser(ctx context.Context, id, collectionID string, data map[string]interface{}) (map[string]interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := normalizeUserInput(data, true); err != nil {
		return nil, err
	}
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return nil, err
	}
	if err := ensureAuthCollection(ctx, client, token, collectionID); err != nil {
		return nil, err
	}
	return client.CreateRecord(ctx, token, collectionID, data)
}

func (m *Manager) UpdateAuthUser(ctx context.Context, id, collectionID, recordID string, data map[string]interface{}) (map[string]interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := normalizeUserInput(data, false); err != nil {
		return nil, err
	}
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return nil, err
	}
	if err := ensureAuthCollection(ctx, client, token, collectionID); err != nil {
		return nil, err
	}
	return client.UpdateRecord(ctx, token, collectionID, recordID, data)
}

func (m *Manager) DeleteAuthUser(ctx context.Context, id, collectionID, recordID, confirmation string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if confirmation == "" {
		return errors.New("confirmation is required")
	}
	inst, err := m.Store.GetInstance(ctx, id)
	if err != nil {
		return err
	}
	client, token, err := m.auth(ctx, inst)
	if err != nil {
		return err
	}
	if err := ensureAuthCollection(ctx, client, token, collectionID); err != nil {
		return err
	}
	return client.DeleteRecord(ctx, token, collectionID, recordID)
}

func ensureAuthCollection(ctx context.Context, client *pocketbase.Client, token, collectionID string) error {
	all, err := client.ListCollections(ctx, token)
	if err != nil {
		return err
	}
	for _, c := range all {
		if c.ID == collectionID {
			if c.Type != "auth" {
				return errors.New("only auth collections are supported")
			}
			return nil
		}
	}
	return errors.New("auth collection not found")
}

type serviceJournal struct {
	InstanceID  string `json:"instanceId"`
	ServiceName string `json:"serviceName"`
	WasRunning  bool   `json:"wasRunning"`
}

func (m *Manager) journalDir() string {
	if m.DataDir == "" {
		return ""
	}
	return filepath.Join(m.DataDir, "admin-ops")
}

func (m *Manager) writeServiceJournal(inst *store.Instance, wasRunning bool) (string, error) {
	dir := m.journalDir()
	if dir == "" {
		return "", nil
	}
	if err := platform.EnsureDir(dir, 0o700); err != nil {
		return "", err
	}
	j := serviceJournal{InstanceID: inst.ID, ServiceName: inst.ServiceName, WasRunning: wasRunning}
	raw, _ := json.MarshalIndent(j, "", "  ")
	path := filepath.Join(dir, inst.ID+".json")
	return path, platform.AtomicWrite(path, raw, 0o600)
}

func (m *Manager) removeServiceJournal(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

// Recover restores a service left stopped by a crashed admin operation.
func (m *Manager) Recover(ctx context.Context) error {
	dir := m.journalDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		var j serviceJournal
		if err := json.Unmarshal(raw, &j); err != nil {
			continue
		}
		if !j.WasRunning || j.ServiceName == "" {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
			continue
		}
		var port int
		if m.Store != nil {
			if inst, err := m.Store.GetInstance(ctx, j.InstanceID); err == nil {
				port = inst.Port
			}
		}
		_ = m.Systemd.ResetFailed(ctx, j.ServiceName)
		if err := m.Systemd.Start(ctx, j.ServiceName); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", j.ServiceName, err))
			continue
		}
		if port > 0 {
			if err := pocketbase.New(port).WaitHealthy(ctx, 90*time.Second); err != nil {
				errs = append(errs, fmt.Sprintf("%s health: %v", j.ServiceName, err))
				continue
			}
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (m *Manager) auth(ctx context.Context, inst *store.Instance) (*pocketbase.Client, string, error) {
	if inst.PBSuperuserEmail == "" || inst.PBSuperuserSecret == "" {
		return nil, "", errors.New("link or create a managed PocketBase superuser first")
	}
	password, err := m.Secrets.Open("pb_superuser:"+inst.ID, inst.PBSuperuserSecret)
	if err != nil {
		return nil, "", errors.New("stored PocketBase credential could not be decrypted")
	}
	client := pocketbase.New(inst.Port)
	token, err := client.TestAuth(ctx, inst.PBSuperuserEmail, password)
	if err != nil {
		return nil, "", err
	}
	return client, token, nil
}

func (m *Manager) withServiceStopped(ctx context.Context, inst *store.Instance, fn func() error, verify bool) error {
	st, err := m.Systemd.Status(ctx, inst.ServiceName)
	if err != nil {
		return err
	}
	if !st.Exists {
		return fmt.Errorf("systemd unit %s does not exist", inst.ServiceName)
	}
	wasRunning := st.Running
	journalPath, journalErr := m.writeServiceJournal(inst, wasRunning)
	if journalErr != nil {
		return journalErr
	}
	keepJournal := false
	defer func() {
		if !keepJournal {
			m.removeServiceJournal(journalPath)
		}
	}()
	if wasRunning {
		if err := m.Systemd.Stop(ctx, inst.ServiceName); err != nil {
			return err
		}
		if err := m.waitStopped(ctx, inst.ServiceName); err != nil {
			return err
		}
	}
	runErr := fn()
	if wasRunning {
		_ = m.Systemd.ResetFailed(ctx, inst.ServiceName)
		if err := m.Systemd.Start(ctx, inst.ServiceName); err != nil {
			keepJournal = true
			if runErr != nil {
				return fmt.Errorf("%v; restart failed: %w", runErr, err)
			}
			return err
		}
		if verify {
			if err := pocketbase.New(inst.Port).WaitHealthy(ctx, 90*time.Second); err != nil {
				keepJournal = true
				if runErr != nil {
					return fmt.Errorf("%v; health check failed: %w", runErr, err)
				}
				return err
			}
		}
	}
	return runErr
}

func (m *Manager) waitStopped(ctx context.Context, service string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := m.Systemd.Status(ctx, service)
		if err == nil && !st.Running {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return errors.New("service did not stop")
}

func (m *Manager) runSuperuser(ctx context.Context, inst *store.Instance, action, email, password string) ([]byte, []byte, error) {
	runner := m.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	name := inst.BinaryPath
	var args []string
	if inst.RunAsUser != "" {
		name = "runuser"
		args = append(args, "-u", inst.RunAsUser, "--")
	}
	args = append(args, inst.BinaryPath, "superuser", action, email)
	if action == "upsert" {
		args = append(args, password)
	}
	args = append(args, "--dir", inst.DataDir)
	return runner.Run(ctx, name, args...)
}

func validateSettings(in Settings) error {
	if strings.ContainsAny(in.Meta.AppName, "\r\n") || strings.ContainsAny(in.Meta.SenderName, "\r\n") {
		return errors.New("application name and sender name must be single-line")
	}
	if in.SMTP.Enabled && in.SMTP.Host == "" {
		return errors.New("SMTP host is required when SMTP is enabled")
	}
	if in.SMTP.Port < 0 || in.SMTP.Port > 65535 {
		return errors.New("SMTP port is invalid")
	}
	if in.S3.Enabled && in.S3.Bucket == "" {
		return errors.New("S3 bucket is required when S3 is enabled")
	}
	if in.Backups.MaxKeep < 1 || in.Backups.MaxKeep > 1000 {
		return errors.New("backup retention must be between 1 and 1000")
	}
	if in.Backups.S3.Enabled && in.Backups.S3.Bucket == "" {
		return errors.New("backup S3 bucket is required when backup S3 is enabled")
	}
	if strings.ContainsAny(in.Backups.Cron, "\r\n") {
		return errors.New("backup cron is invalid")
	}
	if strings.TrimSpace(in.Backups.Cron) != "" {
		if _, err := cron.NextFromString(in.Backups.Cron, time.Now().UTC()); err != nil {
			return errors.New("backup cron is invalid")
		}
	}
	for _, r := range in.RateLimits.Rules {
		if r.Label == "" || r.Duration < 1 || r.MaxRequests < 1 {
			return errors.New("rate limit rules require label, duration, and maxRequests")
		}
	}
	for _, h := range in.TrustedProxy.Headers {
		if strings.ContainsAny(h, "\r\n") {
			return errors.New("trusted proxy headers are invalid")
		}
	}
	return nil
}

func normalizeUserInput(data map[string]interface{}, create bool) error {
	password, _ := data["password"].(string)
	password = strings.TrimSpace(password)
	if create && password == "" {
		return errors.New("password is required")
	}
	if password != "" {
		if len(password) < 8 {
			return errors.New("password must be at least 8 characters")
		}
		data["passwordConfirm"] = password
	} else {
		delete(data, "password")
		delete(data, "passwordConfirm")
	}
	email, _ := data["email"].(string)
	email = strings.TrimSpace(email)
	if email != "" {
		if _, err := mail.ParseAddress(email); err != nil {
			return errors.New("invalid email address")
		}
	}
	return nil
}

func commandError(err error, stderr []byte) string {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = err.Error()
	}
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return msg
}
