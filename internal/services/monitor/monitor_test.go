package monitor

import (
	"testing"

	"github.com/apexo/pbmanager/internal/settings"
)

func TestServerInfoDiskValues(t *testing.T) {
	info := ServerInfoFrom(settings.Config{}, t.TempDir())
	if info.DiskTotal <= 0 {
		t.Fatalf("disk total: %d", info.DiskTotal)
	}
	if info.DiskFree < 0 || info.DiskFree > info.DiskTotal {
		t.Fatalf("disk free %d outside [0,%d]", info.DiskFree, info.DiskTotal)
	}
	if info.MemTotal <= 0 || info.CPUs <= 0 {
		t.Fatalf("host info incomplete: %#v", info)
	}
}
