package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/services/pocketbase"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

// UpdatePayload requests a binary update.
type UpdatePayload struct {
	InstanceID string `json:"instanceId"`
	Version    string `json:"version"`
	Checksum   string `json:"checksum,omitempty"`
}

// DeletePayload requests deletion with explicit safeguards.
type DeletePayload struct {
	InstanceID   string `json:"instanceId"`
	Confirmation string `json:"confirmation"`
	PurgeData    bool   `json:"purgeData"`
	RemoveCaddy  bool   `json:"removeCaddy"`
	RemoveDNS    bool   `json:"removeDns"`
}

// ActionPayload is used by start/stop/restart jobs.
type ActionPayload struct {
	InstanceID string `json:"instanceId"`
}

// Update upgrades the PocketBase binary with a safety backup and rollback.
func (s *Service) Update(ctx context.Context, ex *jobs.Execution) (any, error) {
	var p UpdatePayload
	if err := ex.Payload(&p); err != nil {
		return nil, err
	}
	inst, err := s.Store.GetInstance(ctx, p.InstanceID)
	if err != nil {
		return nil, err
	}
	version := strings.TrimSpace(p.Version)
	if version == "" {
		return nil, errors.New("version is required")
	}
	if version == inst.PBVersion {
		if v, err := pocketbase.ExecutableVersion(ctx, inst.BinaryPath); err == nil && v == version {
			return map[string]any{"instanceId": inst.ID, "version": version, "noop": true}, nil
		}
	}
	cfg := s.Cfg()
	arch := defaultArch()
	if err := ex.Step(ctx, "preflight", "Validate service and current binary", func(ctx context.Context) (string, error) {
		st, err := s.Systemd.Status(ctx, inst.ServiceName)
		if err != nil {
			return "", err
		}
		if !st.Exists {
			return "", errors.New("systemd unit does not exist; import or provision the instance first")
		}
		if _, err := os.Stat(inst.BinaryPath); err != nil {
			return "", fmt.Errorf("current binary not found: %w", err)
		}
		return "Current service and binary are present", nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 10)
	if err := ex.Step(ctx, "safety-backup", "Create a safety backup before updating", func(ctx context.Context) (string, error) {
		b, err := s.Backup.Create(ctx, inst, false, "pre-update safety backup to "+version, func(msg string) {})
		if err != nil {
			return "", err
		}
		return "Safety backup " + b.ID + " created (" + b.Filename + ")", nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 25)
	if err := ex.Step(ctx, "download", "Download and verify the requested release", func(ctx context.Context) (string, error) {
		filename := settings.ExpandVersion(cfg.ArchiveTemplate, version, arch)
		cacheDir := filepath.Join(cfg.BackupDir, ".cache", "releases", version+"-"+arch)
		if err := platform.EnsureDir(cacheDir, 0o700); err != nil {
			return "", err
		}
		cache := filepath.Join(cacheDir, filename)
		res, err := pocketbase.DownloadVerified(ctx, cfg, version, arch, p.Checksum)
		if err != nil {
			return "", jobs.Retryable(err)
		}
		if err := platform.CopyFile(res.ArchivePath, cache); err != nil {
			return "", err
		}
		_ = os.Remove(res.ArchivePath)
		raw, _ := json.Marshal(map[string]any{"path": cache, "sha256": res.SHA256})
		return string(raw), nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 45)
	if err := ex.Step(ctx, "stop", "Stop the instance gracefully", func(ctx context.Context) (string, error) {
		if err := s.Systemd.Stop(ctx, inst.ServiceName); err != nil {
			return "", err
		}
		if err := waitServiceRunning(ctx, s, inst.ServiceName, false, 30*time.Second); err != nil {
			return "", err
		}
		return "Service stopped", nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 55)
	if err := ex.Step(ctx, "install", "Atomically replace the binary", func(ctx context.Context) (string, error) {
		var dl struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(ex.StepOutput(ctx, "download")), &dl); err != nil || dl.Path == "" {
			return "", errors.New("download checkpoint is missing")
		}
		tmp, err := os.MkdirTemp("", "pbmanager-update-*")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tmp)
		binary, err := pocketbase.ExtractBinary(dl.Path, tmp)
		if err != nil {
			return "", err
		}
		if _, err := pocketbase.InstallBinary(binary, inst.BinaryPath); err != nil {
			return "", err
		}
		if v, err := pocketbase.ExecutableVersion(ctx, inst.BinaryPath); err != nil || v != version {
			return "", fmt.Errorf("new binary failed version verification (got %q, want %s)", v, version)
		}
		return "Installed PocketBase " + version + "; previous binary retained", nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 72)
	if err := ex.Step(ctx, "start", "Start the updated instance and verify health", func(ctx context.Context) (string, error) {
		if err := s.Systemd.Start(ctx, inst.ServiceName); err != nil {
			return "", err
		}
		if err := pocketbase.New(inst.Port).WaitHealthy(ctx, 90*time.Second); err != nil {
			return "", err
		}
		return "Updated instance is healthy", nil
	}); err != nil {
		// Roll back to the retained binary.
		rollbackErr := s.rollbackBinary(ctx, inst)
		if rollbackErr != nil {
			return nil, fmt.Errorf("update failed: %w; binary rollback also failed: %v", err, rollbackErr)
		}
		return nil, fmt.Errorf("update failed and was rolled back: %w", err)
	}
	if err := ex.Step(ctx, "finalize", "Record the new version", func(ctx context.Context) (string, error) {
		inst.PBVersion = version
		inst.Status = "running"
		inst.Health = "healthy"
		inst.LastHealthAt = store.Now()
		inst.LastHealthError = ""
		if err := s.Store.UpdateInstance(ctx, inst); err != nil {
			return "", err
		}
		_ = s.Store.UpdateInstanceRuntime(ctx, inst.ID, "running", "healthy", inst.LastHealthAt, "", store.Now())
		return "Version " + version + " recorded", nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 100)
	return map[string]any{"instanceId": inst.ID, "version": version}, nil
}

func (s *Service) rollbackBinary(ctx context.Context, inst *store.Instance) error {
	previous := inst.BinaryPath + ".previous"
	if _, err := os.Stat(previous); err != nil {
		return fmt.Errorf("no previous binary available: %w", err)
	}
	_ = s.Systemd.Stop(ctx, inst.ServiceName)
	if err := os.Rename(previous, inst.BinaryPath); err != nil {
		return err
	}
	if err := s.Systemd.Start(ctx, inst.ServiceName); err != nil {
		return err
	}
	return pocketbase.New(inst.Port).WaitHealthy(ctx, 60*time.Second)
}

// Start starts an instance.
func (s *Service) Start(ctx context.Context, ex *jobs.Execution) (any, error) {
	inst, err := s.instanceFromPayload(ctx, ex)
	if err != nil {
		return nil, err
	}
	if err := ex.Step(ctx, "enable", "Ensure the unit is enabled at boot", func(ctx context.Context) (string, error) {
		if err := s.Systemd.Enable(ctx, inst.ServiceName); err != nil {
			return "", err
		}
		return "Unit enabled", nil
	}); err != nil {
		return nil, err
	}
	if err := ex.Step(ctx, "start", "Start the service and wait for health", func(ctx context.Context) (string, error) {
		if err := s.Systemd.Start(ctx, inst.ServiceName); err != nil {
			return "", err
		}
		if err := pocketbase.New(inst.Port).WaitHealthy(ctx, 60*time.Second); err != nil {
			return "", err
		}
		return "Instance is healthy", nil
	}); err != nil {
		return nil, err
	}
	_ = s.Store.UpdateInstanceRuntime(ctx, inst.ID, "running", "healthy", store.Now(), "", store.Now())
	return map[string]any{"instanceId": inst.ID, "status": "running"}, nil
}

// Stop stops an instance.
func (s *Service) Stop(ctx context.Context, ex *jobs.Execution) (any, error) {
	inst, err := s.instanceFromPayload(ctx, ex)
	if err != nil {
		return nil, err
	}
	if err := ex.Step(ctx, "stop", "Stop the service gracefully", func(ctx context.Context) (string, error) {
		if err := s.Systemd.Stop(ctx, inst.ServiceName); err != nil {
			return "", err
		}
		if err := waitServiceRunning(ctx, s, inst.ServiceName, false, 30*time.Second); err != nil {
			return "", err
		}
		return "Service stopped", nil
	}); err != nil {
		return nil, err
	}
	_ = s.Store.UpdateInstanceRuntime(ctx, inst.ID, "stopped", "down", store.Now(), "", inst.LastSeenAt)
	return map[string]any{"instanceId": inst.ID, "status": "stopped"}, nil
}

// Restart restarts and verifies an instance.
func (s *Service) Restart(ctx context.Context, ex *jobs.Execution) (any, error) {
	inst, err := s.instanceFromPayload(ctx, ex)
	if err != nil {
		return nil, err
	}
	if err := ex.Step(ctx, "restart", "Restart the service and wait for health", func(ctx context.Context) (string, error) {
		if err := s.Systemd.Restart(ctx, inst.ServiceName); err != nil {
			return "", err
		}
		if err := pocketbase.New(inst.Port).WaitHealthy(ctx, 90*time.Second); err != nil {
			return "", err
		}
		return "Instance is healthy", nil
	}); err != nil {
		return nil, err
	}
	_ = s.Store.UpdateInstanceRuntime(ctx, inst.ID, "running", "healthy", store.Now(), "", store.Now())
	return map[string]any{"instanceId": inst.ID, "status": "running"}, nil
}

// Delete removes service management and optionally archives data. It never
// recursively deletes PocketBase data.
func (s *Service) Delete(ctx context.Context, ex *jobs.Execution) (any, error) {
	var p DeletePayload
	if err := ex.Payload(&p); err != nil {
		return nil, err
	}
	inst, err := s.Store.GetInstance(ctx, p.InstanceID)
	if err != nil {
		return nil, err
	}
	if p.Confirmation != inst.Name {
		return nil, errors.New("deletion confirmation does not match the instance name")
	}
	cfg := s.Cfg()
	if p.PurgeData {
		if err := ex.Step(ctx, "safety-backup", "Create a final backup before archiving data", func(ctx context.Context) (string, error) {
			b, err := s.Backup.Create(ctx, inst, false, "final backup before deletion archive", func(string) {})
			if err != nil {
				return "", err
			}
			return "Final backup " + b.ID, nil
		}); err != nil {
			return nil, err
		}
	}
	if err := ex.Step(ctx, "stop", "Stop and disable the service", func(ctx context.Context) (string, error) {
		if err := s.Systemd.Stop(ctx, inst.ServiceName); err != nil {
			return "", err
		}
		if err := waitServiceRunning(ctx, s, inst.ServiceName, false, 30*time.Second); err != nil {
			return "", err
		}
		if err := s.Systemd.Disable(ctx, inst.ServiceName); err != nil {
			return "", err
		}
		return "Service stopped and disabled", nil
	}); err != nil {
		return nil, err
	}
	domains, _ := s.Store.ListDomainsByInstance(ctx, inst.ID)
	if p.RemoveCaddy {
		if err := ex.Step(ctx, "caddy", "Remove only pbmanager-managed Caddy routes", func(ctx context.Context) (string, error) {
			for _, d := range domains {
				if d.CaddyManaged {
					if err := s.Caddy.RemoveRoute(ctx, d.Hostname); err != nil {
						return "", err
					}
				}
			}
			return "Managed Caddy routes removed", nil
		}); err != nil {
			return nil, err
		}
	}
	if p.RemoveDNS {
		if err := ex.Step(ctx, "dns", "Remove only pbmanager-owned Cloudflare records", func(ctx context.Context) (string, error) {
			for _, d := range domains {
				if d.DNSManaged {
					if err := s.DNS.Remove(ctx, &d); err != nil {
						return "", err
					}
					d.DNSRecordID = ""
					d.DNSManaged = false
					d.DNSStatus = "removed"
					_ = s.Store.UpdateDomain(ctx, &d)
				}
			}
			return "Managed DNS records removed", nil
		}); err != nil {
			return nil, err
		}
	}
	if inst.Managed {
		if err := ex.Step(ctx, "unit", "Remove the managed systemd unit and reload systemd", func(ctx context.Context) (string, error) {
			if err := s.Systemd.RemoveUnit(inst.ServiceFile, inst.ID); err != nil {
				return "", err
			}
			if err := s.Systemd.DaemonReload(ctx); err != nil {
				return "", err
			}
			return "Managed systemd unit removed", nil
		}); err != nil {
			return nil, err
		}
	} else {
		_ = ex.Skip(ctx, "unit", "Imported service file was not created by pbmanager and was left in place")
	}
	archivePath := ""
	if p.PurgeData {
		if err := ex.Step(ctx, "archive-data", "Move PocketBase data into the protected purge archive (no recursive deletion)", func(ctx context.Context) (string, error) {
			destRoot := filepath.Join(cfg.BackupDir, "purged", inst.Name+"-"+time.Now().UTC().Format("20060102T150405Z"))
			if err := platform.EnsureDir(destRoot, 0o700); err != nil {
				return "", err
			}
			dest := filepath.Join(destRoot, filepath.Base(inst.DataDir))
			if err := os.Rename(inst.DataDir, dest); err != nil {
				return "", fmt.Errorf("could not archive data directory (left in place): %w", err)
			}
			archivePath = dest
			return "Data archived at " + dest + "; no application data was deleted", nil
		}); err != nil {
			return nil, err
		}
	}
	if err := ex.Step(ctx, "forget", "Remove the instance from the manager inventory", func(ctx context.Context) (string, error) {
		if err := s.Store.DeleteInstance(ctx, inst.ID); err != nil {
			return "", err
		}
		return "Inventory record removed", nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{"instanceId": inst.ID, "archivedData": archivePath}, nil
}

func (s *Service) instanceFromPayload(ctx context.Context, ex *jobs.Execution) (*store.Instance, error) {
	var p ActionPayload
	if err := ex.Payload(&p); err != nil {
		return nil, err
	}
	return s.Store.GetInstance(ctx, p.InstanceID)
}

func waitServiceRunning(ctx context.Context, s *Service, service string, want bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := s.Systemd.Status(ctx, service)
		if err == nil && st.Running == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for service %s to reach running=%v", service, want)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
