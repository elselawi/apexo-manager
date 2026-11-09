package systemd

import (
	"strings"
	"testing"
)

func TestRenderUnit(t *testing.T) {
	raw, err := RenderUnit(UnitSpec{
		InstanceID: "inst-1", Name: "clinic-a", Binary: "/opt/clinic-a/pocketbase",
		BaseDir: "/opt/clinic-a", DataDir: "/opt/clinic-a/pb_data", Port: 8090, User: "pb-clinic-a", Version: "0.23.6",
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{
		"User=pb-clinic-a", "WorkingDirectory=/opt/clinic-a",
		"ExecStart=/opt/clinic-a/pocketbase serve --http=127.0.0.1:8090 --dir=/opt/clinic-a/pb_data",
		"ProtectSystem=strict", "ReadWritePaths=/opt/clinic-a", UnitMarker("inst-1"),
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("unit missing %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "ProtectHome=true") {
		t.Fatal("ProtectHome should be enabled for /opt")
	}
}

func TestRenderUnitRejectsUnsafeValues(t *testing.T) {
	base := UnitSpec{InstanceID: "i", Name: "ok", Binary: "/opt/a/pocketbase", BaseDir: "/opt/a", DataDir: "/opt/a/pb_data", Port: 8090}
	bad := base
	bad.Name = "../evil"
	if _, err := RenderUnit(bad); err == nil {
		t.Fatal("unsafe name accepted")
	}
	bad = base
	bad.Port = 0
	if _, err := RenderUnit(bad); err == nil {
		t.Fatal("invalid port accepted")
	}
	bad = base
	bad.Binary = ""
	if _, err := RenderUnit(bad); err == nil {
		t.Fatal("empty binary accepted")
	}
	bad = base
	bad.Binary = "/opt/a/%n"
	bad.BaseDir = "/opt/a/%n"
	bad.DataDir = "/opt/a/%n/pb_data"
	if _, err := RenderUnit(bad); err == nil {
		t.Fatal("systemd specifier accepted")
	}
}

func TestUnitNameAndSafeName(t *testing.T) {
	if UnitName("pb") != "pb.service" || UnitName("pb.service") != "pb.service" {
		t.Fatal("unit name normalization failed")
	}
	if !ValidServiceName("clinic-a") || ValidServiceName("../evil") {
		t.Fatal("service validation failed")
	}
}
