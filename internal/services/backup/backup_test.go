package backup

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apexo/pbmanager/internal/secret"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

func makeSQLite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE test(id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO test(value) VALUES('hello')`); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotAndManagerCreate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dataDir := filepath.Join(root, "pb_data")
	makeSQLite(t, filepath.Join(dataDir, "data.db"))
	if err := os.MkdirAll(filepath.Join(dataDir, "uploads"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "uploads", "avatar.txt"), []byte("avatar"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	inst := InstanceFixture(t, st, dataDir)
	box, err := secret.Open(filepath.Join(root, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(root, "backups")
	mgr := &Manager{Store: st, Secrets: box, Cfg: func() settings.Config {
		return settings.Config{BackupDir: backupDir}
	}, Log: slog.New(slog.NewTextHandler(testWriter{t}, nil))}
	b, err := mgr.Create(ctx, inst, false, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if b.Method != MethodSQLite || b.Status != "verified" || b.SHA256 == "" {
		t.Fatalf("backup: %#v", b)
	}
	if !strings.HasSuffix(b.Path, ".tar.gz") {
		t.Fatalf("path = %s", b.Path)
	}
	if err := ValidateTarBackup(ctx, b.Path); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Verify(ctx, b); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetBackup(ctx, b.ID)
	if got.Status != "verified" {
		t.Fatalf("record: %#v", got)
	}
	if removed, err := mgr.Prune(ctx, inst.ID, 1); err != nil || removed != 0 {
		t.Fatalf("prune: %d %v", removed, err)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Log(string(p)); return len(p), nil }

func InstanceFixture(t *testing.T, st *store.Store, dataDir string) *store.Instance {
	t.Helper()
	inst := &store.Instance{
		Name: "clinic-a", Hostname: "clinic-a", BaseDir: filepath.Dir(dataDir), BinaryPath: filepath.Join(filepath.Dir(dataDir), "pocketbase"),
		DataDir: dataDir, Port: 8090, ServiceName: "clinic-a", ServiceFile: "/etc/systemd/system/clinic-a.service",
		Managed: true, BackupRetention: 7,
	}
	if err := st.CreateInstance(context.Background(), inst); err != nil {
		t.Fatal(err)
	}
	return inst
}

func TestSafeExtractRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.tar.gz")
	f, _ := os.Create(archive)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	body := []byte("evil")
	_ = tw.WriteHeader(&tar.Header{Name: "../evil.txt", Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(body)
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
	if err := SafeExtractTarGz(archive, filepath.Join(dir, "out"), ""); err == nil {
		t.Fatal("path traversal accepted")
	}
}

func TestInspectPocketBaseZip(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.db")
	makeSQLite(t, dataPath)
	zipPath := filepath.Join(dir, "backup.zip")
	f, _ := os.Create(zipPath)
	zw := zip.NewWriter(f)
	entry, _ := zw.Create("data.db")
	raw, _ := os.ReadFile(dataPath)
	_, _ = entry.Write(raw)
	_ = zw.Close()
	_ = f.Close()
	info, err := InspectPocketBaseZip(zipPath)
	if err != nil || info.DataDBNext != "data.db" {
		t.Fatalf("inspect: %v %#v", err, info)
	}
	if err := ValidateZipBackup(context.Background(), zipPath); err != nil {
		t.Fatal(err)
	}
}

func TestPackageNativeBundle(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dataDir := filepath.Join(root, "pb_data")
	makeSQLite(t, filepath.Join(dataDir, "data.db"))
	_ = os.MkdirAll(filepath.Join(dataDir, "uploads"), 0o750)
	_ = os.WriteFile(filepath.Join(dataDir, "uploads", "file.txt"), []byte("x"), 0o600)
	st, _ := store.Open(ctx, filepath.Join(root, "manager.db"))
	defer st.Close()
	inst := InstanceFixture(t, st, dataDir)
	// Build an official-style zip containing data.db.
	zipPath := filepath.Join(root, "native.zip")
	f, _ := os.Create(zipPath)
	zw := zip.NewWriter(f)
	entry, _ := zw.Create("data.db")
	raw, _ := os.ReadFile(filepath.Join(dataDir, "data.db"))
	_, _ = entry.Write(raw)
	_ = zw.Close()
	_ = f.Close()
	mgr := &Manager{Store: st, Cfg: func() settings.Config { return settings.Config{BackupDir: filepath.Join(root, "backups")} }}
	dest := filepath.Join(root, "bundle.tar.gz")
	if err := mgr.packageNativeBundle(ctx, inst, zipPath, dest); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTarBackup(ctx, dest); err != nil {
		t.Fatal(err)
	}
	extract := filepath.Join(root, "extract")
	if err := SafeExtractTarGz(dest, extract, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(extract, "data", "uploads", "file.txt")); err != nil {
		t.Fatalf("uploads missing from native bundle: %v", err)
	}
}
