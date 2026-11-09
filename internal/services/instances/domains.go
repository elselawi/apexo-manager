package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/cron"
	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/services/caddy"
	"github.com/apexo/pbmanager/internal/store"
)

// ImportInput registers an existing installation without modifying it.
type ImportInput struct {
	Name           string          `json:"name"`
	Hostname       string          `json:"hostname"`
	BaseDir        string          `json:"baseDir"`
	BinaryPath     string          `json:"binaryPath"`
	DataDir        string          `json:"dataDir"`
	Port           int             `json:"port"`
	ServiceName    string          `json:"serviceName"`
	ServiceFile    string          `json:"serviceFile"`
	RunAsUser      string          `json:"runAsUser"`
	PBVersion      string          `json:"pbVersion"`
	Domains        []string        `json:"domains"`
	Notes          string          `json:"notes"`
	Source         string          `json:"source"`
	DiscoveredJSON json.RawMessage `json:"discovered"`
	Managed        bool            `json:"managed"`
}

// Import records an existing installation. It never writes to the installation,
// service, Caddy, or DNS.
func (s *Service) Import(ctx context.Context, in ImportInput, actor string) (*store.Instance, error) {
	name := normalizeName(in.Name)
	if !platform.ValidSafeName(name) {
		return nil, errors.New("invalid instance name")
	}
	hostname := normalizeName(in.Hostname)
	if hostname == "" {
		hostname = name
	}
	if !platform.ValidSafeName(hostname) {
		return nil, errors.New("invalid instance hostname")
	}
	if in.Port < 1 || in.Port > 65535 {
		return nil, errors.New("port is required and must be between 1 and 65535")
	}
	if !filepath.IsAbs(in.BinaryPath) || !filepath.IsAbs(in.DataDir) {
		return nil, errors.New("binary and data directory must be absolute paths")
	}
	base := strings.TrimSpace(in.BaseDir)
	if base == "" {
		base = filepath.Dir(in.BinaryPath)
	}
	if !filepath.IsAbs(base) {
		return nil, errors.New("base directory must be absolute")
	}
	serviceName := strings.TrimSuffix(strings.TrimSpace(in.ServiceName), ".service")
	serviceFile := strings.TrimSpace(in.ServiceFile)
	if serviceName == "" && serviceFile != "" {
		serviceName = strings.TrimSuffix(filepath.Base(serviceFile), ".service")
	}
	if serviceName != "" && !platform.ValidSafeName(serviceName) {
		return nil, errors.New("invalid systemd service name")
	}
	if serviceFile != "" && !filepath.IsAbs(serviceFile) {
		return nil, errors.New("service file must be absolute")
	}
	existing, err := s.Store.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range existing {
		if strings.EqualFold(e.Name, name) || strings.EqualFold(e.Hostname, hostname) {
			return nil, fmt.Errorf("an instance named %s or using hostname %s already exists", name, hostname)
		}
		if e.Port == in.Port {
			return nil, fmt.Errorf("port %d is already assigned to %s", in.Port, e.Name)
		}
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = "imported"
	}
	inst := &store.Instance{
		Name: name, Hostname: hostname, BaseDir: base, BinaryPath: in.BinaryPath, DataDir: in.DataDir,
		Port: in.Port, ServiceName: serviceName, ServiceFile: serviceFile, RunAsUser: in.RunAsUser,
		PBVersion: in.PBVersion, Status: "unknown", Health: "unknown", Managed: in.Managed,
		Source: source, Notes: in.Notes, DiscoveredJSON: string(in.DiscoveredJSON),
		CaddyMode: "unmanaged", SMTPApplyStatus: "not_applied", StorageApplyStatus: "not_applied",
		PBAuthStatus: "unlinked", BackupEnabled: false, BackupCron: "0 3 * * *",
		BackupRetention: s.Cfg().BackupRetention,
	}
	if err := s.Store.CreateInstance(ctx, inst); err != nil {
		return nil, err
	}
	for _, host := range in.Domains {
		host = strings.TrimSpace(strings.ToLower(host))
		if !caddy.ValidDomain(host) {
			continue
		}
		d := &store.Domain{InstanceID: inst.ID, Hostname: host, DNSStatus: "unknown", CaddyStatus: "unknown", TLSStatus: "unknown"}
		if err := s.Store.CreateDomain(ctx, d); err != nil {
			_ = s.Store.DeleteInstance(ctx, inst.ID)
			return nil, err
		}
	}
	return inst, nil
}

