package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apexo/pbmanager/internal/platform"
)

type recordingRunner struct {
	name string
	args []string
}

func (r *recordingRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	r.name, r.args = name, append([]string{}, args...)
	return []byte("started"), nil, nil
}

func TestScheduleInstallBuildsDetachedUnit(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "pbmanager_1.0.2_linux_amd64")
	if err := os.WriteFile(source, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	err := ScheduleInstall(context.Background(), runner, source, strings.Repeat("a", 64), "1.0.2", "/etc/pbmanager/config.json", "/usr/local/bin/pbmanager", "pbmanager", 7*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if runner.name != "systemd-run" {
		t.Fatalf("runner = %s", runner.name)
	}
	joined := strings.Join(runner.args, " ")
	for _, want := range []string{"--no-block", "self-upgrade", "--from " + source, "--sha256 " + strings.Repeat("a", 64), "--version 1.0.2", "--target /usr/local/bin/pbmanager", "--service pbmanager", "--config /etc/pbmanager/config.json"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
}

func TestScheduleRejectsUnsafeSource(t *testing.T) {
	if err := ScheduleInstall(context.Background(), &recordingRunner{}, "/tmp/does-not-exist", strings.Repeat("a", 64), "1.0.2", "/etc/pbmanager/config.json", "/usr/local/bin/pbmanager", "pbmanager", 5*time.Second); err == nil {
		t.Fatal("missing source accepted")
	}
}

var _ platform.Runner = (*recordingRunner)(nil)
