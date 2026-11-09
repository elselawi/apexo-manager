package instances

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/netutil"
	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/services/caddy"
	"github.com/apexo/pbmanager/internal/services/dns"
	"github.com/apexo/pbmanager/internal/services/pocketbase"
	"github.com/apexo/pbmanager/internal/services/systemd"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

// Provision is the durable provisioning state machine.
func (s *Service) Provision(ctx context.Context, ex *jobs.Execution) (any, error) {
	var p ProvisionPayload
	if err := ex.Payload(&p); err != nil {
		return nil, err
	}
	inst, err := s.Store.GetInstance(ctx, p.InstanceID)
	if err != nil {
		return nil, err
	}
	var domain *store.Domain
	if p.DomainID != "" {
		domain, err = s.Store.GetDomain(ctx, p.DomainID)
		if err != nil {
			return nil, err
		}
	}
	cfg := s.Cfg()
	ex.Progress(ctx, 5)
	if err := ex.Step(ctx, "validate", "Validate instance name, paths, ports, and service ownership", func(ctx context.Context) (string, error) {
		if err := s.validateProvisionState(ctx, inst); err != nil {
			return "", err
		}
		return "Target and paths are available; no existing unmanaged data will be overwritten.", nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 12)
	if inst.RunAsUser != "" {
		if err := ex.Step(ctx, "system-user", "Create a dedicated system user if needed", func(ctx context.Context) (string, error) {
			created, err := s.Systemd.EnsureUser(ctx, inst.RunAsUser, inst.BaseDir)
			if err != nil {
				return "", err
			}
			if created {
				return "Created system user " + inst.RunAsUser, nil
			}
			return "System user " + inst.RunAsUser + " already exists", nil
		}); err != nil {
			return nil, err
		}
	} else {
		_ = ex.Skip(ctx, "system-user", "No dedicated system user requested")
	}
	ex.Progress(ctx, 20)
	if err := ex.Step(ctx, "directories", "Create installation/data directories and ownership marker", func(ctx context.Context) (string, error) {
		return s.prepareDirectories(ctx, inst)
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 30)

	alreadyInstalled := false
	if v, err := pocketbase.ExecutableVersion(ctx, inst.BinaryPath); err == nil && v == inst.PBVersion {
		alreadyInstalled = true
	}
	if alreadyInstalled {
		_ = ex.Skip(ctx, "download", "Requested PocketBase version is already installed and verified")
		_ = ex.Skip(ctx, "install", "PocketBase binary is already installed")
	} else {
		arch := archOrDefault(p.Arch)
		if err := ex.Step(ctx, "download", "Download and cryptographically verify PocketBase release", func(ctx context.Context) (string, error) {
			filename := settings.ExpandVersion(cfg.ArchiveTemplate, inst.PBVersion, arch)
			cacheDir := filepath.Join(cfg.BackupDir, ".cache", "releases", inst.PBVersion+"-"+arch)
			if err := platform.EnsureDir(cacheDir, 0o700); err != nil {
				return "", err
			}
			cache := filepath.Join(cacheDir, filename)
			result, err := pocketbase.DownloadVerified(ctx, cfg, inst.PBVersion, arch, p.Checksum)
			if err != nil {
				return "", jobs.Retryable(err)
			}
			if err := platform.CopyFile(result.ArchivePath, cache); err != nil {
				return "", err
			}
			_ = os.Remove(result.ArchivePath)
			raw, _ := json.Marshal(map[string]any{"path": cache, "sha256": result.SHA256, "size": result.Size, "checksumSource": result.ChecksumFrom})
			return string(raw), nil
		}); err != nil {
			return nil, err
		}
		ex.Progress(ctx, 50)
		if err := ex.Step(ctx, "install", "Extract and install PocketBase executable atomically", func(ctx context.Context) (string, error) {
			var dl struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal([]byte(ex.StepOutput(ctx, "download")), &dl); err != nil || dl.Path == "" {
				return "", errors.New("verified release archive is missing from job checkpoint")
			}
			tmp, err := os.MkdirTemp("", "pbmanager-install-*")
			if err != nil {
				return "", err
			}
			defer os.RemoveAll(tmp)
			binary, err := pocketbase.ExtractBinary(dl.Path, tmp)
			if err != nil {
				return "", err
			}
			previous, err := pocketbase.InstallBinary(binary, inst.BinaryPath)
			if err != nil {
				return "", err
			}
			if v, err := pocketbase.ExecutableVersion(ctx, inst.BinaryPath); err != nil || v != inst.PBVersion {
				if previous != "" {
					_ = os.Rename(previous, inst.BinaryPath)
				}
				return "", fmt.Errorf("installed executable failed version verification: got %q, want %s", v, inst.PBVersion)
			}
			return "Installed PocketBase " + inst.PBVersion + " at " + inst.BinaryPath, nil
		}); err != nil {
			return nil, err
		}
	}
	ex.Progress(ctx, 65)
	if err := ex.Step(ctx, "systemd", "Write, validate, enable, and reload the systemd unit", func(ctx context.Context) (string, error) {
		content, err := systemd.RenderUnit(systemd.UnitSpec{
			InstanceID: inst.ID, Name: inst.Name, Binary: inst.BinaryPath, BaseDir: inst.BaseDir,
			DataDir: inst.DataDir, Port: inst.Port, User: inst.RunAsUser, Version: inst.PBVersion,
		})
		if err != nil {
			return "", err
		}
		if err := s.Systemd.WriteUnit(ctx, inst.ServiceFile, content, inst.ID, 0o644); err != nil {
			return "", err
		}
		if err := s.Systemd.DaemonReload(ctx); err != nil {
			return "", err
		}
		if err := s.Systemd.Enable(ctx, inst.ServiceName); err != nil {
			return "", err
		}
		return "Systemd unit " + inst.ServiceName + " installed and enabled", nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 75)
	if err := ex.Step(ctx, "start", "Start the service and wait for the PocketBase health endpoint", func(ctx context.Context) (string, error) {
		st, err := s.Systemd.Status(ctx, inst.ServiceName)
		if err != nil {
			return "", err
		}
		if !st.Running {
			if err := s.Systemd.Start(ctx, inst.ServiceName); err != nil {
				return "", err
			}
		}
		if err := pocketbase.New(inst.Port).WaitHealthy(ctx, 90*time.Second); err != nil {
			return "", err
		}
		return "PocketBase is running and healthy on 127.0.0.1:" + fmt.Sprint(inst.Port), nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 78)
	if err := ex.Step(ctx, "superuser", "Create a generated PocketBase superuser and link native management", func(ctx context.Context) (string, error) {
		if err := s.createLinkedSuperuser(ctx, inst); err != nil {
			return "", jobs.Retryable(err)
		}
		return "Generated PocketBase superuser linked; native backups and settings APIs enabled", nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 82)

	if domain != nil {
		if p.ManageCaddy {
			if err := ex.Step(ctx, "caddy", "Add a managed Caddy route and reload Caddy", func(ctx context.Context) (string, error) {
				path, err := s.Caddy.ApplyRoute(ctx, inst, domain.Hostname)
				if err != nil {
					return "", jobs.Retryable(err)
				}
				domain.CaddyManaged = true
				domain.SnippetPath = path
				domain.CaddyStatus = "ok"
				domain.LastCheckedAt = store.Now()
				domain.LastError = ""
				if err := s.Store.UpdateDomain(ctx, domain); err != nil {
					return "", err
				}
				return "Caddy route applied for " + domain.Hostname + " -> 127.0.0.1:" + fmt.Sprint(inst.Port), nil
			}); err != nil {
				return nil, err
			}
		} else {
			_ = ex.Skip(ctx, "caddy", "Caddy management was not requested for this instance")
		}
		if p.ManageDNS {
			if err := ex.Step(ctx, "dns", "Create or update the manager-owned Cloudflare DNS record", func(ctx context.Context) (string, error) {
				rec, err := s.DNS.Ensure(ctx, inst, domain, inst.CloudflareAccountID, domain.Proxied)
				if err != nil {
					return "", jobs.Retryable(err)
				}
				domain.DNSManaged = true
				domain.DNSStatus = "ok"
				domain.LastCheckedAt = store.Now()
				if err := s.Store.UpdateDomain(ctx, domain); err != nil {
					return "", err
				}
				return fmt.Sprintf("Cloudflare %s record %s points to %s", rec.Type, domain.Hostname, rec.Content), nil
			}); err != nil {
				return nil, err
			}
		} else {
			_ = ex.Skip(ctx, "dns", "Cloudflare DNS management was not requested")
		}
		if err := ex.Step(ctx, "verify", "Verify DNS, HTTPS, and application availability", func(ctx context.Context) (string, error) {
			if err := s.verifyDomain(ctx, inst, domain, p.ManageDNS, p.ManageCaddy); err != nil {
				return "", jobs.Retryable(err)
			}
			return "Application and HTTPS checks passed for " + domain.Hostname, nil
		}); err != nil {
			return nil, err
		}
	} else {
		_ = ex.Skip(ctx, "caddy", "No domain requested")
		_ = ex.Skip(ctx, "dns", "No domain requested")
		_ = ex.Skip(ctx, "verify", "No domain requested; local health check already passed")
	}
	ex.Progress(ctx, 95)
	if err := ex.Step(ctx, "finalize", "Record the provisioned state in the inventory", func(ctx context.Context) (string, error) {
		inst.Status = "running"
		inst.Health = "healthy"
		inst.LastHealthAt = store.Now()
		inst.LastSeenAt = store.Now()
		inst.LastHealthError = ""
		if v, err := pocketbase.ExecutableVersion(ctx, inst.BinaryPath); err == nil {
			inst.PBVersion = v
		}
		if err := s.Store.UpdateInstance(ctx, inst); err != nil {
			return "", err
		}
		if err := s.Store.UpdateInstanceRuntime(ctx, inst.ID, "running", "healthy", inst.LastHealthAt, "", inst.LastSeenAt); err != nil {
			return "", err
		}
		return "Instance registered and healthy", nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 100)
	return map[string]any{"instanceId": inst.ID, "name": inst.Name, "port": inst.Port, "version": inst.PBVersion}, nil
}

// ArchOr returns arch or a fallback.
func (p ProvisionPayload) ArchOr(fallback string) string {
	if p.Arch != "" {
		return p.Arch
	}
	return fallback
}

func (s *Service) validateProvisionState(ctx context.Context, inst *store.Instance) error {
	if inst.ID == "" || !platform.ValidSafeName(inst.Name) {
		return errors.New("invalid instance")
	}
	for _, p := range []string{inst.BaseDir, inst.BinaryPath, inst.DataDir, inst.ServiceFile} {
		if err := platform.ValidateAbsolutePath(p, false); err != nil {
			return err
		}
	}
	if !platform.IsWithin(inst.BaseDir, inst.BinaryPath) && inst.BinaryPath != filepath.Join(inst.BaseDir, "pocketbase") {
		return errors.New("binary path must be inside the installation directory")
	}
	if _, err := os.Lstat(inst.BaseDir); err == nil {
		entries, _ := os.ReadDir(inst.BaseDir)
		if len(entries) > 0 {
			raw, err := os.ReadFile(filepath.Join(inst.BaseDir, ".pbmanager-instance.json"))
			if err != nil {
				return errors.New("installation directory is not empty and has no pbmanager ownership marker")
			}
			var marker struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &marker) != nil || marker.ID != inst.ID {
				return errors.New("installation directory belongs to a different instance")
			}
		}
	}
	if raw, err := os.ReadFile(inst.ServiceFile); err == nil {
		if !strings.Contains(string(raw), systemd.UnitMarker(inst.ID)) {
			return fmt.Errorf("service file %s exists and is not managed by this instance", inst.ServiceFile)
		}
	}
	existing, err := s.Store.ListInstances(ctx)
	if err != nil {
		return err
	}
	for _, e := range existing {
		if e.ID != inst.ID && e.Port == inst.Port {
			return fmt.Errorf("port %d is already assigned to %s", inst.Port, e.Name)
		}
		if e.ID != inst.ID && strings.EqualFold(e.Hostname, inst.Hostname) {
			return fmt.Errorf("hostname %s is already assigned", inst.Hostname)
		}
	}
	return nil
}

func (s *Service) prepareDirectories(ctx context.Context, inst *store.Instance) (string, error) {
	if _, err := os.Stat(inst.BaseDir); err == nil {
		entries, _ := os.ReadDir(inst.BaseDir)
		if len(entries) > 0 {
			raw, err := os.ReadFile(filepath.Join(inst.BaseDir, ".pbmanager-instance.json"))
			if err != nil {
				return "", errors.New("refusing to use a non-empty unmanaged installation directory")
			}
			var marker struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &marker) != nil || marker.ID != inst.ID {
				return "", errors.New("installation directory belongs to a different instance")
			}
		}
	}
	if err := platform.EnsureDir(inst.BaseDir, 0o755); err != nil {
		return "", err
	}
	marker, _ := json.MarshalIndent(map[string]any{"id": inst.ID, "name": inst.Name, "createdAt": store.Now(), "managedBy": "pbmanager"}, "", "  ")
	if err := platform.AtomicWrite(filepath.Join(inst.BaseDir, ".pbmanager-instance.json"), marker, 0o644); err != nil {
		return "", err
	}
	if err := platform.EnsureDir(inst.DataDir, 0o750); err != nil {
		return "", err
	}
	if inst.RunAsUser != "" {
		if err := s.Systemd.Chown(ctx, inst.DataDir, inst.RunAsUser); err != nil {
			return "", err
		}
	}
	return "Directories prepared: " + inst.BaseDir + ", " + inst.DataDir, nil
}

func (s *Service) createLinkedSuperuser(ctx context.Context, inst *store.Instance) error {
	if inst.PBSuperuserEmail != "" && inst.PBSuperuserSecret != "" {
		return nil
	}
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	password := hex.EncodeToString(buf)
	email := "admin@" + inst.Name + ".pbmanager.local"
	runner := s.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	// PocketBase's supported superuser command runs against the same data
	// directory. Stop the service first so the command and the running app do
	// not race on SQLite, and run as the instance user to preserve ownership.
	st, err := s.Systemd.Status(ctx, inst.ServiceName)
	if err != nil {
		return err
	}
	if st.Running {
		if err := s.Systemd.Stop(ctx, inst.ServiceName); err != nil {
			return err
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			st, err = s.Systemd.Status(ctx, inst.ServiceName)
			if err == nil && !st.Running {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
		if st.Running {
			return errors.New("PocketBase did not stop before superuser creation")
		}
	}
	args := []string{}
	name := inst.BinaryPath
	if inst.RunAsUser != "" {
		name = "runuser"
		args = append(args, "-u", inst.RunAsUser, "--")
	}
	args = append(args, inst.BinaryPath, "superuser", "upsert", email, password, "--dir", inst.DataDir)
	_, stderr, err := runner.Run(ctx, name, args...)
	if err != nil {
		_ = s.Systemd.Start(ctx, inst.ServiceName)
		_ = pocketbase.New(inst.Port).WaitHealthy(ctx, 60*time.Second)
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("PocketBase superuser setup failed: %s", truncateForLog(msg, 400))
	}
	if err := s.Systemd.Start(ctx, inst.ServiceName); err != nil {
		return err
	}
	if err := pocketbase.New(inst.Port).WaitHealthy(ctx, 90*time.Second); err != nil {
		return err
	}
	sealed, err := s.Secrets.Seal("pb_superuser:"+inst.ID, password)
	if err != nil {
		return err
	}
	client := pocketbase.New(inst.Port)
	session, err := client.AuthSession(ctx, email, password)
	if err != nil {
		return fmt.Errorf("generated superuser could not authenticate: %w", err)
	}
	if err := s.Store.SetManagedPBAuth(ctx, inst.ID, email, sealed, session.RecordID, store.Now()); err != nil {
		return err
	}
	inst.PBSuperuserEmail = email
	inst.PBSuperuserSecret = sealed
	inst.PBSuperuserManaged = true
	inst.PBSuperuserRecordID = session.RecordID
	inst.PBAuthStatus = "linked"
	inst.PBAuthError = ""
	return nil
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (s *Service) verifyDomain(ctx context.Context, inst *store.Instance, d *store.Domain, managedDNS, managedCaddy bool) error {
	if err := pocketbase.New(inst.Port).WaitHealthy(ctx, 60*time.Second); err != nil {
		return err
	}
	if managedDNS {
		if _, err := dns.WaitPropagation(ctx, d.Hostname, 2*time.Minute); err != nil {
			return err
		}
	}
	if managedCaddy {
		deadline := time.Now().Add(3 * time.Minute)
		var last error
		for {
			info := netutil.CheckTLS(ctx, d.Hostname)
			if info.Error == "" {
				d.TLSStatus = "valid"
				d.TLSExpiresAt = info.NotAfter
				d.LastCheckedAt = store.Now()
				d.LastError = ""
				_ = s.Store.UpdateDomain(ctx, d)
				return nil
			}
			last = errors.New(info.Error)
			if time.Now().After(deadline) {
				return fmt.Errorf("HTTPS certificate is not ready: %w", last)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Second):
			}
		}
	}
	return nil
}

func caddyImportLine(dir string) string { return caddy.ImportLine(dir) }
