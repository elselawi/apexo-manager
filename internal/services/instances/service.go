// Package instances implements PocketBase lifecycle operations and the
// provisioning state machine. All long-running methods are job handlers.
package instances

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/cron"
	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/netutil"
	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/secret"
	"github.com/apexo/pbmanager/internal/services/backup"
	"github.com/apexo/pbmanager/internal/services/caddy"
	"github.com/apexo/pbmanager/internal/services/dns"
	"github.com/apexo/pbmanager/internal/services/pbauth"
	"github.com/apexo/pbmanager/internal/services/systemd"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

// Service coordinates inventory and infrastructure operations.
type Service struct {
	Store   *store.Store
	Secrets *secret.Box
	Cfg     func() settings.Config
	Systemd *systemd.Manager
	Caddy   *caddy.Manager
	DNS     *dns.Manager
	Backup  *backup.Manager
	PBAuth  *pbauth.Manager
	Runner  platform.Runner
	Jobs    *jobs.Engine
	Log     *slog.Logger
}

// CreateInput is the provisioning request.
type CreateInput struct {
	Name                string `json:"name"`
	Hostname            string `json:"hostname"`
	PBVersion           string `json:"pbVersion"`
	Arch                string `json:"arch"`
	Port                int    `json:"port"`
	BaseDir             string `json:"baseDir"`
	DataDir             string `json:"dataDir"`
	BinaryPath          string `json:"binaryPath"`
	ServiceName         string `json:"serviceName"`
	ServiceFile         string `json:"serviceFile"`
	RunAsUser           string `json:"runAsUser"`
	CreateSystemUser    *bool  `json:"createSystemUser"`
	AllowRootUser       bool   `json:"allowRootUser"`
	Domain              string `json:"domain"`
	CloudflareAccountID string `json:"cloudflareAccountId"`
	ManageCaddy         bool   `json:"manageCaddy"`
	ManageDNS           bool   `json:"manageDns"`
	Proxied             bool   `json:"proxied"`
	SMTPProfileID       string `json:"smtpProfileId"`
	StorageProfileID    string `json:"storageProfileId"`
	BackupEnabled       bool   `json:"backupEnabled"`
	BackupCron          string `json:"backupCron"`
	BackupRetention     int    `json:"backupRetention"`
	Notes               string `json:"notes"`
	Checksum            string `json:"checksum"`
}

// ProvisionPayload drives the provisioning job.
type ProvisionPayload struct {
	InstanceID  string `json:"instanceId"`
	DomainID    string `json:"domainId,omitempty"`
	ManageCaddy bool   `json:"manageCaddy"`
	ManageDNS   bool   `json:"manageDns"`
	Checksum    string `json:"checksum,omitempty"`
	Arch        string `json:"arch,omitempty"`
}

// Defaults describes calculated defaults for a new instance.
type Defaults struct {
	BaseDir     string   `json:"baseDir"`
	DataDir     string   `json:"dataDir"`
	BinaryPath  string   `json:"binaryPath"`
	ServiceName string   `json:"serviceName"`
	ServiceFile string   `json:"serviceFile"`
	RunAsUser   string   `json:"runAsUser"`
	Port        int      `json:"port"`
	Version     string   `json:"pbVersion"`
	Arch        string   `json:"arch"`
	BackupCron  string   `json:"backupCron"`
	Retention   int      `json:"backupRetention"`
	Warnings    []string `json:"warnings,omitempty"`
}

// Defaults computes path and port defaults from server settings.
func (s *Service) Defaults(ctx context.Context, name string) (Defaults, error) {
	cfg := s.Cfg()
	name = normalizeName(name)
	if name == "" {
		name = "new-instance"
	}
	base := filepath.Join(cfg.InstallRoot, name)
	user := systemUser(cfg.ServiceUserPrefix, name)
	port, err := s.nextFreePort(ctx, 0)
	if err != nil {
		return Defaults{}, err
	}
	return Defaults{
		BaseDir: base, DataDir: settings.ExpandDataDir(cfg.PBDataDirTemplate, base),
		BinaryPath: filepath.Join(base, "pocketbase"), ServiceName: name,
		ServiceFile: filepath.Join(cfg.ServiceDir, name+".service"), RunAsUser: user,
		Port: port, Version: cfg.DefaultPBVersion, Arch: defaultArch(), BackupCron: "0 3 * * *",
		Retention: cfg.BackupRetention,
	}, nil
}

