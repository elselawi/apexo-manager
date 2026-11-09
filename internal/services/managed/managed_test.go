package managed

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/apexo/pbmanager/internal/services/systemd"
)

type fakeRunner struct {
	mu      sync.Mutex
	running bool
	calls   []string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	switch {
	case name == "systemctl" && len(args) >= 2 && args[0] == "show":
		state, sub, pid := "inactive", "dead", "0"
		if f.running {
			state, sub, pid = "active", "running", "42"
		}
		return []byte(fmt.Sprintf("LoadState=loaded\nActiveState=%s\nSubState=%s\nMainPID=%s\nMemoryCurrent=0\nCPUUsageNSec=0\nExecMainStartTimestamp=\nFragmentPath=/etc/systemd/system/test.service\nUser=test\n", state, sub, pid)), nil, nil
	case name == "systemctl" && len(args) >= 2 && args[0] == "stop":
		f.running = false
	case name == "systemctl" && len(args) >= 2 && args[0] == "start":
		f.running = true
	}
	return nil, nil, nil
}

func TestRecoverDeletesOrphanAndRemovesJournal(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{running: true}
	m := &Manager{
		Systemd: &systemd.Manager{Runner: runner},
		Runner:  runner,
		DataDir: dir,
	}
	j := journal{InstanceID: "inst-1", Email: "pbmanager-test@managed.local", ServiceName: "test", WasRunning: true, Phase: "created"}
	if err := m.writeJournal(j); err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	if !strings.Contains(joined, "superuser delete pbmanager-test@managed.local") {
		t.Fatalf("delete not invoked:\n%s", joined)
	}
	if !strings.Contains(joined, "systemctl start test") {
		t.Fatalf("service state not restored:\n%s", joined)
	}
	entries, err := os.ReadDir(m.journalDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("journal not removed: %v", entries)
	}
}
