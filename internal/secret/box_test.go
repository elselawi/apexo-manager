package secret

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealOpenAndPurposeBinding(t *testing.T) {
	box, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Seal("smtp:profile-1", "super-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(enc, "super-secret") {
		t.Fatal("plaintext leaked into ciphertext")
	}
	plain, err := box.Open("smtp:profile-1", enc)
	if err != nil || plain != "super-secret" {
		t.Fatalf("open: %q %v", plain, err)
	}
	if _, err := box.Open("smtp:profile-2", enc); err == nil {
		t.Fatal("purpose binding not enforced")
	}
	if _, err := box.Open("smtp:profile-1", enc[:len(enc)-2]+"AA"); err == nil {
		t.Fatal("tampering not detected")
	}
	empty, err := box.Seal("x", "")
	if err != nil || empty != "" {
		t.Fatalf("empty seal: %q %v", empty, err)
	}
}

func TestKeyPermissions(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "secret.key")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %o", info.Mode().Perm())
	}
	if err := CheckKeyPermissions(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckKeyPermissions(dir); err == nil {
		t.Fatal("expected broad permissions error")
	}
}

func TestSymlinkKeyRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.key")
	if err := os.WriteFile(target, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "secret.key")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("symlink key accepted")
	}
}
