package backup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/apexo/pbmanager/internal/secret"
	"github.com/apexo/pbmanager/internal/services/systemd"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

type fakeSystemdRunner struct {
	mu      sync.Mutex
	running bool
}

func (f *fakeSystemdRunner) setRunning(v bool) {
	f.mu.Lock()
	f.running = v
	f.mu.Unlock()
}

func (f *fakeSystemdRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case name == "systemctl" && len(args) >= 2 && args[0] == "show":
		state := "inactive"
		sub := "dead"
		pid := "0"
		if f.running {
			state = "active"
			sub = "running"
			pid = "4242"
		}
		return []byte(fmt.Sprintf("LoadState=loaded\nActiveState=%s\nSubState=%s\nMainPID=%s\nMemoryCurrent=1048576\nCPUUsageNSec=0\nExecMainStartTimestamp=\nFragmentPath=/etc/systemd/system/test.service\nUser=test\n", state, sub, pid)), nil, nil
	case name == "systemctl" && len(args) >= 2 && (args[0] == "stop" || args[0] == "start" || args[0] == "restart" || args[0] == "enable" || args[0] == "disable"):
		f.running = args[0] != "stop" && args[0] != "disable"
		return nil, nil, nil
	case name == "chown":
		return nil, nil, nil
	default:
		return nil, nil, nil
	}
}

func TestRestoreRollbackOnUnhealthyService(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dataDir := filepath.Join(root, "pb_data")
	makeSQLite(t, filepath.Join(dataDir, "data.db"))
	_ = os.MkdirAll(filepath.Join(dataDir, "uploads"), 0o750)
	_ = os.WriteFile(filepath.Join(dataDir, "uploads", "before.txt"), []byte("before"), 0o600)

	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"code":200,"message":"healthy"}`)
	}))
	defer health.Close()
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(health.URL, "http://"))
	port := 0
	for _, r := range portStr {
		port = port*10 + int(r-'0')
	}

	st, err := store.Open(ctx, filepath.Join(root, "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	inst := InstanceFixture(t, st, dataDir)
	inst.Port = port
	inst.ServiceName = "test"
	inst.RunAsUser = "test"
	if err := st.UpdateInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}
	box, _ := secret.Open(filepath.Join(root, "secrets"))
	mgr := &Manager{
		Store: st, Secrets: box, Systemd: &systemd.Manager{Runner: &fakeSystemdRunner{running: true}},
		Cfg: func() settings.Config { return settings.Config{BackupDir: filepath.Join(root, "backups")} },
		Log: slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	backup, err := mgr.Create(ctx, inst, false, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dataDir, "uploads")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "marker-after"), []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if backup.Status != "verified" {
		t.Fatalf("backup status %q", backup.Status)
	}
	if err := os.RemoveAll(filepath.Join(dataDir, "uploads")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "marker-after"), []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Restore(ctx, inst, backup, inst.Name, nil); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "uploads", "before.txt")); err != nil {
		t.Fatalf("uploads not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "marker-after")); !os.IsNotExist(err) {
		t.Fatal("post-backup data leaked into restored directory")
	}
	oldDirs, _ := filepath.Glob(dataDir + ".pbmanager-pre-restore-*")
	if len(oldDirs) == 0 {
		t.Fatal("previous data directory was not retained")
	}
}

func TestRestoreRejectsWrongConfirmation(t *testing.T) {
	ctx := context.Background()
	mgr := &Manager{}
	if err := mgr.Restore(ctx, &store.Instance{Name: "clinic-a"}, &store.Backup{}, "wrong", nil); err == nil {
		t.Fatal("wrong confirmation accepted")
	}
}