// SettingsInput updates non-path instance settings.
type SettingsInput struct {
	Notes               *string `json:"notes"`
	BackupEnabled       *bool   `json:"backupEnabled"`
	BackupCron          *string `json:"backupCron"`
	BackupRetention     *int    `json:"backupRetention"`
	SMTPProfileID       *string `json:"smtpProfileId"`
	StorageProfileID    *string `json:"storageProfileId"`
	CloudflareAccountID *string `json:"cloudflareAccountId"`
}

// UpdateSettings changes safe per-instance settings.
func (s *Service) UpdateSettings(ctx context.Context, id string, in SettingsInput) (*store.Instance, error) {
	inst, err := s.Store.GetInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Notes != nil {
		inst.Notes = strings.TrimSpace(*in.Notes)
	}
	if in.SMTPProfileID != nil {
		v := strings.TrimSpace(*in.SMTPProfileID)
		if v != "" {
			if _, err := s.Store.GetSMTPProfile(ctx, v); err != nil {
				return nil, errors.New("SMTP profile does not exist")
			}
		}
		changed := v != inst.SMTPProfileID
		inst.SMTPProfileID = v
		if changed {
			inst.SMTPApplyStatus = "not_applied"
		}
	}
	if in.StorageProfileID != nil {
		v := strings.TrimSpace(*in.StorageProfileID)
		if v != "" {
			if _, err := s.Store.GetStorageProfile(ctx, v); err != nil {
				return nil, errors.New("storage profile does not exist")
			}
		}
		inst.StorageProfileID = v
		inst.StorageApplyStatus = "not_applied"
	}
	if in.CloudflareAccountID != nil {
		v := strings.TrimSpace(*in.CloudflareAccountID)
		if v != "" {
			if _, err := s.Store.GetCloudflareAccount(ctx, v); err != nil {
				return nil, errors.New("Cloudflare account does not exist")
			}
		}
		inst.CloudflareAccountID = v
	}
	cronExpr := inst.BackupCron
	if in.BackupCron != nil {
		cronExpr = strings.TrimSpace(*in.BackupCron)
	}
	retention := inst.BackupRetention
	if in.BackupRetention != nil {
		retention = *in.BackupRetention
	}
	if retention < 1 || retention > 365 {
		return nil, errors.New("backup retention must be between 1 and 365")
	}
	enabled := inst.BackupEnabled
	if in.BackupEnabled != nil {
		enabled = *in.BackupEnabled
	}
	next := time.Time{}
	if enabled {
		next, err = cron.NextFromString(cronExpr, time.Now().UTC())
		if err != nil {
			return nil, fmt.Errorf("backup schedule: %w", err)
		}
	}
	inst.BackupCron = cronExpr
	inst.BackupRetention = retention
	inst.BackupEnabled = enabled
	if next.IsZero() {
		inst.NextBackupAt = ""
	} else {
		inst.NextBackupAt = store.FormatTime(next)
	}
	if err := s.Store.UpdateInstance(ctx, inst); err != nil {
		return nil, err
	}
	if in.SMTPProfileID != nil || in.StorageProfileID != nil {
		// Assignment changes require an explicit apply action; reset recorded state.
		if in.SMTPProfileID != nil {
			_ = s.Store.SetApplyState(ctx, inst.ID, "smtp", "not_applied", "", "")
		}
		if in.StorageProfileID != nil {
			_ = s.Store.SetApplyState(ctx, inst.ID, "storage", "not_applied", "", "")
		}
	}
	return inst, nil
}

// DomainInput attaches a domain.
type DomainInput struct {
	Hostname            string `json:"hostname"`
	CloudflareAccountID string `json:"cloudflareAccountId"`
	Proxied             bool   `json:"proxied"`
	ManageCaddy         bool   `json:"manageCaddy"`
	ManageDNS           bool   `json:"manageDns"`
}

// DomainPayload drives attach/remove jobs.
type DomainPayload struct {
	DomainID     string `json:"domainId"`
	ManageCaddy  bool   `json:"manageCaddy"`
	ManageDNS    bool   `json:"manageDns"`
	RemoveCaddy  bool   `json:"removeCaddy"`
	RemoveDNS    bool   `json:"removeDns"`
	Confirmation string `json:"confirmation,omitempty"`
}

