package discovery

import (
	"testing"
)

func TestParseUnit(t *testing.T) {
	content := `[Unit]
Description=Clinic A
[Service]
User=pb-clinic-a
WorkingDirectory=/opt/clinic-a
ExecStart=/opt/clinic-a/pocketbase serve --http=127.0.0.1:8090 --dir=/opt/clinic-a/pb_data
`
	found := parseUnit("clinic-a.service", "/etc/systemd/system/clinic-a.service", content)
	if found == nil {
		t.Fatal("unit not recognized")
	}
	if found.BinaryPath != "/opt/clinic-a/pocketbase" || found.DataDir != "/opt/clinic-a/pb_data" || found.Port != 8090 {
		t.Fatalf("parsed: %#v", found)
	}
	if found.ServiceName != "clinic-a" || found.RunAsUser != "pb-clinic-a" {
		t.Fatalf("metadata: %#v", found)
	}
	if parseUnit("other.service", "/x", "[Service]\nExecStart=/usr/bin/nginx\n") != nil {
		t.Fatal("non-PocketBase unit accepted")
	}
}

func TestParsePBArgs(t *testing.T) {
	dir, port := parsePBArgs([]string{"serve", "--http", "127.0.0.1:8090", "--dir", "/srv/pb_data"})
	if dir != "/srv/pb_data" || port != 8090 {
		t.Fatalf("args: %q %d", dir, port)
	}
	dir, port = parsePBArgs([]string{"serve", "--http=localhost:8091", "--dir=/opt/x"})
	if dir != "/opt/x" || port != 8091 {
		t.Fatalf("equals args: %q %d", dir, port)
	}
}

func TestSanitizeNames(t *testing.T) {
	if got := sanitizeName("Clinic A / #1"); got != "clinic-a-1" {
		t.Fatalf("sanitize = %q", got)
	}
	seen := map[string]bool{}
	if uniqueName("a", seen) != "a" || uniqueName("a", seen) != "a-2" {
		t.Fatal("unique naming failed")
	}
}
