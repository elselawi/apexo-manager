package pbadmin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/apexo/pbmanager/internal/services/systemd"
	"github.com/apexo/pbmanager/internal/store"
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

func TestRecoverRestartsServiceFromJournal(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{running: false}
	m := &Manager{
		Systemd: &systemd.Manager{Runner: runner},
		Runner:  runner,
		DataDir: dir,
	}
	path, err := m.writeServiceJournal(&store.Instance{ID: "inst-1", ServiceName: "test"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	if !strings.Contains(joined, "systemctl start test") {
		t.Fatalf("service was not restarted:\n%s", joined)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("journal still exists: %v", err)
	}
}
