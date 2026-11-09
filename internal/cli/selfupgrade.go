package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/config"
	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/services/pocketbase"
	"github.com/apexo/pbmanager/internal/services/systemd"
)

type selfUpgradeResult struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	Previous   string `json:"previous,omitempty"`
	Error      string `json:"error,omitempty"`
	FinishedAt string `json:"finishedAt"`
}

// cmdSelfUpgrade is the detached, root-owned half of a one-click manager
// update. It verifies the staged binary, installs it atomically, restarts the
// service, and rolls back if the new build does not become healthy.
func cmdSelfUpgrade(args []string) int {
	fs := flag.NewFlagSet("self-upgrade", flag.ContinueOnError)
	from := fs.String("from", "", "staged binary")
	sum := fs.String("sha256", "", "expected SHA-256")
	version := fs.String("version", "", "expected manager version")
	target := fs.String("target", "/usr/local/bin/pbmanager", "installed binary")
	service := fs.String("service", "pbmanager", "systemd service")
	delay := fs.Int("delay", 5, "seconds to wait before replacing the binary")
	if err := fs.Parse(parseArgs(fs, args)); err != nil {
		return 2
	}
	if err := requireRoot(); err != nil {
		return fail(err)
	}
	if *from == "" || *sum == "" || *version == "" {
		return fail(errors.New("--from, --sha256, and --version are required"))
	}
	if *delay < 0 || *delay > 120 {
		return fail(errors.New("--delay must be between 0 and 120 seconds"))
	}
	if !systemd.ValidServiceName(*service) {
		return fail(errors.New("invalid service name"))
	}
	if err := platform.ValidateAbsolutePath(*from, false); err != nil {
		return fail(err)
	}
	if err := platform.ValidateAbsolutePath(*target, false); err != nil {
		return fail(err)
	}
	if len(*sum) != 64 {
		return fail(errors.New("invalid SHA-256"))
	}
	actual, err := fileSHA256(*from)
	if err != nil {
		return fail(err)
	}
	if !strings.EqualFold(actual, *sum) {
		return fail(fmt.Errorf("staged binary checksum mismatch: got %s, want %s", actual, *sum))
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return fail(fmt.Errorf("load bootstrap config: %w", err))
	}
	updatesDir := filepath.Join(cfg.DataDir, "updates")
	if err := platform.EnsureDir(updatesDir, 0o700); err != nil {
		return fail(err)
	}
	resultPath := filepath.Join(updatesDir, "last-result.json")
	writeResult := func(res selfUpgradeResult) {
		res.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		raw, _ := json.MarshalIndent(res, "", "  ")
		_ = platform.AtomicWrite(resultPath, raw, 0o600)
	}
	if *delay > 0 {
		fmt.Fprintf(os.Stderr, "Applying pbmanager %s in %d seconds\n", *version, *delay)
		time.Sleep(time.Duration(*delay) * time.Second)
	}
	runner := platform.ExecRunner{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, stderr, err := runner.Run(ctx, *from, "--version")
	if err != nil {
		writeResult(selfUpgradeResult{Status: "failed", Version: *version, Error: "staged binary cannot execute: " + strings.TrimSpace(string(stderr)+" "+err.Error())})
		return fail(err)
	}
	gotVersion := strings.TrimSpace(string(out))
	if !strings.Contains(gotVersion, strings.TrimPrefix(*version, "v")) {
		writeResult(selfUpgradeResult{Status: "failed", Version: *version, Error: "staged binary reported " + gotVersion})
		return fail(fmt.Errorf("staged binary reported %q, expected %s", gotVersion, *version))
	}
	previous, err := pocketbase.InstallBinary(*from, *target)
	if err != nil {
		writeResult(selfUpgradeResult{Status: "failed", Version: *version, Error: err.Error()})
		return fail(fmt.Errorf("install binary: %w", err))
	}
	if _, stderr, err := runner.Run(ctx, "systemctl", "restart", *service); err != nil {
		_ = restorePreviousBinary(runner, ctx, *target, previous, *service)
		writeResult(selfUpgradeResult{Status: "failed", Version: *version, Previous: previous, Error: "restart failed: " + strings.TrimSpace(string(stderr)+" "+err.Error())})
		return fail(fmt.Errorf("restart %s: %w", *service, err))
	}
	if err := waitManagerHealthy(ctx, cfg.ListenAddr, 45*time.Second); err != nil {
		rollbackErr := restorePreviousBinary(runner, ctx, *target, previous, *service)
		msg := "new version failed health check: " + err.Error()
		if rollbackErr != nil {
			msg += "; rollback failed: " + rollbackErr.Error()
		}
		writeResult(selfUpgradeResult{Status: "rolled_back", Version: *version, Previous: previous, Error: msg})
		return fail(errors.New(msg))
	}
	_ = os.Remove(*from)
	writeResult(selfUpgradeResult{Status: "success", Version: *version, Previous: previous})
	fmt.Printf("pbmanager updated to %s; previous binary retained at %s\n", *version, previous)
	return 0
}

func restorePreviousBinary(runner platform.Runner, ctx context.Context, target, previous, service string) error {
	if previous == "" {
		return errors.New("no previous binary is available")
	}
	if _, err := os.Stat(previous); err != nil {
		return err
	}
	_, stderr, err := runner.Run(ctx, "systemctl", "stop", service)
	if err != nil {
		return fmt.Errorf("stop service: %s", strings.TrimSpace(string(stderr)+" "+err.Error()))
	}
	if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(previous, target); err != nil {
		return err
	}
	if err := os.Chmod(target, 0o755); err != nil {
		return err
	}
	if _, stderr, err := runner.Run(ctx, "systemctl", "start", service); err != nil {
		return fmt.Errorf("restart previous version: %s", strings.TrimSpace(string(stderr)+" "+err.Error()))
	}
	return nil
}

func waitManagerHealthy(ctx context.Context, listen string, timeout time.Duration) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return err
	}
	if host == "0.0.0.0" || host == "::" || host == "" {
		host = "127.0.0.1"
	}
	url := "http://" + net.JoinHostPort(host, port) + "/healthz"
	client := &http.Client{Timeout: 4 * time.Second}
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			last = fmt.Errorf("health returned HTTP %d", resp.StatusCode)
		} else {
			last = err
		}
		time.Sleep(2 * time.Second)
	}
	if last == nil {
		last = errors.New("timed out")
	}
	return last
}

func fileSHA256(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("staged binary is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, 256<<20)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
