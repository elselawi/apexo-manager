package caddy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

// realValidateRunner uses the actual caddy binary for validation and treats
// reloads as successful, allowing the staging path to be tested without a
// running Caddy service.
type realValidateRunner struct {
	bin string
}

func (r realValidateRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	if name == r.bin && len(args) > 0 && args[0] == "validate" {
		return (platform.ExecRunner{}).Run(ctx, name, args...)
	}
	if len(args) > 1 && args[0] == "version" {
		return (platform.ExecRunner{}).Run(ctx, name, args...)
	}
	return []byte("ok"), nil, nil
}

func TestRealCaddyValidation(t *testing.T) {
	bin := os.Getenv("PBMANAGER_CADDY_BIN")
	if bin == "" {
		if found, err := exec.LookPath("caddy"); err == nil {
			bin = found
		}
	}
	if bin == "" {
		t.Skip("caddy binary not available")
	}
	dir := t.TempDir()
	caddyfile := filepath.Join(dir, "Caddyfile")
	snippets := filepath.Join(dir, "pbmanager.d")
	if err := os.MkdirAll(snippets, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caddyfile, []byte("{\n\tadmin off\n}\n\n:8099 {\n\trespond \"ok\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := settings.Config{
		CaddyfilePath: caddyfile, CaddySnippetDir: snippets, CaddyService: "caddy",
		CaddyBinary: bin, SystemctlBinary: "systemctl", BackupDir: filepath.Join(dir, "backups"),
	}
	m := &Manager{Runner: realValidateRunner{bin: bin}, Config: func() settings.Config { return cfg }, BackupDir: func() string { return cfg.BackupDir }}
	inst := &store.Instance{ID: "inst-1", Name: "clinic-a", Port: 8090}
	if _, err := m.ApplyRoute(context.Background(), inst, "clinic-a.localhost"); err != nil {
		t.Fatalf("real Caddy validation failed: %v", err)
	}
	content, _ := os.ReadFile(caddyfile)
	if !strings.Contains(string(content), "import") {
		t.Fatalf("import missing:\n%s", content)
	}
}