// Create validates, records, and enqueues a provisioning job.
func (s *Service) Create(ctx context.Context, in CreateInput, actor string) (*store.Instance, *store.Job, error) {
	inst, domain, err := s.validateCreate(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	if err := s.Store.CreateInstance(ctx, inst); err != nil {
		return nil, nil, err
	}
	if domain != nil {
		if err := s.Store.CreateDomain(ctx, domain); err != nil {
			_ = s.Store.DeleteInstance(ctx, inst.ID)
			return nil, nil, err
		}
	}
	payload := ProvisionPayload{InstanceID: inst.ID, ManageCaddy: in.ManageCaddy, ManageDNS: in.ManageDNS, Checksum: in.Checksum, Arch: archOrDefault(in.Arch)}
	if domain != nil {
		payload.DomainID = domain.ID
	}
	job, _, err := s.Jobs.Enqueue(ctx, "instance.provision", payload, jobs.EnqueueOptions{
		Priority: 10, MaxAttempts: 5, IdempotencyKey: "provision:" + inst.ID, CreatedBy: actor,
	})
	if err != nil {
		_ = s.Store.DeleteInstance(ctx, inst.ID)
		return nil, nil, err
	}
	return inst, job, nil
}

func (s *Service) validateCreate(ctx context.Context, in CreateInput) (*store.Instance, *store.Domain, error) {
	cfg := s.Cfg()
	name := normalizeName(in.Name)
	if !platform.ValidSafeName(name) || len(name) < 2 {
		return nil, nil, errors.New("instance name must be 2-63 characters using letters, digits, dot, underscore, or hyphen")
	}
	hostname := normalizeName(in.Hostname)
	if hostname == "" {
		hostname = name
	}
	if !platform.ValidSafeName(hostname) {
		return nil, nil, errors.New("hostname must be a simple identifier (letters, digits, dot, underscore, hyphen)")
	}
	version := strings.TrimSpace(in.PBVersion)
	if version == "" {
		version = cfg.DefaultPBVersion
	}
	if !regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`).MatchString(version) {
		return nil, nil, errors.New("invalid PocketBase version")
	}
	arch := strings.TrimSpace(in.Arch)
	if arch == "" {
		arch = defaultArch()
	}
	if arch != "amd64" && arch != "arm64" && arch != "armv7" {
		return nil, nil, errors.New("unsupported architecture")
	}
	base := strings.TrimSpace(in.BaseDir)
	if base == "" {
		base = filepath.Join(cfg.InstallRoot, name)
	}
	if err := validateNewRoot(base, cfg); err != nil {
		return nil, nil, err
	}
	data := strings.TrimSpace(in.DataDir)
	if data == "" {
		data = settings.ExpandDataDir(cfg.PBDataDirTemplate, base)
	}
	if data == base {
		return nil, nil, errors.New("data directory must not equal the installation directory")
	}
	if err := platform.ValidateAbsolutePath(data, false); err != nil {
		return nil, nil, fmt.Errorf("data directory: %w", err)
	}
	if err := checkParentTraversal(base); err != nil {
		return nil, nil, err
	}
	if err := checkParentTraversal(data); err != nil {
		return nil, nil, err
	}
	if !platform.IsWithin(base, data) && !platform.IsWithin(filepath.Dir(data), base) {
		// External data directories are supported, but never inside system paths.
		if err := validateNewRoot(data, cfg); err != nil {
			return nil, nil, fmt.Errorf("data directory: %w", err)
		}
	}
	binary := strings.TrimSpace(in.BinaryPath)
	if binary == "" {
		binary = filepath.Join(base, "pocketbase")
	}
	if err := platform.ValidateAbsolutePath(binary, false); err != nil || !platform.IsWithin(base, binary) {
		return nil, nil, errors.New("binary path must be inside the installation directory")
	}
	serviceName := strings.TrimSpace(in.ServiceName)
	if serviceName == "" {
		serviceName = hostname
	}
	serviceName = strings.TrimSuffix(serviceName, ".service")
	if !platform.ValidSafeName(serviceName) {
		return nil, nil, errors.New("invalid systemd service name")
	}
	serviceFile := strings.TrimSpace(in.ServiceFile)
	if serviceFile == "" {
		serviceFile = filepath.Join(cfg.ServiceDir, serviceName+".service")
	}
	if err := platform.ValidateAbsolutePath(serviceFile, false); err != nil || !strings.HasSuffix(serviceFile, ".service") {
		return nil, nil, errors.New("invalid systemd service file path")
	}
	createUser := true
	if in.CreateSystemUser != nil {
		createUser = *in.CreateSystemUser
	}
	runAsUser := strings.TrimSpace(in.RunAsUser)
	if runAsUser == "" && createUser {
		runAsUser = systemUser(cfg.ServiceUserPrefix, name)
	}
	if runAsUser == "" && !in.AllowRootUser {
		return nil, nil, errors.New("a dedicated system user is required; set allowRootUser explicitly to run as root")
	}
	if runAsUser != "" && !platform.ValidSafeName(runAsUser) {
		return nil, nil, errors.New("invalid run-as user")
	}
	port := in.Port
	if port == 0 {
		p, err := s.nextFreePort(ctx, 0)
		if err != nil {
			return nil, nil, err
		}
		port = p
	}
	if port < 1024 || port > 65535 {
		return nil, nil, errors.New("port must be between 1024 and 65535")
	}
	existing, err := s.Store.ListInstances(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range existing {
		if e.Port == port {
			return nil, nil, fmt.Errorf("port %d is already assigned to instance %s", port, e.Name)
		}
		if strings.EqualFold(e.Name, name) || strings.EqualFold(e.Hostname, hostname) {
			return nil, nil, fmt.Errorf("an instance named %s or using hostname %s already exists", name, hostname)
		}
	}
	if !netutil.PortFree(port) {
		return nil, nil, fmt.Errorf("TCP port %d is already in use", port)
	}
	if err := checkServiceFileNew(serviceFile); err != nil {
		return nil, nil, err
	}
	if err := checkBaseNew(base); err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(data); err == nil {
		if err := checkBaseNew(data); err != nil {
			return nil, nil, fmt.Errorf("data directory: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	if in.SMTPProfileID != "" {
		if _, err := s.Store.GetSMTPProfile(ctx, in.SMTPProfileID); err != nil {
			return nil, nil, errors.New("selected SMTP profile does not exist")
		}
	}
	if in.StorageProfileID != "" {
		if _, err := s.Store.GetStorageProfile(ctx, in.StorageProfileID); err != nil {
			return nil, nil, errors.New("selected storage profile does not exist")
		}
	}
	if in.CloudflareAccountID != "" {
		if _, err := s.Store.GetCloudflareAccount(ctx, in.CloudflareAccountID); err != nil {
			return nil, nil, errors.New("selected Cloudflare account does not exist")
		}
	}
	backupCron := strings.TrimSpace(in.BackupCron)
	if backupCron == "" {
		backupCron = "0 3 * * *"
	}
	nextBackup, err := cron.NextFromString(backupCron, time.Now().UTC())
	if err != nil {
		return nil, nil, fmt.Errorf("backup schedule: %w", err)
	}
	retention := in.BackupRetention
	if retention <= 0 {
		retention = cfg.BackupRetention
	}
	if retention < 1 || retention > 365 {
		return nil, nil, errors.New("backup retention must be between 1 and 365")
	}
	var domain *store.Domain
	domainName := strings.TrimSpace(strings.ToLower(in.Domain))
	if domainName != "" {
		if !caddy.ValidDomain(domainName) {
			return nil, nil, errors.New("invalid domain name")
		}
		if in.ManageDNS && in.CloudflareAccountID == "" {
			return nil, nil, errors.New("DNS management requires a Cloudflare account")
		}
		domain = &store.Domain{
			InstanceID: "", Hostname: domainName, CloudflareAccountID: in.CloudflareAccountID,
			Proxied: in.Proxied, DNSStatus: "pending", CaddyStatus: "pending", TLSStatus: "unknown",
		}
	}
	inst := &store.Instance{
		ID: store.NewID(), Name: name, Hostname: hostname, BaseDir: base, BinaryPath: binary,
		DataDir: data, Port: port, ServiceName: serviceName, ServiceFile: serviceFile,
		RunAsUser: runAsUser, PBVersion: version, Status: "provisioning", Health: "unknown",
		Managed: true, Source: "provisioned", Notes: strings.TrimSpace(in.Notes),
		CloudflareAccountID: in.CloudflareAccountID, SMTPProfileID: in.SMTPProfileID,
		StorageProfileID: in.StorageProfileID, CaddyMode: caddyMode(in.ManageCaddy),
		SMTPApplyStatus: "not_applied", StorageApplyStatus: "not_applied",
		PBAuthStatus: "unlinked", BackupEnabled: in.BackupEnabled, BackupCron: backupCron,
		BackupRetention: retention, NextBackupAt: store.FormatTime(nextBackup),
	}
	if domain != nil {
		domain.InstanceID = inst.ID
	}
	return inst, domain, nil
}

func (s *Service) nextFreePort(ctx context.Context, preferred int) (int, error) {
	cfg := s.Cfg()
	existing, err := s.Store.ListInstances(ctx)
	if err != nil {
		return 0, err
	}
	used := map[int]bool{}
	for _, e := range existing {
		used[e.Port] = true
	}
	if preferred > 0 {
		if used[preferred] || !netutil.PortFree(preferred) {
			return 0, fmt.Errorf("port %d is unavailable", preferred)
		}
		return preferred, nil
	}
	for p := cfg.PortStart; p <= cfg.PortEnd; p++ {
		if !used[p] && netutil.PortFree(p) {
			return p, nil
		}
	}
	return 0, errors.New("no free ports available in the configured range")
}

func validateNewRoot(path string, cfg settings.Config) error {
	if err := platform.ValidateAbsolutePath(path, false); err != nil {
		return err
	}
	for _, critical := range []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/dev", "/proc", "/sys", "/var/lib"} {
		if path == critical || platform.IsWithin(critical, path) || platform.IsWithin(path, critical) {
			return fmt.Errorf("path %s is unsafe for an instance", path)
		}
	}
	if path == cfg.InstallRoot {
		return fmt.Errorf("the installation root itself cannot be used as an instance directory")
	}
	for _, protected := range []string{cfg.ServiceDir, filepath.Dir(cfg.CaddyfilePath), cfg.CaddySnippetDir, cfg.BackupDir} {
		if protected != "" && (path == protected || platform.IsWithin(protected, path) || platform.IsWithin(path, protected)) {
			return fmt.Errorf("path %s overlaps protected server path %s", path, protected)
		}
	}
	if path == "/home" || platform.IsWithin("/home", path) {
		return fmt.Errorf("installation under /home is not supported for a managed service")
	}
	return nil
}

func checkParentTraversal(target string) error {
	parent := filepath.Dir(target)
	for {
		info, err := os.Stat(parent)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("parent path %s is not a directory", parent)
			}
			if info.Mode().Perm()&0o001 == 0 {
				return fmt.Errorf("parent directory %s is not traversable by an unprivileged service user (needs other-execute, currently %04o)", parent, info.Mode().Perm())
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent = filepath.Dir(parent)
		if parent == "/" || parent == "." {
			return nil
		}
	}
}

func checkServiceFileNew(path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !strings.Contains(string(raw), "# managed by pbmanager") {
		return fmt.Errorf("service file %s already exists and is not managed by pbmanager; choose another name", path)
	}
	return nil
}

func checkBaseNew(path string) error {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	marker, err := os.ReadFile(filepath.Join(path, ".pbmanager-instance.json"))
	if err != nil || len(marker) == 0 {
		return fmt.Errorf("directory %s is not empty and is not managed by pbmanager", path)
	}
	return nil
}

func caddyMode(manage bool) string {
	if manage {
		return "managed"
	}
	return "unmanaged"
}

func normalizeName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func defaultArch() string {
	switch runtime.GOARCH {
	case "amd64", "arm64", "arm":
		if runtime.GOARCH == "arm" {
			return "armv7"
		}
		return runtime.GOARCH
	default:
		return "amd64"
	}
}

func systemUser(prefix, name string) string {
	user := prefix + "-" + name
	user = regexp.MustCompile(`[^a-zA-Z0-9_-]+`).ReplaceAllString(user, "-")
	user = strings.Trim(user, "-")
	if len(user) > 32 {
		user = user[:32]
	}
	return user
}

func archOrDefault(arch string) string {
	arch = strings.TrimSpace(arch)
	if arch == "" {
		return defaultArch()
	}
	return arch
}

func archForVersion() string { return defaultArch() }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