// AttachDomain creates a mapping and enqueues integration work.
func (s *Service) AttachDomain(ctx context.Context, instanceID string, in DomainInput, actor string) (*store.Domain, *store.Job, error) {
	inst, err := s.Store.GetInstance(ctx, instanceID)
	if err != nil {
		return nil, nil, err
	}
	host := strings.TrimSpace(strings.ToLower(in.Hostname))
	if !caddy.ValidDomain(host) {
		return nil, nil, errors.New("invalid domain name")
	}
	if _, err := s.Store.GetDomainByHostname(ctx, host); err == nil {
		return nil, nil, errors.New("this domain is already mapped")
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, nil, err
	}
	account := strings.TrimSpace(in.CloudflareAccountID)
	if account == "" {
		account = inst.CloudflareAccountID
	}
	if in.ManageDNS {
		if account == "" {
			return nil, nil, errors.New("DNS management requires a Cloudflare account")
		}
		if _, err := s.Store.GetCloudflareAccount(ctx, account); err != nil {
			return nil, nil, errors.New("Cloudflare account does not exist")
		}
	}
	if in.ManageCaddy {
		view, err := s.Caddy.ReadConfig(ctx)
		if err == nil {
			for _, site := range view.Sites {
				for _, addr := range site.Addresses {
					if addr == host {
						return nil, nil, fmt.Errorf("Caddy already has an unmanaged site block for %s; remove or rename it manually before attaching", host)
					}
				}
			}
		}
	}
	d := &store.Domain{
		InstanceID: inst.ID, Hostname: host, CloudflareAccountID: account, Proxied: in.Proxied,
		DNSStatus: "pending", CaddyStatus: "pending", TLSStatus: "unknown",
	}
	if err := s.Store.CreateDomain(ctx, d); err != nil {
		return nil, nil, err
	}
	if !in.ManageCaddy && !in.ManageDNS {
		d.DNSStatus, d.CaddyStatus = "unmanaged", "unmanaged"
		_ = s.Store.UpdateDomain(ctx, d)
		return d, nil, nil
	}
	job, _, err := s.Jobs.Enqueue(ctx, "domain.attach", DomainPayload{
		DomainID: d.ID, ManageCaddy: in.ManageCaddy, ManageDNS: in.ManageDNS,
	}, jobs.EnqueueOptions{Priority: 5, IdempotencyKey: "domain.attach:" + d.ID, CreatedBy: actor})
	if err != nil {
		_ = s.Store.DeleteDomain(ctx, d.ID)
		return nil, nil, err
	}
	return d, job, nil
}

