package caddy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

type fakeRunner struct {
	failReload bool
	calls      []string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if len(args) > 0 && args[0] == "reload" {
		if f.failReload {
			return nil, []byte("reload failed"), errors.New("reload failed")
		}
		return nil, nil, nil
	}
	return []byte("ok"), nil, nil
}

func testCaddyManager(t *testing.T) (*Manager, string, string, *fakeRunner) {
	t.Helper()
	dir := t.TempDir()
	caddyfile := filepath.Join(dir, "Caddyfile")
	snippets := filepath.Join(dir, "pbmanager.d")
	if err := os.WriteFile(caddyfile, []byte(":8099 {\n\trespond \"ok\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(snippets, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := settings.Config{
		CaddyfilePath: caddyfile, CaddySnippetDir: snippets, CaddyService: "caddy",
		CaddyBinary: "caddy", SystemctlBinary: "systemctl", BackupDir: filepath.Join(dir, "backups"),
	}
	runner := &fakeRunner{}
	m := &Manager{Runner: runner, Config: func() settings.Config { return cfg }, BackupDir: func() string { return cfg.BackupDir }}
	return m, caddyfile, snippets, runner
}

func TestApplyRouteAndRemove(t *testing.T) {
	m, caddyfile, snippets, _ := testCaddyManager(t)
	inst := &store.Instance{ID: "inst-1", Name: "clinic-a", Port: 8090}
	path, err := m.ApplyRoute(context.Background(), inst, "clinic-a.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "clinic-a.example.com.caddy") {
		t.Fatalf("path = %s", path)
	}
	snippet, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(snippet), "reverse_proxy 127.0.0.1:8090") {
		t.Fatalf("snippet: %v %s", err, snippet)
	}
	content, _ := os.ReadFile(caddyfile)
	if !HasImport(string(content), snippets) {
		t.Fatal("import not added")
	}
	if err := m.RemoveRoute(context.Background(), "clinic-a.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("snippet not removed")
	}
}

func TestRollbackOnReloadFailure(t *testing.T) {
	m, caddyfile, snippets, runner := testCaddyManager(t)
	inst := &store.Instance{ID: "inst-1", Name: "clinic-a", Port: 8090}
	if _, err := m.ApplyRoute(context.Background(), inst, "clinic-a.example.com"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(snippets, "clinic-a.example.com.caddy"))
	runner.failReload = true
	inst.Port = 9090
	if _, err := m.ApplyRoute(context.Background(), inst, "clinic-a.example.com"); err == nil {
		t.Fatal("expected reload failure")
	}
	after, _ := os.ReadFile(filepath.Join(snippets, "clinic-a.example.com.caddy"))
	if string(before) != string(after) {
		t.Fatalf("snippet was not rolled back:\nbefore=%s\nafter=%s", before, after)
	}
	raw, _ := os.ReadFile(caddyfile)
	if !strings.Contains(string(raw), ":8099") {
		t.Fatal("Caddyfile lost unrelated content")
	}
}

func TestRefusesUnmanagedSnippet(t *testing.T) {
	m, _, snippets, _ := testCaddyManager(t)
	unmanaged := filepath.Join(snippets, "clinic-a.example.com.caddy")
	if err := os.WriteFile(unmanaged, []byte("clinic-a.example.com { respond \"custom\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst := &store.Instance{ID: "inst-1", Name: "clinic-a", Port: 8090}
	if _, err := m.ApplyRoute(context.Background(), inst, "clinic-a.example.com"); err == nil {
		t.Fatal("unmanaged snippet was overwritten")
	}
}
