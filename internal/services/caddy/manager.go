package caddy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

// Manager performs all Caddyfile changes through validated, backed-up,
// reversible updates. It never regenerates the Caddyfile from scratch.
type Manager struct {
	Runner    platform.Runner
	Config    func() settings.Config
	BackupDir func() string
	Log       *slog.Logger
}

// Status describes Caddy's local service and configuration state.
type Status struct {
	Available       bool   `json:"available"`
	Active          bool   `json:"active"`
	Version         string `json:"version,omitempty"`
	ConfigValid     bool   `json:"configValid"`
	ImportInstalled bool   `json:"importInstalled"`
	Caddyfile       string `json:"caddyfile"`
	SnippetDir      string `json:"snippetDir"`
	Error           string `json:"error,omitempty"`
	Service         string `json:"service"`
}

// SnippetInfo is a managed snippet file.
type SnippetInfo struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	Domains    []string `json:"domains"`
	Upstreams  []string `json:"upstreams"`
	Managed    bool     `json:"managed"`
	ModifiedAt string   `json:"modifiedAt"`
}

// ConfigView is the full read-only Caddy inventory.
type ConfigView struct {
	Status   Status        `json:"status"`
	Sites    []Site        `json:"sites"`
	Imports  []string      `json:"imports"`
	Snippets []SnippetInfo `json:"snippets"`
}

type stagedChange struct {
	add       map[string]string
	remove    []string
	ensureImp bool
}

// Status inspects Caddy without changing anything.
func (m *Manager) Status(ctx context.Context) Status {
	cfg := m.Config()
	st := Status{Caddyfile: cfg.CaddyfilePath, SnippetDir: cfg.CaddySnippetDir, Service: cfg.CaddyService}
	version, _, err := m.runner().Run(ctx, cfg.CaddyBinary, "version")
	if err != nil {
		st.Error = "caddy binary: " + err.Error()
		return st
	}
	st.Available = true
	st.Version = strings.TrimSpace(string(version))
	active, _, err := m.runner().Run(ctx, cfg.SystemctlBinary, "is-active", cfg.CaddyService)
	st.Active = err == nil && strings.TrimSpace(string(active)) == "active"
	content, err := platform.ReadFileLimited(cfg.CaddyfilePath, 2<<20)
	if err != nil {
		st.Error = "read Caddyfile: " + err.Error()
		st.ConfigValid = false
	} else {
		st.ImportInstalled = HasImport(string(content), cfg.CaddySnippetDir)
		_, _, verr := m.runner().Run(ctx, cfg.CaddyBinary, "validate", "--config", cfg.CaddyfilePath, "--adapter", "caddyfile")
		st.ConfigValid = verr == nil
		if verr != nil {
			st.Error = "validation: " + verr.Error()
		}
	}
	if !st.Active && st.Error == "" {
		st.Error = "Caddy service is not active"
	}
	return st
}

// ReadConfig parses sites, imports, and managed snippets.
func (m *Manager) ReadConfig(ctx context.Context) (ConfigView, error) {
	cfg := m.Config()
	view := ConfigView{Sites: []Site{}, Imports: []string{}, Snippets: []SnippetInfo{}}
	view.Status = m.Status(ctx)
	content, err := platform.ReadFileLimited(cfg.CaddyfilePath, 4<<20)
	if err != nil {
		return view, err
	}
	parsed := Parse(string(content))
	view.Sites = parsed.Sites
	view.Imports = parsed.Imports
	entries, err := os.ReadDir(cfg.CaddySnippetDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return view, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".caddy") {
			continue
		}
		path := filepath.Join(cfg.CaddySnippetDir, e.Name())
		raw, err := platform.ReadFileLimited(path, 256<<10)
		if err != nil {
			continue
		}
		p := Parse(string(raw))
		info := SnippetInfo{Name: e.Name(), Path: path, Managed: strings.Contains(string(raw), "managed by pbmanager")}
		if fi, err := e.Info(); err == nil {
			info.ModifiedAt = fi.ModTime().UTC().Format(time.RFC3339)
		}
		for _, s := range p.Sites {
			info.Domains = append(info.Domains, s.Addresses...)
			info.Upstreams = append(info.Upstreams, s.Upstreams...)
		}
		view.Snippets = append(view.Snippets, info)
	}
	return view, nil
}

// Validate validates the live configuration.
func (m *Manager) Validate(ctx context.Context) error {
	cfg := m.Config()
	_, _, err := m.runner().Run(ctx, cfg.CaddyBinary, "validate", "--config", cfg.CaddyfilePath, "--adapter", "caddyfile")
	return err
}

// EnsureImport adds the managed import block if absent, validates, backs up,
// and reloads.
func (m *Manager) EnsureImport(ctx context.Context) (bool, error) {
	return m.apply(ctx, stagedChange{ensureImp: true})
}

