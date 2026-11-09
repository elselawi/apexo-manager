package pocketbase

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/settings"
)

// DownloadResult describes a verified PocketBase release archive.
type DownloadResult struct {
	ArchivePath  string `json:"archivePath"`
	Filename     string `json:"filename"`
	SHA256       string `json:"sha256"`
	Version      string `json:"version"`
	Arch         string `json:"arch"`
	Size         int64  `json:"size"`
	ChecksumFrom string `json:"checksumSource"`
}

// DownloadVerified downloads a release archive and verifies it against the
// release checksums or the GitHub API asset digest.
func DownloadVerified(ctx context.Context, cfg settings.Config, version, arch, explicitChecksum string) (res DownloadResult, retErr error) {
	if !regexp.MustCompile(`^[0-9A-Za-z.+-]{1,32}$`).MatchString(version) {
		return DownloadResult{}, errors.New("invalid PocketBase version")
	}
	if arch != "amd64" && arch != "arm64" && arch != "armv7" {
		return DownloadResult{}, errors.New("unsupported architecture")
	}
	filename := settings.ExpandVersion(cfg.ArchiveTemplate, version, arch)
	if filename == "" || strings.ContainsAny(filename, `/\`) {
		return DownloadResult{}, errors.New("invalid archive filename template")
	}
	downloadURL := settings.ExpandVersion(cfg.DownloadURLTemplate, version, arch)
	u, err := url.Parse(downloadURL)
	if err != nil || u.Host == "" {
		return DownloadResult{}, errors.New("invalid download URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return DownloadResult{}, errors.New("refusing non-HTTPS download")
	}
	expected := strings.ToLower(strings.TrimSpace(explicitChecksum))
	source := "provided"
	if expected == "" {
		expected, source, err = releaseChecksum(ctx, cfg, version, arch, filename)
		if err != nil {
			if cfg.RequireChecksums {
				return DownloadResult{}, fmt.Errorf("could not obtain a trusted checksum: %w", err)
			}
			source = "unverified (checksum requirement disabled)"
		}
	}
	client := &http.Client{
		Timeout: 10 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && !(req.URL.Scheme == "http" && isLoopbackHost(req.URL.Hostname())) {
				return errors.New("redirect to insecure URL refused")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return DownloadResult{}, err
	}
	req.Header.Set("User-Agent", "pbmanager-provisioner/1")
	resp, err := client.Do(req)
	if err != nil {
		return DownloadResult{}, fmt.Errorf("download PocketBase: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DownloadResult{}, fmt.Errorf("download PocketBase returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > 512<<20 {
		return DownloadResult{}, errors.New("release archive is unexpectedly large")
	}
	tmp, err := os.CreateTemp("", "pbmanager-release-*.zip")
	if err != nil {
		return DownloadResult{}, err
	}
	tmpName := tmp.Name()
	defer func() {
		if retErr != nil {
			os.Remove(tmpName)
		}
	}()
	h := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, 512<<20+1))
	if copyErr != nil {
		tmp.Close()
		return DownloadResult{}, copyErr
	}
	if written > 512<<20 {
		tmp.Close()
		return DownloadResult{}, errors.New("release archive exceeded size limit")
	}
	if err := tmp.Close(); err != nil {
		return DownloadResult{}, err
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if expected != "" && subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
		return DownloadResult{}, fmt.Errorf("release checksum mismatch: expected %s, got %s", expected, actual)
	}
	return DownloadResult{ArchivePath: tmpName, Filename: filename, SHA256: actual, Version: version, Arch: arch, Size: written, ChecksumFrom: source}, nil
}

func releaseChecksum(ctx context.Context, cfg settings.Config, version, arch, filename string) (string, string, error) {
	var errs []string
	if sum := pinnedChecksum(version, arch, filename); sum != "" {
		return sum, "pinned release metadata", nil
	}
	if cfg.ChecksumURLTemplate != "" {
		checksumURL := settings.ExpandVersion(cfg.ChecksumURLTemplate, version, arch)
		raw, status, err := fetchLimited(ctx, checksumURL, 2<<20)
		if err == nil && status == http.StatusOK {
			if sum := parseChecksums(string(raw), filename); sum != "" {
				return sum, "release checksums.txt", nil
			}
			errs = append(errs, "checksums file did not list "+filename)
		} else if err != nil {
			errs = append(errs, err.Error())
		} else {
			errs = append(errs, fmt.Sprintf("checksums URL returned HTTP %d", status))
		}
	}
	if sum, err := githubAssetDigest(ctx, cfg, version, filename); err == nil && sum != "" {
		return sum, "GitHub release asset digest", nil
	} else if err != nil {
		errs = append(errs, err.Error())
	}
	return "", "", errors.New(strings.Join(errs, "; "))
}

func fetchLimited(ctx context.Context, target string, max int64) ([]byte, int, error) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return nil, 0, errors.New("invalid URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil, 0, errors.New("refusing insecure checksum URL")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "pbmanager-provisioner/1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if int64(len(raw)) > max {
		return nil, resp.StatusCode, errors.New("response too large")
	}
	return raw, resp.StatusCode, nil
}

var checksumLineRE = regexp.MustCompile(`(?i)([a-f0-9]{64})\s+\*?(.+)$`)

func parseChecksums(text, filename string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if m := checksumLineRE.FindStringSubmatch(line); len(m) == 3 {
			if filepath.Base(strings.TrimSpace(m[2])) == filename {
				return strings.ToLower(m[1])
			}
		}
		// BSD style: SHA256 (file) = hash
		if strings.HasPrefix(strings.ToUpper(line), "SHA256") && strings.Contains(line, filename) {
			parts := strings.Split(line, "=")
			if len(parts) == 2 {
				sum := strings.ToLower(strings.TrimSpace(parts[1]))
				if len(sum) == 64 {
					return sum
				}
			}
		}
	}
	return ""
}

func githubAssetDigest(ctx context.Context, cfg settings.Config, version, filename string) (string, error) {
	downloadURL := settings.ExpandVersion(cfg.DownloadURLTemplate, version, "amd64")
	u, err := url.Parse(downloadURL)
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		return "", errors.New("not a GitHub release source")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// owner/repo/releases/download/tag/file
	if len(parts) < 6 || parts[2] != "releases" || parts[3] != "download" {
		return "", errors.New("unrecognized GitHub release URL")
	}
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/tags/%s", parts[0], parts[1], url.PathEscape(parts[4]))
	raw, status, err := fetchLimited(ctx, apiURL, 8<<20)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned HTTP %d", status)
	}
	var release struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(raw, &release); err != nil {
		return "", err
	}
	for _, a := range release.Assets {
		if a.Name == filename && strings.HasPrefix(a.Digest, "sha256:") {
			return strings.TrimPrefix(a.Digest, "sha256:"), nil
		}
	}
	return "", errors.New("GitHub asset has no sha256 digest (older release)")
}

// ExtractBinary extracts the single PocketBase executable to destDir and
// returns its path. Archives containing traversal or extra executables are
// rejected.
func ExtractBinary(archivePath, destDir string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open release archive: %w", err)
	}
	defer zr.Close()
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", err
	}
	var binaryEntry *zip.File
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasPrefix(name, "/") || strings.Contains(name, "../") {
			return "", fmt.Errorf("unsafe release archive entry %q", f.Name)
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("release archive contains symlink %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(name) == "pocketbase" {
			if binaryEntry != nil {
				return "", errors.New("release archive contains multiple PocketBase binaries")
			}
			binaryEntry = f
		}
	}
	if binaryEntry == nil {
		return "", errors.New("release archive does not contain a pocketbase executable")
	}
	if binaryEntry.UncompressedSize64 > 512<<20 {
		return "", errors.New("PocketBase binary is too large")
	}
	rc, err := binaryEntry.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	dest := filepath.Join(destDir, "pocketbase")
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, io.LimitReader(rc, 512<<20)); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Chmod(0o755); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return dest, nil
}

// InstallBinary atomically replaces target with source, retaining one previous
// binary at target+".previous".
func InstallBinary(source, target string) (string, error) {
	if err := platform.ValidateAbsolutePath(target, false); err != nil {
		return "", err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("source binary is not a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	previous := ""
	if _, err := os.Lstat(target); err == nil {
		previous = target + ".previous"
		_ = os.Remove(previous)
		if err := os.Rename(target, previous); err != nil {
			return "", err
		}
	}
	if err := platform.CopyFile(source, target); err != nil {
		if previous != "" {
			_ = os.Rename(previous, target)
		}
		return "", err
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return "", err
	}
	return previous, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Legacy PocketBase releases predate checksums.txt. These SHA-256 digests were
// computed from the official GitHub release assets over TLS and are pinned so
// that older installations can still be provisioned with verified downloads.
var pinnedReleaseChecksums = map[string]string{
	"0.23.6/linux/amd64": "0cf234cfffa3a6851d1b6606b649ce8b2102dad2e8dbf6eb94d77a773b1dea94",
	"0.23.6/linux/arm64": "bef140a1a96994029153dca8c00b1750b9a5a764fb9db2dc68d7bb40e8a29e8a",
	"0.23.6/linux/armv7": "aff660cfe3ab421e9c1191573e5c2859e83f955c55d52e3ebb2fceeb919155c7",
}

func pinnedChecksum(version, arch, filename string) string {
	if !strings.Contains(filename, version) {
		return ""
	}
	return pinnedReleaseChecksums[version+"/linux/"+arch]
}
