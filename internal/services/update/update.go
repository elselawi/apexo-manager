// Package update discovers and stages official pbmanager releases from GitHub.
package update

import (
	"context"
	"crypto/sha256"
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
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/platform"
)

const (
	// DefaultRepo points at the upstream project. Forks can override it when
	// constructing the client.
	DefaultRepo = "elselawi/apexo-manager"
	// DefaultAPIBase is GitHub's public REST endpoint.
	DefaultAPIBase = "https://api.github.com"
)

var safeVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)

// Client talks to the GitHub releases API for one repository.
type Client struct {
	HTTP    *http.Client
	APIBase string
	Repo    string
	GOOS    string
	GOARCH  string
}

// New returns a production client with conservative timeouts.
func New() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		APIBase: DefaultAPIBase,
		Repo:    DefaultRepo,
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
	}
}

// Info describes the latest available release relative to the running build.
type Info struct {
	Current     string `json:"current"`
	Latest      string `json:"latest,omitempty"`
	Tag         string `json:"tag,omitempty"`
	Name        string `json:"name,omitempty"`
	Notes       string `json:"notes,omitempty"`
	ReleaseURL  string `json:"releaseUrl,omitempty"`
	PublishedAt string `json:"publishedAt,omitempty"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason,omitempty"`
	AssetName   string `json:"assetName,omitempty"`
	AssetSize   int64  `json:"assetSize,omitempty"`
	DownloadURL string `json:"downloadUrl,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	CheckedAt   string `json:"checkedAt"`
}

type release struct {
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	HTMLURL     string  `json:"html_url"`
	PublishedAt string  `json:"published_at"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	Assets      []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

func (c *Client) base() string {
	if c.APIBase == "" {
		return DefaultAPIBase
	}
	return strings.TrimRight(c.APIBase, "/")
}

func (c *Client) repo() string {
	if c.Repo == "" {
		return DefaultRepo
	}
	return c.Repo
}

func (c *Client) architecture() string {
	switch c.GOARCH {
	case "", "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	default:
		return c.GOARCH
	}
}

// Check fetches the latest non-draft, non-prerelease release. A repository with
// no releases returns a non-error result explaining that state.
func (c *Client) Check(ctx context.Context, current string) (Info, error) {
	info := Info{Current: normalizeVersion(current), CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	endpoint := fmt.Sprintf("%s/repos/%s/releases/latest", c.base(), c.repo())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "pbmanager/"+info.Current)
	resp, err := c.http().Do(req)
	if err != nil {
		return info, fmt.Errorf("GitHub release check failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		info.Reason = "No published GitHub release was found for " + c.repo() + "."
		return info, nil
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return info, fmt.Errorf("GitHub release check returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return info, fmt.Errorf("parse GitHub release: %w", err)
	}
	if rel.Draft || rel.Prerelease {
		info.Reason = "The latest GitHub release is a draft or prerelease."
		return info, nil
	}
	info.Tag = rel.TagName
	info.Latest = normalizeVersion(rel.TagName)
	info.Name = strings.TrimSpace(rel.Name)
	info.Notes = truncate(rel.Body, 12000)
	info.ReleaseURL = rel.HTMLURL
	info.PublishedAt = rel.PublishedAt
	if info.Latest == "" {
		info.Reason = "The latest release has no usable version tag."
		return info, nil
	}
	if !isNewer(info.Latest, info.Current) {
		info.Reason = "You are running the latest release."
		return info, nil
	}
	wantAsset := fmt.Sprintf("pbmanager_%s_%s_%s", info.Latest, c.GOOS, c.architecture())
	var chosen *asset
	var checksums *asset
	for i := range rel.Assets {
		a := &rel.Assets[i]
		if a.Name == wantAsset {
			chosen = a
		}
		if a.Name == "checksums.txt" {
			checksums = a
		}
	}
	if chosen == nil {
		info.Reason = fmt.Sprintf("No release asset %q was found.", wantAsset)
		return info, nil
	}
	sum, err := c.checksum(ctx, chosen, checksums, wantAsset)
	if err != nil {
		return info, err
	}
	info.Available = true
	info.AssetName = chosen.Name
	info.AssetSize = chosen.Size
	info.DownloadURL = chosen.BrowserDownloadURL
	info.SHA256 = sum
	return info, nil
}

func (c *Client) checksum(ctx context.Context, chosen *asset, checksums *asset, name string) (string, error) {
	if strings.HasPrefix(chosen.Digest, "sha256:") {
		sum := strings.ToLower(strings.TrimPrefix(chosen.Digest, "sha256:"))
		if len(sum) == 64 {
			return sum, nil
		}
	}
	if checksums == nil || checksums.BrowserDownloadURL == "" {
		return "", errors.New("release has no SHA-256 digest and no checksums.txt asset")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksums.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return "", fmt.Errorf("download release checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download release checksums: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[1], "*") == name {
			sum := strings.ToLower(fields[0])
			if len(sum) == 64 {
				return sum, nil
			}
		}
	}
	return "", fmt.Errorf("checksums.txt does not contain %s", name)
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// Download verifies and stores the release asset in dir. It returns the local
// path and SHA-256 digest.
func (c *Client) Download(ctx context.Context, info Info, dir string) (string, string, error) {
	if !info.Available || info.DownloadURL == "" || info.SHA256 == "" {
		return "", "", errors.New("no verified update is available")
	}
	u, err := url.Parse(info.DownloadURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", "", errors.New("release asset URL is invalid")
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return "", "", errors.New("release assets must use HTTPS")
	}
	if err := platform.EnsureDir(dir, 0o700); err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.DownloadURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "pbmanager")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", "", fmt.Errorf("download release asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download release asset: HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, 128<<20))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", "", fmt.Errorf("download release asset: %w", err)
	}
	if n == 0 {
		return "", "", errors.New("release asset is empty")
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(sum, info.SHA256) {
		return "", "", fmt.Errorf("release asset checksum mismatch: got %s, want %s", sum, info.SHA256)
	}
	target := filepath.Join(dir, info.AssetName)
	if err := os.Rename(tmpPath, target); err != nil {
		return "", "", err
	}
	if err := os.Chmod(target, 0o700); err != nil {
		return "", "", err
	}
	return target, sum, nil
}

// ScheduleInstall starts a detached systemd unit that installs source, restarts
// the service, and rolls back if the new version fails to become healthy.
func ScheduleInstall(ctx context.Context, runner platform.Runner, source, sum, version, configPath, target, service string, delay time.Duration) error {
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	if !safeVersion.MatchString(version) {
		return errors.New("invalid update version")
	}
	for _, p := range []string{source, configPath, target} {
		if err := platform.ValidateAbsolutePath(p, false); err != nil {
			return err
		}
	}
	if info, err := os.Lstat(source); err != nil || !info.Mode().IsRegular() {
		return errors.New("downloaded update is not a regular file")
	}
	if delay < 2*time.Second {
		delay = 5 * time.Second
	}
	unit := "pbmanager-self-update-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	args := []string{
		"--unit=" + unit,
		"--collect",
		"--quiet",
		"--no-block",
		"--description=Install pbmanager " + version,
		"--property=Type=oneshot",
		"--property=WorkingDirectory=/",
		"--property=StandardOutput=journal",
		"--property=StandardError=journal",
		"--",
		source, "self-upgrade",
		"--from", source,
		"--sha256", sum,
		"--version", version,
		"--target", target,
		"--service", service,
		"--config", configPath,
		"--delay", strconv.Itoa(int(delay.Seconds())),
	}
	stdout, stderr, err := runner.Run(ctx, "systemd-run", args...)
	if err != nil {
		msg := strings.TrimSpace(string(stderr) + " " + string(stdout))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("could not start the detached updater: %s", truncate(msg, 500))
	}
	return nil
}

// CompareVersions returns -1, 0, or 1.
func CompareVersions(a, b string) int {
	pa, prea := parseVersion(a)
	pb, preb := parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	if prea == preb {
		return 0
	}
	if prea == "" {
		return 1
	}
	if preb == "" {
		return -1
	}
	return strings.Compare(prea, preb)
}

func isNewer(latest, current string) bool {
	if current == "" || current == "dev" {
		return latest != ""
	}
	return CompareVersions(latest, current) > 0
}

func parseVersion(v string) ([3]int, string) {
	v = normalizeVersion(v)
	var out [3]int
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	parts := strings.Split(v, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out, pre
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	return v
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func isLoopback(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
