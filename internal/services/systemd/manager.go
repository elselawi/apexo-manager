// Package systemd provides argument-only wrappers for service management.
package systemd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/apexo/pbmanager/internal/platform"
)

// Status is a snapshot of a systemd unit.
type Status struct {
	Name        string `json:"name"`
	LoadState   string `json:"loadState"`
	ActiveState string `json:"activeState"`
	SubState    string `json:"subState"`
	MainPID     int    `json:"mainPid"`
	MemoryBytes int64  `json:"memoryBytes"`
	CPUUsageNS  int64  `json:"cpuUsageNs"`
	StartedAt   string `json:"startedAt,omitempty"`
	Fragment    string `json:"fragmentPath,omitempty"`
	User        string `json:"user,omitempty"`
	Running     bool   `json:"running"`
	Exists      bool   `json:"exists"`
}

// Manager wraps systemctl and journalctl.
type Manager struct {
	Runner           platform.Runner
	SystemctlBinary  string
	JournalctlBinary string
	// AllowUnitWrite is a safety switch for tests.
	AllowUnitWrite bool
}

func (m *Manager) runner() platform.Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return platform.ExecRunner{}
}
func (m *Manager) systemctl() string {
	if m.SystemctlBinary != "" {
		return m.SystemctlBinary
	}
	return "systemctl"
}
func (m *Manager) journalctl() string {
	if m.JournalctlBinary != "" {
		return m.JournalctlBinary
	}
	return "journalctl"
}

// ValidServiceName validates a unit basename or unit name.
func ValidServiceName(s string) bool {
	s = strings.TrimSuffix(s, ".service")
	return platform.ValidSafeName(s)
}

// UnitName normalizes a service name to a .service unit.
func UnitName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(name, ".service") {
		return name
	}
	return name + ".service"
}

// Status reads a unit without changing it.
func (m *Manager) Status(ctx context.Context, service string) (Status, error) {
	if !ValidServiceName(service) {
		return Status{}, fmt.Errorf("invalid service name %q", service)
	}
	unit := UnitName(service)
	st := Status{Name: unit}
	out, _, err := m.runner().Run(ctx, m.systemctl(), "show", unit, "--no-pager",
		"--property=LoadState,ActiveState,SubState,MainPID,MemoryCurrent,CPUUsageNSec,ExecMainStartTimestamp,FragmentPath,User")
	if err != nil {
		// `systemctl show` for a missing unit often still exits 0, but a host
		// without systemd will fail. Surface that distinctly.
		return st, err
	}
	props := parseProperties(string(out))
	st.LoadState = props["LoadState"]
	st.ActiveState = props["ActiveState"]
	st.SubState = props["SubState"]
	st.StartedAt = props["ExecMainStartTimestamp"]
	st.Fragment = props["FragmentPath"]
	st.User = props["User"]
	st.MainPID, _ = strconv.Atoi(props["MainPID"])
	st.MemoryBytes = parseSystemdBytes(props["MemoryCurrent"])
	st.CPUUsageNS, _ = strconv.ParseInt(props["CPUUsageNSec"], 10, 64)
	st.Exists = st.LoadState != "" && st.LoadState != "not-found"
	st.Running = st.ActiveState == "active" || st.SubState == "running"
	return st, nil
}

// Start starts a unit.
func (m *Manager) Start(ctx context.Context, service string) error {
	return m.act(ctx, "start", service)
}

// ResetFailed clears a unit's failed/start-limit state before a deliberate
// restart after a controlled stop.
func (m *Manager) ResetFailed(ctx context.Context, service string) error {
	return m.act(ctx, "reset-failed", service)
}

// Stop stops a unit.
func (m *Manager) Stop(ctx context.Context, service string) error {
	return m.act(ctx, "stop", service)
}

// Restart restarts a unit.
func (m *Manager) Restart(ctx context.Context, service string) error {
	return m.act(ctx, "restart", service)
}

// Enable enables a unit at boot.
func (m *Manager) Enable(ctx context.Context, service string) error {
	return m.act(ctx, "enable", service)
}

// Disable disables a unit.
func (m *Manager) Disable(ctx context.Context, service string) error {
	return m.act(ctx, "disable", service)
}

// ReloadUnit asks systemd to reload one unit.
func (m *Manager) ReloadUnit(ctx context.Context, service string) error {
	return m.act(ctx, "reload", service)
}

// DaemonReload refreshes systemd's unit cache.
func (m *Manager) DaemonReload(ctx context.Context) error {
	_, _, err := m.runner().Run(ctx, m.systemctl(), "daemon-reload")
	return err
}

func (m *Manager) act(ctx context.Context, action, service string) error {
	if !ValidServiceName(service) {
		return fmt.Errorf("invalid service name %q", service)
	}
	_, stderr, err := m.runner().Run(ctx, m.systemctl(), action, UnitName(service))
	if err != nil {
		if len(stderr) > 0 {
			return fmt.Errorf("systemctl %s %s: %s", action, service, strings.TrimSpace(string(stderr)))
		}
		return err
	}
	return nil
}

