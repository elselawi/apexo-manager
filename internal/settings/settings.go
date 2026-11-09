// Package settings manages the editable server-wide configuration stored in
// SQLite. It deliberately does not move or alter any existing installation when
// defaults change.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/store"
)

// Config is the editable server configuration.
type Config struct {
	InstallRoot         string   `json:"installRoot"`
	ArchiveTemplate     string   `json:"archiveTemplate"`
	DownloadURLTemplate string   `json:"downloadUrlTemplate"`
	ChecksumURLTemplate string   `json:"checksumUrlTemplate"`
	DefaultPBVersion    string   `json:"defaultPbVersion"`
	PBDataDirTemplate   string   `json:"pbDataDirTemplate"`
	ServiceDir          string   `json:"serviceDir"`
	CaddyfilePath       string   `json:"caddyfilePath"`
	CaddySnippetDir     string   `json:"caddySnippetDir"`
	CaddyService        string   `json:"caddyService"`
	CaddyBinary         string   `json:"caddyBinary"`
	SystemctlBinary     string   `json:"systemctlBinary"`
	JournalctlBinary    string   `json:"journalctlBinary"`
	BackupDir           string   `json:"backupDir"`
	DiscoveryRoots      []string `json:"discoveryRoots"`
	PublicIPv4          string   `json:"publicIpv4"`
	PortStart           int      `json:"portStart"`
	PortEnd             int      `json:"portEnd"`
	ServiceUserPrefix   string   `json:"serviceUserPrefix"`
	MonitorIntervalSecs int      `json:"monitorIntervalSeconds"`
	JobRetentionDays    int      `json:"jobRetentionDays"`
	AuditRetentionDays  int      `json:"auditRetentionDays"`
	BackupRetention     int      `json:"backupRetentionDefault"`
	VerifyHTTPS         bool     `json:"verifyHttps"`
	DefaultCaddyEnabled bool     `json:"defaultCaddyEnabled"`
	DefaultDNSEnabled   bool     `json:"defaultDnsEnabled"`
	RequireChecksums    bool     `json:"requireChecksums"`
}

// Bootstrap describes values that come from the root-owned config file.
type Bootstrap struct {
	ConfigPath string `json:"configPath"`
	DataDir    string `json:"dataDir"`
	DBPath     string `json:"dbPath"`
	ListenAddr string `json:"listenAddr"`
	PublicURL  string `json:"publicUrl"`
	Note       string `json:"note"`
}

// View is the API representation.
type View struct {
	Config    Config    `json:"config"`
	Bootstrap Bootstrap `json:"bootstrap"`
	Warnings  []string  `json:"warnings,omitempty"`
	UpdatedAt string    `json:"updatedAt,omitempty"`
}

// Service loads and validates server settings.
type Service struct {
	store     *store.Store
	bootstrap Bootstrap
	current   Config
	updatedAt string
}

// Defaults returns the initial server-wide defaults.
func Defaults() Config {
	return Config{
		InstallRoot:         "/opt",
		ArchiveTemplate:     "pocketbase_{version}_linux_{arch}.zip",
		DownloadURLTemplate: "https://github.com/pocketbase/pocketbase/releases/download/v{version}/pocketbase_{version}_linux_{arch}.zip",
		ChecksumURLTemplate: "https://github.com/pocketbase/pocketbase/releases/download/v{version}/checksums.txt",
		DefaultPBVersion:    "0.23.6",
		PBDataDirTemplate:   "{base_dir}/pb_data",
		ServiceDir:          "/etc/systemd/system",
		CaddyfilePath:       "/etc/caddy/Caddyfile",
		CaddySnippetDir:     "/etc/caddy/pbmanager.d",
		CaddyService:        "caddy",
		CaddyBinary:         "caddy",
		SystemctlBinary:     "systemctl",
		JournalctlBinary:    "journalctl",
		BackupDir:           "/var/lib/pbmanager/backups",
		DiscoveryRoots:      []string{"/opt", "/srv", "/var/www"},
		PortStart:           8090,
		PortEnd:             8199,
		ServiceUserPrefix:   "pb",
		MonitorIntervalSecs: 60,
		JobRetentionDays:    30,
		AuditRetentionDays:  180,
		BackupRetention:     7,
		VerifyHTTPS:         true,
		DefaultCaddyEnabled: true,
		DefaultDNSEnabled:   false,
		RequireChecksums:    true,
	}
}

