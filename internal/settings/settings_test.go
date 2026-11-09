package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/apexo/pbmanager/internal/store"
)

func newSettings(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc, err := New(context.Background(), st, Bootstrap{DataDir: dir, DBPath: filepath.Join(dir, "manager.db")})
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, dir
}

func TestDefaultsPersistAndValidate(t *testing.T) {
	svc, _, dir := newSettings(t)
	cfg := svc.Get()
	if cfg.DefaultPBVersion == "" || cfg.InstallRoot != "/opt" {
		t.Fatalf("defaults: %#v", cfg)
	}
	cfg.InstallRoot = filepath.Join(dir, "install")
	cfg.ServiceDir = dir
	cfg.CaddySnippetDir = filepath.Join(dir, "caddy", "snippets")
	cfg.CaddyfilePath = filepath.Join(dir, "caddy", "Caddyfile")
	cfg.BackupDir = filepath.Join(dir, "backups")
	cfg.DiscoveryRoots = []string{dir}
	view, changed, err := svc.Update(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) == 0 || view.Config.InstallRoot != cfg.InstallRoot {
		t.Fatalf("update: %#v changed=%v", view, changed)
	}
}

func TestValidationRejectsUnsafeTemplates(t *testing.T) {
	svc, _, dir := newSettings(t)
	base := svc.Get()
	base.ServiceDir = dir
	cases := []Config{base, base, base, base}
	cases[0].DownloadURLTemplate = "http://example.com/pb-{version}-{arch}.zip"
	cases[1].ArchiveTemplate = "../pocketbase_{version}_{arch}.zip"
	cases[2].PBDataDirTemplate = "/var/lib/pb/data"
	cases[3].PortStart = 9000
	cases[3].PortEnd = 8000
	for i, c := range cases {
		if _, _, err := svc.Update(context.Background(), c); err == nil {
			t.Fatalf("case %d accepted invalid settings", i)
		}
	}
}

func TestChangingDefaultsDoesNotMoveFiles(t *testing.T) {
	svc, _, dir := newSettings(t)
	oldRoot := filepath.Join(dir, "old-install")
	if err := os.MkdirAll(oldRoot, 0o750); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(oldRoot, "data.db")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := svc.Get()
	cfg.ServiceDir = dir
	cfg.InstallRoot = filepath.Join(dir, "new-install")
	if _, _, err := svc.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("existing data was moved or removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new-install")); !os.IsNotExist(err) {
		t.Fatalf("default path was created as a side effect: %v", err)
	}
}

func TestExpandTemplates(t *testing.T) {
	got := ExpandVersion("pocketbase_{version}_linux_{arch}.zip", "0.23.6", "amd64")
	if got != "pocketbase_0.23.6_linux_amd64.zip" {
		t.Fatalf("expanded = %q", got)
	}
	if got := ExpandDataDir("{base_dir}/pb_data", "/opt/clinic"); got != "/opt/clinic/pb_data" {
		t.Fatalf("data = %q", got)
	}
}
