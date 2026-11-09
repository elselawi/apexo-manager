package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckAndDownload(t *testing.T) {
	payload := []byte("new-pbmanager-binary")
	sum := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v1.0.2", "name": "pbmanager 1.0.2", "body": "notes", "html_url": "https://example.test/release",
				"published_at": "2026-11-09T10:00:00Z", "draft": false, "prerelease": false,
				"assets": []map[string]any{{
					"name": "pbmanager_1.0.2_linux_amd64", "size": len(payload),
					"browser_download_url": "http://" + r.Host + "/asset", "digest": "sha256:" + sumHex,
				}},
			})
		case "/asset":
			_, _ = w.Write(payload)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := &Client{HTTP: srv.Client(), APIBase: srv.URL, Repo: "test/repo", GOOS: "linux", GOARCH: "amd64"}
	info, err := client.Check(context.Background(), "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.Latest != "1.0.2" || info.SHA256 != sumHex || info.AssetName != "pbmanager_1.0.2_linux_amd64" {
		t.Fatalf("info: %#v", info)
	}
	dir := t.TempDir()
	path, gotSum, err := client.Download(context.Background(), info, dir)
	if err != nil {
		t.Fatal(err)
	}
	if gotSum != sumHex {
		t.Fatalf("sum = %s", gotSum)
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != string(payload) {
		t.Fatalf("payload mismatch")
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("path = %s", path)
	}
}

func TestNoUpdateAndNoRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/test/repo/releases/latest" {
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.0.1", "assets": []any{}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	client := &Client{HTTP: srv.Client(), APIBase: srv.URL, Repo: "test/repo", GOOS: "linux", GOARCH: "amd64"}
	info, err := client.Check(context.Background(), "1.0.1")
	if err != nil || info.Available {
		t.Fatalf("expected up-to-date, got %#v %v", info, err)
	}
	if info.Reason == "" {
		t.Fatal("expected a reason")
	}

	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer notFound.Close()
	client.APIBase = notFound.URL
	info, err = client.Check(context.Background(), "1.0.1")
	if err != nil || info.Available || info.Reason == "" {
		t.Fatalf("404 handling: %#v %v", info, err)
	}
}

func TestChecksumsFileFallback(t *testing.T) {
	payload := []byte("binary")
	sum := sha256.Sum256(payload)
	sumHex := hex.EncodeToString(sum[:])
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/repo/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v2.0.0", "assets": []map[string]any{
					{"name": "pbmanager_2.0.0_linux_amd64", "browser_download_url": "http://" + r.Host + "/asset"},
					{"name": "checksums.txt", "browser_download_url": "http://" + r.Host + "/checksums"},
				},
			})
		case "/checksums":
			_, _ = w.Write([]byte(sumHex + "  pbmanager_2.0.0_linux_amd64\n"))
		case "/asset":
			_, _ = w.Write(payload)
		}
	}))
	defer srv.Close()
	client := &Client{HTTP: srv.Client(), APIBase: srv.URL, Repo: "test/repo", GOOS: "linux", GOARCH: "amd64"}
	info, err := client.Check(context.Background(), "1.0.0")
	if err != nil || !info.Available || info.SHA256 != sumHex {
		t.Fatalf("checksum fallback: %#v %v", info, err)
	}
}

func TestVersionComparison(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.2", "1.0.1", 1},
		{"v1.0.1", "1.0.1", 0},
		{"1.0.1", "1.0.2", -1},
		{"1.0.0", "1.0.0-beta", 1},
		{"1.0.0-beta", "1.0.0", -1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Fatalf("CompareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}