// New loads settings or initializes defaults.
func New(ctx context.Context, st *store.Store, bootstrap Bootstrap) (*Service, error) {
	bootstrap.Note = "Bootstrap paths are root-owned. Changing them requires the pbmanager config CLI and a service restart; existing data is never moved automatically."
	s := &Service{store: st, bootstrap: bootstrap}
	raw, err := st.GetServerSettings(ctx)
	if err != nil {
		return nil, err
	}
	cfg := Defaults()
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, fmt.Errorf("stored server settings are invalid: %w", err)
		}
	} else {
		rawBytes, _ := json.Marshal(cfg)
		if err := st.SetServerSettings(ctx, string(rawBytes)); err != nil {
			return nil, err
		}
	}
	if err := s.validate(cfg, false); err != nil {
		return nil, fmt.Errorf("stored server settings failed validation: %w", err)
	}
	s.current = cfg
	return s, nil
}

// Bootstrap returns bootstrap paths.
func (s *Service) Bootstrap() Bootstrap { return s.bootstrap }

// Get returns the current config.
func (s *Service) Get() Config { return s.current }

// View builds the API view.
func (s *Service) View() View {
	warnings := s.warnings(s.current)
	return View{Config: s.current, Bootstrap: s.bootstrap, Warnings: warnings, UpdatedAt: s.updatedAt}
}

// Update validates and persists a new configuration. Path changes only affect
// future operations; no installation is relocated.
func (s *Service) Update(ctx context.Context, candidate Config) (View, []string, error) {
	old := s.current
	if err := s.validate(candidate, true); err != nil {
		return View{}, nil, err
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return View{}, nil, err
	}
	if err := s.store.SetServerSettings(ctx, string(raw)); err != nil {
		return View{}, nil, err
	}
	s.current = candidate
	s.updatedAt = store.Now()
	changed := diff(old, candidate)
	return s.View(), changed, nil
}