// AttachDomainJob applies Caddy/DNS for an attached domain.
func (s *Service) AttachDomainJob(ctx context.Context, ex *jobs.Execution) (any, error) {
	var p DomainPayload
	if err := ex.Payload(&p); err != nil {
		return nil, err
	}
	d, err := s.Store.GetDomain(ctx, p.DomainID)
	if err != nil {
		return nil, err
	}
	inst, err := s.Store.GetInstance(ctx, d.InstanceID)
	if err != nil {
		return nil, err
	}
	if p.ManageCaddy {
		if err := ex.Step(ctx, "caddy", "Apply managed Caddy route", func(ctx context.Context) (string, error) {
			path, err := s.Caddy.ApplyRoute(ctx, inst, d.Hostname)
			if err != nil {
				return "", jobs.Retryable(err)
			}
			d.CaddyManaged = true
			d.SnippetPath = path
			d.CaddyStatus = "ok"
			d.LastCheckedAt = store.Now()
			if err := s.Store.UpdateDomain(ctx, d); err != nil {
				return "", err
			}
			return "Caddy route applied", nil
		}); err != nil {
			return nil, err
		}
	} else {
		_ = ex.Skip(ctx, "caddy", "Caddy management not requested")
	}
	if p.ManageDNS {
		if err := ex.Step(ctx, "dns", "Create or update Cloudflare DNS record", func(ctx context.Context) (string, error) {
			rec, err := s.DNS.Ensure(ctx, inst, d, d.CloudflareAccountID, d.Proxied)
			if err != nil {
				return "", jobs.Retryable(err)
			}
			d.DNSManaged = true
			d.DNSStatus = "ok"
			d.LastCheckedAt = store.Now()
			if err := s.Store.UpdateDomain(ctx, d); err != nil {
				return "", err
			}
			return fmt.Sprintf("Cloudflare %s record points to %s", rec.Type, rec.Content), nil
		}); err != nil {
			return nil, err
		}
	} else {
		_ = ex.Skip(ctx, "dns", "Cloudflare DNS management not requested")
	}
	if p.ManageCaddy || p.ManageDNS {
		if err := ex.Step(ctx, "verify", "Verify public DNS and HTTPS", func(ctx context.Context) (string, error) {
			if err := s.verifyDomain(ctx, inst, d, p.ManageDNS, p.ManageCaddy); err != nil {
				return "", jobs.Retryable(err)
			}
			return "DNS and HTTPS verification passed", nil
		}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"domainId": d.ID, "hostname": d.Hostname}, nil
}

// RemoveDomain enqueues removal of a mapping and its managed integrations.
func (s *Service) RemoveDomain(ctx context.Context, domainID string, removeCaddy, removeDNS bool, confirmation, actor string) (*store.Job, error) {
	d, err := s.Store.GetDomain(ctx, domainID)
	if err != nil {
		return nil, err
	}
	if confirmation != d.Hostname {
		return nil, errors.New("confirmation must exactly match the domain name")
	}
	job, _, err := s.Jobs.Enqueue(ctx, "domain.remove", DomainPayload{
		DomainID: domainID, RemoveCaddy: removeCaddy, RemoveDNS: removeDNS, Confirmation: confirmation,
	}, jobs.EnqueueOptions{Priority: 5, IdempotencyKey: "domain.remove:" + domainID, CreatedBy: actor})
	return job, err
}

// RemoveDomainJob removes integrations only when ownership is recorded.
func (s *Service) RemoveDomainJob(ctx context.Context, ex *jobs.Execution) (any, error) {
	var p DomainPayload
	if err := ex.Payload(&p); err != nil {
		return nil, err
	}
	d, err := s.Store.GetDomain(ctx, p.DomainID)
	if err != nil {
		return nil, err
	}
	if p.Confirmation != d.Hostname {
		return nil, errors.New("confirmation mismatch")
	}
	if p.RemoveDNS && d.DNSManaged {
		if err := ex.Step(ctx, "dns", "Delete the pbmanager-owned Cloudflare record", func(ctx context.Context) (string, error) {
			if err := s.DNS.Remove(ctx, d); err != nil {
				return "", jobs.Retryable(err)
			}
			return "DNS record removed", nil
		}); err != nil {
			return nil, err
		}
	} else {
		_ = ex.Skip(ctx, "dns", "DNS record not managed or removal not requested")
	}
	if p.RemoveCaddy && d.CaddyManaged {
		if err := ex.Step(ctx, "caddy", "Remove the pbmanager-managed Caddy snippet", func(ctx context.Context) (string, error) {
			if err := s.Caddy.RemoveRoute(ctx, d.Hostname); err != nil {
				return "", jobs.Retryable(err)
			}
			return "Caddy route removed", nil
		}); err != nil {
			return nil, err
		}
	} else {
		_ = ex.Skip(ctx, "caddy", "Caddy route not managed or removal not requested")
	}
	if err := ex.Step(ctx, "forget", "Remove domain mapping", func(ctx context.Context) (string, error) {
		if err := s.Store.DeleteDomain(ctx, d.ID); err != nil {
			return "", err
		}
		return "Domain mapping removed", nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{"domainId": d.ID, "hostname": d.Hostname}, nil
}

// SyncCaddy applies every managed domain for an instance.
func (s *Service) SyncCaddy(ctx context.Context, ex *jobs.Execution) (any, error) {
	inst, err := s.instanceFromPayload(ctx, ex)
	if err != nil {
		return nil, err
	}
	domains, err := s.Store.ListDomainsByInstance(ctx, inst.ID)
	if err != nil {
		return nil, err
	}
	applied := 0
	for i := range domains {
		d := &domains[i]
		if !d.CaddyManaged {
			continue
		}
		if err := ex.Step(ctx, "route:"+d.Hostname, "Reapply managed route "+d.Hostname, func(ctx context.Context) (string, error) {
			path, err := s.Caddy.ApplyRoute(ctx, inst, d.Hostname)
			if err != nil {
				return "", jobs.Retryable(err)
			}
			d.SnippetPath = path
			d.CaddyStatus = "ok"
			if err := s.Store.UpdateDomain(ctx, d); err != nil {
				return "", err
			}
			return "Route applied", nil
		}); err != nil {
			return nil, err
		}
		applied++
	}
	return map[string]any{"instanceId": inst.ID, "applied": applied}, nil
}