// ApplyRoute writes or updates a managed route and reloads Caddy atomically.
func (m *Manager) ApplyRoute(ctx context.Context, instance *store.Instance, domain string) (string, error) {
	if !ValidDomain(domain) {
		return "", fmt.Errorf("invalid domain %q", domain)
	}
	cfg := m.Config()
	name := snippetFilename(domain)
	_, err := m.apply(ctx, stagedChange{add: map[string]string{name: Snippet(instance.ID, instance.Name, domain, instance.Port)}, ensureImp: true})
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg.CaddySnippetDir, name), nil
}

// RemoveRoute deletes a managed snippet. It refuses to remove files that lack
// the pbmanager ownership marker.
func (m *Manager) RemoveRoute(ctx context.Context, domain string) error {
	cfg := m.Config()
	path := filepath.Join(cfg.CaddySnippetDir, snippetFilename(domain))
	raw, err := platform.ReadFileLimited(path, 256<<10)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !strings.Contains(string(raw), "managed by pbmanager") {
		return fmt.Errorf("refusing to remove %s: not a pbmanager-managed snippet", path)
	}
	_, err = m.apply(ctx, stagedChange{remove: []string{snippetFilename(domain)}})
	return err
}

func (m *Manager) apply(ctx context.Context, change stagedChange) (bool, error) {
	cfg := m.Config()
	if err := platform.ValidateAbsolutePath(cfg.CaddyfilePath, false); err != nil {
		return false, err
	}
	live, err := platform.ReadFileLimited(cfg.CaddyfilePath, 4<<20)
	if err != nil {
		return false, fmt.Errorf("read Caddyfile: %w", err)
	}
	liveContent := string(live)
	if err := platform.EnsureDir(cfg.CaddySnippetDir, 0o755); err != nil {
		return false, err
	}
	snippets, err := readSnippets(cfg.CaddySnippetDir)
	if err != nil {
		return false, err
	}
	changedFile := false
	for name, content := range change.add {
		if err := validateSnippetName(name); err != nil {
			return false, err
		}
		if existing, ok := snippets[name]; ok && existing != content {
			if !strings.Contains(existing, "managed by pbmanager") {
				return false, fmt.Errorf("refusing to overwrite unmanaged snippet %s", name)
			}
		}
		if snippets[name] != content {
			snippets[name] = content
			changedFile = true
		}
	}
	for _, name := range change.remove {
		content, ok := snippets[name]
		if !ok {
			continue
		}
		if !strings.Contains(content, "managed by pbmanager") {
			return false, fmt.Errorf("refusing to remove unmanaged snippet %s", name)
		}
		delete(snippets, name)
		changedFile = true
	}
	candidate := liveContent
	addedImport := false
	if change.ensureImp {
		candidate, addedImport = AddImport(candidate, cfg.CaddySnippetDir)
	}
	if !changedFile && !addedImport {
		return false, nil
	}
	if !HasImport(candidate, cfg.CaddySnippetDir) && len(snippets) > 0 {
		return false, errors.New("Caddyfile does not import the managed snippet directory")
	}

	// Stage snippets in a temporary directory and validate a candidate
	// Caddyfile in the real directory so relative imports keep working.
	tmpDir, err := os.MkdirTemp("/tmp", "pbmanager-caddy-*")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(tmpDir)
	for name, content := range snippets {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0o600); err != nil {
			return false, err
		}
	}
	validateContent := candidate
	if HasImport(validateContent, cfg.CaddySnippetDir) {
		validateContent = ReplaceImportDir(validateContent, cfg.CaddySnippetDir, tmpDir)
	} else {
		validateContent, _ = AddImport(validateContent, tmpDir)
	}
	tmpFile, err := os.CreateTemp(filepath.Dir(cfg.CaddyfilePath), ".Caddyfile.pbmanager-validate-*")
	if err != nil {
		return false, err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)
	if _, err := tmpFile.WriteString(validateContent); err != nil {
		tmpFile.Close()
		return false, err
	}
	if err := tmpFile.Chmod(0o600); err != nil {
		tmpFile.Close()
		return false, err
	}
	if err := tmpFile.Close(); err != nil {
		return false, err
	}
	if _, _, err := m.runner().Run(ctx, cfg.CaddyBinary, "validate", "--config", tmpPath, "--adapter", "caddyfile"); err != nil {
		return false, fmt.Errorf("Caddyfile validation failed (no changes applied): %w", err)
	}

	backupPath, err := m.backup()
	if err != nil {
		return false, fmt.Errorf("backup configuration: %w", err)
	}
	rollbackWrite := func(cause error) error {
		restoreErr := m.restore(backupPath, cfg)
		if restoreErr == nil {
			_ = m.Reload(ctx)
		}
		msg := fmt.Sprintf("Caddy write failed; previous configuration restored (%s): %v", backupPath, cause)
		if restoreErr != nil {
			msg += "; restore error: " + restoreErr.Error()
		}
		return errors.New(msg)
	}
	// Apply snippet additions/removals.
	for name, content := range change.add {
		if err := platform.AtomicWrite(filepath.Join(cfg.CaddySnippetDir, name), []byte(content), 0o644); err != nil {
			return false, rollbackWrite(err)
		}
	}
	for _, name := range change.remove {
		if err := os.Remove(filepath.Join(cfg.CaddySnippetDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, rollbackWrite(err)
		}
	}
	if candidate != liveContent {
		if err := platform.AtomicWrite(cfg.CaddyfilePath, []byte(candidate), 0o644); err != nil {
			return false, rollbackWrite(err)
		}
	}
	if err := m.Reload(ctx); err != nil {
		restoreErr := m.restore(backupPath, cfg)
		if restoreErr == nil {
			_ = m.Reload(ctx)
		}
		msg := fmt.Sprintf("Caddy reload failed; previous configuration restored (%s): %v", backupPath, err)
		if restoreErr != nil {
			msg += "; restore error: " + restoreErr.Error()
		}
		return false, errors.New(msg)
	}
	m.log().Info("caddy configuration applied", "backup", backupPath, "changed_snippets", len(change.add)+len(change.remove), "import_added", addedImport)
	return addedImport, nil
}