func (s *Service) validate(c Config, strict bool) error {
	if err := platform.ValidateAbsolutePath(c.InstallRoot, false); err != nil {
		return fmt.Errorf("installRoot: %w", err)
	}
	if strings.ContainsAny(c.ArchiveTemplate, `/\`) || !strings.Contains(c.ArchiveTemplate, "{version}") {
		return errors.New("archiveTemplate must be a filename containing {version} and no path separators")
	}
	if !strings.Contains(c.ArchiveTemplate, "{arch}") {
		return errors.New("archiveTemplate must contain {arch}")
	}
	if c.DownloadURLTemplate == "" || !strings.Contains(c.DownloadURLTemplate, "{version}") || !strings.Contains(c.DownloadURLTemplate, "{arch}") {
		return errors.New("downloadUrlTemplate must contain {version} and {arch}")
	}
	du, err := url.Parse(strings.ReplaceAll(strings.ReplaceAll(c.DownloadURLTemplate, "{version}", "0.1.0"), "{arch}", "amd64"))
	if err != nil || du.Host == "" {
		return errors.New("downloadUrlTemplate is not a valid URL")
	}
	if du.Scheme != "https" && !(du.Scheme == "http" && isLoopbackHost(du.Hostname())) {
		return errors.New("downloadUrlTemplate must use HTTPS (HTTP is allowed only for loopback test servers)")
	}
	if c.ChecksumURLTemplate != "" {
		cu, err := url.Parse(strings.ReplaceAll(strings.ReplaceAll(c.ChecksumURLTemplate, "{version}", "0.1.0"), "{arch}", "amd64"))
		if err != nil || cu.Host == "" || (cu.Scheme != "https" && !(cu.Scheme == "http" && isLoopbackHost(cu.Hostname()))) {
			return errors.New("checksumUrlTemplate must be an HTTPS URL or empty")
		}
	}
	if !regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`).MatchString(c.DefaultPBVersion) {
		return errors.New("defaultPbVersion contains invalid characters")
	}
	if !strings.Contains(c.PBDataDirTemplate, "{base_dir}") {
		return errors.New("pbDataDirTemplate must contain {base_dir}")
	}
	if strings.ContainsAny(c.PBDataDirTemplate, "\x00\n\r") {
		return errors.New("pbDataDirTemplate contains invalid characters")
	}
	if err := platform.ValidateAbsolutePath(c.ServiceDir, false); err != nil {
		return fmt.Errorf("serviceDir: %w", err)
	}
	if err := platform.ValidateAbsolutePath(c.CaddyfilePath, false); err != nil {
		return fmt.Errorf("caddyfilePath: %w", err)
	}
	if err := platform.ValidateAbsolutePath(c.CaddySnippetDir, false); err != nil {
		return fmt.Errorf("caddySnippetDir: %w", err)
	}
	if err := platform.ValidateAbsolutePath(c.BackupDir, false); err != nil {
		return fmt.Errorf("backupDir: %w", err)
	}
	if len(c.DiscoveryRoots) == 0 || len(c.DiscoveryRoots) > 12 {
		return errors.New("discoveryRoots must contain between 1 and 12 directories")
	}
	for _, root := range c.DiscoveryRoots {
		if err := platform.ValidateAbsolutePath(root, false); err != nil {
			return fmt.Errorf("discoveryRoots: %w", err)
		}
	}
	if !platform.ValidSafeName(c.CaddyService) {
		return errors.New("caddyService is not a valid service name")
	}
	if !platform.ValidSafeName(c.ServiceUserPrefix) {
		return errors.New("serviceUserPrefix is not a valid identifier")
	}
	if c.PortStart < 1 || c.PortEnd > 65535 || c.PortStart > c.PortEnd {
		return errors.New("port range is invalid")
	}
	if c.MonitorIntervalSecs < 15 || c.MonitorIntervalSecs > 3600 {
		return errors.New("monitorIntervalSeconds must be between 15 and 3600")
	}
	if c.JobRetentionDays < 1 || c.JobRetentionDays > 3650 {
		return errors.New("jobRetentionDays must be between 1 and 3650")
	}
	if c.AuditRetentionDays < 1 || c.AuditRetentionDays > 3650 {
		return errors.New("auditRetentionDays must be between 1 and 3650")
	}
	if c.BackupRetention < 1 || c.BackupRetention > 365 {
		return errors.New("backupRetentionDefault must be between 1 and 365")
	}
	if c.PublicIPv4 != "" {
		ip := net.ParseIP(c.PublicIPv4)
		if ip == nil || ip.To4() == nil {
			return errors.New("publicIpv4 must be a valid IPv4 address")
		}
	}
	for name, bin := range map[string]string{"caddyBinary": c.CaddyBinary, "systemctlBinary": c.SystemctlBinary, "journalctlBinary": c.JournalctlBinary} {
		if strings.TrimSpace(bin) == "" || strings.ContainsAny(bin, "\x00\n\r") {
			return fmt.Errorf("%s is required", name)
		}
	}
	if strict {
		if info, err := os.Stat(c.ServiceDir); err != nil || !info.IsDir() {
			return fmt.Errorf("serviceDir %s does not exist or is not a directory", c.ServiceDir)
		}
		// Caddy and backup paths may intentionally be configured before the
		// corresponding packages are installed. They are exposed as warnings and
		// validated again by the operations that use them.
	}
	return nil
}

func (s *Service) warnings(c Config) []string {
	var out []string
	if _, err := os.Stat(c.CaddyfilePath); err != nil {
		out = append(out, "Caddyfile not found at "+c.CaddyfilePath+"; Caddy operations will be unavailable until installed")
	}
	if info, err := os.Stat(c.CaddySnippetDir); err != nil || !info.IsDir() {
		out = append(out, "Caddy snippet directory does not exist yet; it will be created by the first managed route")
	}
	return out
}

func diff(a, b Config) []string {
	var out []string
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	t := va.Type()
	for i := 0; i < t.NumField(); i++ {
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			out = append(out, t.Field(i).Tag.Get("json"))
		}
	}
	return out
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ExpandVersion applies version/arch placeholders.
func ExpandVersion(template, version, arch string) string {
	return strings.NewReplacer("{version}", version, "{arch}", arch).Replace(template)
}

// ExpandDataDir applies the base-dir placeholder.
func ExpandDataDir(template, baseDir string) string {
	return strings.ReplaceAll(template, "{base_dir}", baseDir)
}
