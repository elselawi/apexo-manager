package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAbsolutePath(t *testing.T) {
	if err := ValidateAbsolutePath("/opt/clinic-a", false); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "relative/path", "/", "/opt/../etc", "/opt/a\nb"} {
		if err := ValidateAbsolutePath(bad, false); err == nil {
			t.Fatalf("accepted invalid path %q", bad)
		}
	}
	if err := ValidateAbsolutePath("/", true); err != nil {
		t.Fatalf("root should be allowed explicitly: %v", err)
	}
}

func TestIsWithin(t *testing.T) {
	if !IsWithin("/opt/a", "/opt/a/pb_data") || !IsWithin("/opt/a", "/opt/a") {
		t.Fatal("expected containment")
	}
	if IsWithin("/opt/a", "/opt/ab") || IsWithin("/opt/a", "/etc/passwd") {
		t.Fatal("false containment")
	}
}

func TestEnsureDirMakesParentsTraversable(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "a", "b", "data")
	if err := EnsureDir(target, 0o750); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o750 {
		t.Fatalf("target mode: %v %o", err, info.Mode().Perm())
	}
	parent, _ := os.Stat(filepath.Join(base, "a"))
	if parent.Mode().Perm() != 0o755 {
		t.Fatalf("parent mode = %o", parent.Mode().Perm())
	}
}

func TestAtomicWriteAndCopy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "file.txt")
	if err := AtomicWrite(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "hello" {
		t.Fatalf("read: %q %v", raw, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	dest := filepath.Join(dir, "copy.txt")
	if err := CopyFile(path, dest); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(dest)
	if string(raw) != "hello" {
		t.Fatal("copy mismatch")
	}
}

func TestValidSafeName(t *testing.T) {
	for _, ok := range []string{"clinic-a", "pb_clinic", "a.b"} {
		if !ValidSafeName(ok) {
			t.Fatalf("rejected %q", ok)
		}
	}
	for _, bad := range []string{"", "../evil", "a/b", "a b", "a;b"} {
		if ValidSafeName(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestLimitedRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large")
	_ = os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o600)
	if _, err := ReadFileLimited(path, 50); err == nil {
		t.Fatal("expected size error")
	}
	if raw, err := ReadFileLimited(path, 200); err != nil || len(raw) != 100 {
		t.Fatalf("read: %d %v", len(raw), err)
	}
}