func (m *Manager) backup() (string, error) {
	cfg := m.Config()
	root := m.BackupDir()
	if root == "" {
		root = cfg.BackupDir
	}
	dir := filepath.Join(root, "caddy", time.Now().UTC().Format("20060102T150405.000000000Z"))
	if err := platform.EnsureDir(dir, 0o700); err != nil {
		return "", err
	}
	if err := platform.CopyFile(cfg.CaddyfilePath, filepath.Join(dir, "Caddyfile")); err != nil {
		return "", err
	}
	if err := platform.EnsureDir(filepath.Join(dir, "snippets"), 0o700); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(cfg.CaddySnippetDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		_ = platform.CopyFile(filepath.Join(cfg.CaddySnippetDir, e.Name()), filepath.Join(dir, "snippets", e.Name()))
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.txt"), []byte("Caddyfile and managed snippets backup\n"), 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

func (m *Manager) restore(backupPath string, cfg settings.Config) error {
	if err := platform.CopyFile(filepath.Join(backupPath, "Caddyfile"), cfg.CaddyfilePath); err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(backupPath, "snippets"))
	if err != nil {
		return err
	}
	// Remove snippets that did not exist in the backup snapshot only when they
	// are managed by pbmanager.
	current, _ := os.ReadDir(cfg.CaddySnippetDir)
	backupNames := map[string]bool{}
	for _, e := range entries {
		backupNames[e.Name()] = true
	}
	for _, e := range current {
		if e.IsDir() || backupNames[e.Name()] {
			continue
		}
		path := filepath.Join(cfg.CaddySnippetDir, e.Name())
		if raw, err := os.ReadFile(path); err == nil && strings.Contains(string(raw), "managed by pbmanager") {
			_ = os.Remove(path)
		}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := platform.CopyFile(filepath.Join(backupPath, "snippets", e.Name()), filepath.Join(cfg.CaddySnippetDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// Reload gracefully reloads Caddy, preferring systemd.
func (m *Manager) Reload(ctx context.Context) error {
	cfg := m.Config()
	_, _, err := m.runner().Run(ctx, cfg.SystemctlBinary, "reload", cfg.CaddyService)
	if err == nil {
		return nil
	}
	sysErr := err.Error()
	_, _, fallbackErr := m.runner().Run(ctx, cfg.CaddyBinary, "reload", "--config", cfg.CaddyfilePath, "--adapter", "caddyfile")
	if fallbackErr == nil {
		return nil
	}
	return fmt.Errorf("systemctl reload failed: %s; caddy reload failed: %v", sysErr, fallbackErr)
}

// Logs returns recent Caddy journal lines.
func (m *Manager) Logs(ctx context.Context, lines int) (string, error) {
	cfg := m.Config()
	if lines <= 0 || lines > 2000 {
		lines = 200
	}
	out, stderr, err := m.runner().Run(ctx, cfg.JournalctlBinary, "-u", cfg.CaddyService, "-n", fmt.Sprint(lines), "--no-pager", "--output=short-iso")
	if err != nil {
		if len(stderr) > 0 {
			return "", fmt.Errorf("journalctl: %s", strings.TrimSpace(string(stderr)))
		}
		return "", err
	}
	return string(out), nil
}

func readSnippets(dir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".caddy") {
			continue
		}
		if err := validateSnippetName(e.Name()); err != nil {
			return nil, err
		}
		raw, err := platform.ReadFileLimited(filepath.Join(dir, e.Name()), 512<<10)
		if err != nil {
			return nil, err
		}
		out[e.Name()] = string(raw)
	}
	return out, nil
}

func snippetFilename(domain string) string {
	return strings.ReplaceAll(strings.ToLower(domain), "*", "_wildcard") + ".caddy"
}

func validateSnippetName(name string) error {
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) || !strings.HasSuffix(name, ".caddy") {
		return fmt.Errorf("invalid snippet name %q", name)
	}
	return nil
}

func (m *Manager) runner() platform.Runner {
	if m.Runner != nil {
		return m.Runner
	}
	return platform.ExecRunner{}
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}
