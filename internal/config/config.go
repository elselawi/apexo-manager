// Package config contains the bootstrap configuration for the manager itself.
//
// Bootstrap configuration is intentionally separate from the editable server
// settings stored in SQLite. Bootstrap values (data directory, listen address,
// public URL) are needed before the database can be opened and are therefore
// managed by the root CLI and the systemd unit, not by the web application.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultPath      = "/etc/pbmanager/config.json"
	DefaultDataDir   = "/var/lib/pbmanager"
	DefaultListen    = "127.0.0.1:8787"
	DefaultPublicURL = "http://127.0.0.1:8787"
)

// Runtime is the manager bootstrap configuration.
type Runtime struct {
	DataDir string `json:"data_dir"`
	DBPath  string `json:"db_path,omitempty"`
	// ListenAddr should normally be a loopback address. The dashboard is meant
	// to be published through Caddy, never directly on a public interface.
	ListenAddr    string `json:"listen_addr"`
	PublicURL     string `json:"public_url"`
	SecureCookies bool   `json:"secure_cookies"`
	LogLevel      string `json:"log_level"`
	// AllowPublicBind exists for unusual deployments (e.g. a private VPN
	// interface). It never changes authentication requirements.
	AllowPublicBind bool `json:"allow_public_bind,omitempty"`
	// SessionHours is the absolute session lifetime.
	SessionHours int `json:"session_hours,omitempty"`
	// TrustProxy trusts X-Forwarded-For only from loopback/private peers.
	TrustProxy bool `json:"trust_proxy,omitempty"`
	// ConfigPath is populated by Load and is not serialized.
	ConfigPath string `json:"-"`
}

// Default returns a conservative production default.
func Default() Runtime {
	return Runtime{
		DataDir:       DefaultDataDir,
		DBPath:        filepath.Join(DefaultDataDir, "manager.db"),
		ListenAddr:    DefaultListen,
		PublicURL:     DefaultPublicURL,
		SecureCookies: false, // loopback HTTP default; install script sets true for HTTPS
		LogLevel:      "info",
		SessionHours:  12,
		TrustProxy:    true,
	}
}

// Path resolves the config path from an explicit value, the environment, or
// the conventional system location.
func Path(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if p := strings.TrimSpace(os.Getenv("PBMANAGER_CONFIG")); p != "" {
		return p
	}
	return DefaultPath
}

// Load reads a runtime config. A missing file is not silently invented; the
// caller decides whether that is acceptable.
func Load(path string) (Runtime, error) {
	cfg := Default()
	cfg.ConfigPath = path
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.ConfigPath = path
	if err := cfg.Normalize(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// LoadOrInit loads the config or writes a default one at path. It refuses to
// overwrite anything and is intended for first-run/development convenience.
func LoadOrInit(path string) (Runtime, bool, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Runtime{}, false, err
	}
	cfg = Default()
	cfg.ConfigPath = path
	if err := cfg.Normalize(); err != nil {
		return Runtime{}, false, err
	}
	if err := Write(path, cfg); err != nil {
		return Runtime{}, false, err
	}
	return cfg, true, nil
}

// FromEnv overlays a small set of environment variables, primarily for tests
// and local development. Unlike some tools, it never accepts secrets.
func (c *Runtime) FromEnv() {
	if v := os.Getenv("PBMANAGER_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("PBMANAGER_DB"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("PBMANAGER_LISTEN"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("PBMANAGER_PUBLIC_URL"); v != "" {
		c.PublicURL = v
	}
	if v := os.Getenv("PBMANAGER_LOG_LEVEL"); v != "" {
		c.LogLevel = v
	}
}

// Normalize cleans and validates the configuration.
func (c *Runtime) Normalize() error {
	c.DataDir = strings.TrimSpace(c.DataDir)
	c.DBPath = strings.TrimSpace(c.DBPath)
	c.ListenAddr = strings.TrimSpace(c.ListenAddr)
	c.PublicURL = strings.TrimSpace(c.PublicURL)
	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))

	if c.DataDir == "" {
		c.DataDir = DefaultDataDir
	}
	if !filepath.IsAbs(c.DataDir) {
		return fmt.Errorf("data_dir must be an absolute path")
	}
	c.DataDir = filepath.Clean(c.DataDir)
	if c.DataDir == "/" {
		return fmt.Errorf("data_dir must not be the filesystem root")
	}
	if c.DBPath == "" {
		c.DBPath = filepath.Join(c.DataDir, "manager.db")
	}
	if !filepath.IsAbs(c.DBPath) {
		return fmt.Errorf("db_path must be an absolute path")
	}
	c.DBPath = filepath.Clean(c.DBPath)
	if strings.ContainsAny(c.DBPath, "\x00\n\r") {
		return fmt.Errorf("db_path contains invalid characters")
	}
	if c.ListenAddr == "" {
		c.ListenAddr = DefaultListen
	}
	host, _, err := net.SplitHostPort(c.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen_addr must be host:port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil && !strings.EqualFold(host, "localhost") {
		return fmt.Errorf("listen_addr host must be an IP address or localhost")
	}
	if !c.AllowPublicBind && !isLoopback(host) {
		return fmt.Errorf("listen_addr %q is not loopback; set allow_public_bind only for a private/VPN interface", c.ListenAddr)
	}
	if c.PublicURL == "" {
		c.PublicURL = "http://" + c.ListenAddr
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("public_url must be an absolute http(s) URL")
	}
	if u.User != nil {
		return fmt.Errorf("public_url must not contain credentials")
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level must be debug, info, warn, or error")
	}
	if c.SessionHours == 0 {
		c.SessionHours = 12
	}
	if c.SessionHours < 1 || c.SessionHours > 168 {
		return fmt.Errorf("session_hours must be between 1 and 168")
	}
	if u.Scheme == "https" {
		c.SecureCookies = true
	}
	return nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateFilesystem verifies the data directory is usable without creating it.
func (c Runtime) ValidateFilesystem() error {
	info, err := os.Stat(c.DataDir)
	if err != nil {
		return fmt.Errorf("data_dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("data_dir %q is not a directory", c.DataDir)
	}
	parent := filepath.Dir(c.DBPath)
	pinfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("database parent: %w", err)
	}
	if !pinfo.IsDir() {
		return fmt.Errorf("database parent %q is not a directory", parent)
	}
	return nil
}

// EnsureDirectories creates the data directory and chmods it to 0700.
func (c Runtime) EnsureDirectories() error {
	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return fmt.Errorf("create data_dir: %w", err)
	}
	if err := os.Chmod(c.DataDir, 0o700); err != nil {
		return fmt.Errorf("secure data_dir permissions: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.DBPath), 0o700); err != nil {
		return fmt.Errorf("create database parent: %w", err)
	}
	return nil
}

// Write persists a config atomically with mode 0600.
func Write(path string, cfg Runtime) error {
	cfg.ConfigPath = ""
	if err := cfg.Normalize(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
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
	return os.Rename(tmpName, path)
}

// SessionTTL returns the configured absolute lifetime with a sane default.
func (c Runtime) SessionTTL() time.Duration {
	h := c.SessionHours
	if h <= 0 {
		h = 12
	}
	return time.Duration(h) * time.Hour
}

// Origin returns the normalized origin of PublicURL.
func (c Runtime) Origin() string {
	u, err := url.Parse(c.PublicURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