// Logs reads bounded journal output.
func (m *Manager) Logs(ctx context.Context, service string, lines int) (string, error) {
	if !ValidServiceName(service) {
		return "", fmt.Errorf("invalid service name %q", service)
	}
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	out, stderr, err := m.runner().Run(ctx, m.journalctl(), "-u", UnitName(service), "-n", strconv.Itoa(lines), "--no-pager", "--output=short-iso")
	if err != nil {
		if len(stderr) > 0 {
			return "", fmt.Errorf("journalctl: %s", strings.TrimSpace(string(stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// ServiceFile is a unit file on disk.
type ServiceFile struct {
	Path    string
	Content string
}

// ReadUnit reads a regular unit file.
func ReadUnit(path string) (ServiceFile, error) {
	if err := platform.ValidateAbsolutePath(path, false); err != nil {
		return ServiceFile{}, err
	}
	raw, err := platform.ReadFileLimited(path, 512<<10)
	if err != nil {
		return ServiceFile{}, err
	}
	return ServiceFile{Path: path, Content: string(raw)}, nil
}

// UnitMarker identifies units written by pbmanager.
func UnitMarker(instanceID string) string {
	return "# managed by pbmanager instance: " + instanceID
}

// WriteUnit atomically writes a managed unit, refusing to overwrite an
// unmanaged file. It validates with systemd-analyze when available.
func (m *Manager) WriteUnit(ctx context.Context, path string, content []byte, instanceID string, mode os.FileMode) error {
	if !m.AllowUnitWrite {
		return errors.New("unit writes are disabled")
	}
	if err := platform.ValidateAbsolutePath(path, false); err != nil {
		return err
	}
	if !strings.HasSuffix(path, ".service") {
		return errors.New("unit path must end in .service")
	}
	if len(content) > 256<<10 {
		return errors.New("unit content is too large")
	}
	if !strings.Contains(string(content), UnitMarker(instanceID)) {
		return errors.New("refusing to write a unit without the management marker")
	}
	if existing, err := os.ReadFile(path); err == nil {
		if !strings.Contains(string(existing), UnitMarker(instanceID)) && !strings.Contains(string(existing), "# managed by pbmanager") {
			return fmt.Errorf("refusing to overwrite existing unmanaged unit %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	verifyDir, err := os.MkdirTemp("", "pbmanager-unit-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(verifyDir)
	verifyPath := filepath.Join(verifyDir, filepath.Base(path))
	if err := os.WriteFile(verifyPath, content, 0o600); err != nil {
		return err
	}
	// systemd-analyze verify is best-effort: missing binary support should not
	// prevent installation on unusual hosts, but a reported invalid unit does.
	if _, _, err := m.runner().Run(ctx, "systemd-analyze", "verify", verifyPath); err != nil {
		var ce *platform.CommandError
		if errors.As(err, &ce) {
			return fmt.Errorf("unit validation failed: %w", err)
		}
	}
	if err := platform.AtomicWrite(path, content, mode); err != nil {
		return err
	}
	return nil
}

// RemoveUnit deletes a managed unit after checking its marker.
func (m *Manager) RemoveUnit(path, instanceID string) error {
	if !m.AllowUnitWrite {
		return errors.New("unit writes are disabled")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !strings.Contains(string(raw), UnitMarker(instanceID)) {
		return fmt.Errorf("refusing to remove unit %s without expected marker", path)
	}
	return os.Remove(path)
}

// EnsureUser creates a system user when it does not exist. It returns whether
// the user was created.
func (m *Manager) EnsureUser(ctx context.Context, user, home string) (bool, error) {
	if !platform.ValidSafeName(user) {
		return false, fmt.Errorf("invalid user name %q", user)
	}
	if err := platform.ValidateAbsolutePath(home, false); err != nil {
		return false, err
	}
	if _, _, err := m.runner().Run(ctx, "id", "-u", user); err == nil {
		return false, nil
	}
	_, stderr, err := m.runner().Run(ctx, "useradd", "--system", "--no-create-home", "--home-dir", home, "--shell", "/usr/sbin/nologin", user)
	if err != nil {
		return false, fmt.Errorf("create user %s: %s", user, strings.TrimSpace(string(stderr)))
	}
	return true, nil
}

// Chown changes ownership recursively. Arguments are fixed; no shell.
func (m *Manager) Chown(ctx context.Context, path, user string) error {
	if !platform.ValidSafeName(user) {
		return fmt.Errorf("invalid user name %q", user)
	}
	if err := platform.ValidateAbsolutePath(path, false); err != nil {
		return err
	}
	_, stderr, err := m.runner().Run(ctx, "chown", "-R", user+":"+user, path)
	if err != nil {
		return fmt.Errorf("chown %s: %s", path, strings.TrimSpace(string(stderr)))
	}
	return nil
}

func parseProperties(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

func parseSystemdBytes(v string) int64 {
	if v == "" || v == "[not set]" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
