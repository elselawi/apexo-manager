package pocketbase

import "testing"

func TestPinnedLegacyChecksums(t *testing.T) {
	if sum := pinnedChecksum("0.23.6", "amd64", "pocketbase_0.23.6_linux_amd64.zip"); sum != "0cf234cfffa3a6851d1b6606b649ce8b2102dad2e8dbf6eb94d77a773b1dea94" {
		t.Fatalf("pinned amd64 = %q", sum)
	}
	if sum := pinnedChecksum("0.23.6", "arm64", "pocketbase_0.23.6_linux_arm64.zip"); sum == "" {
		t.Fatal("missing arm64 checksum")
	}
	if sum := pinnedChecksum("0.40.6", "amd64", "pocketbase_0.40.6_linux_amd64.zip"); sum != "" {
		t.Fatalf("unexpected pinned checksum for newer release: %q", sum)
	}
}
