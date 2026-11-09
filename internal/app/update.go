package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apexo/pbmanager/internal/config"
	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/services/update"
)

// managerUpdate downloads the latest GitHub release, verifies it, and starts a
// detached updater. The detached unit survives the service restart it performs.
func (a *App) managerUpdate(ctx context.Context, ex *jobs.Execution) (any, error) {
	current := a.Version
	if current == "" {
		current = "dev"
	}
	// A scheduled update deliberately restarts the service. When the new
	// process recovers this job, detect that it is already running the target
	// release instead of scheduling a second installer.
	pre, preErr := a.Update.Check(ctx, current)
	if preErr == nil && pre.Latest != "" && update.CompareVersions(pre.Latest, current) <= 0 {
		return map[string]any{"version": pre.Latest, "status": "already_current", "note": "The running version is already current."}, nil
	}
	var info update.Info
	if err := ex.Step(ctx, "check", "Check the latest GitHub release", func(ctx context.Context) (string, error) {
		got, err := a.Update.Check(ctx, current)
		if err != nil {
			return "", jobs.Retryable(err)
		}
		if !got.Available {
			if got.Reason == "" {
				got.Reason = "No update is available."
			}
			return "", errors.New(got.Reason)
		}
		info = got
		return fmt.Sprintf("Latest release %s (%s)", got.Latest, got.AssetName), nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 35)
	var staged, stagedSHA, stagedVersion string
	if err := ex.Step(ctx, "download", "Download and verify the release asset", func(ctx context.Context) (string, error) {
		if !info.Available {
			got, err := a.Update.Check(ctx, a.Version)
			if err != nil {
				return "", jobs.Retryable(err)
			}
			if !got.Available {
				return "", errors.New(got.Reason)
			}
			info = got
		}
		path, sum, err := a.Update.Download(ctx, info, filepath.Join(a.Config.DataDir, "updates"))
		if err != nil {
			return "", jobs.Retryable(err)
		}
		staged, stagedSHA, stagedVersion = path, sum, info.Latest
		raw, _ := json.Marshal(map[string]string{"path": path, "sha256": sum, "version": info.Latest})
		return string(raw), nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 75)
	if err := ex.Step(ctx, "schedule", "Start the detached installer and service restart", func(ctx context.Context) (string, error) {
		if staged == "" {
			var checkpoint struct {
				Path    string `json:"path"`
				SHA256  string `json:"sha256"`
				Version string `json:"version"`
			}
			if err := json.Unmarshal([]byte(ex.StepOutput(ctx, "download")), &checkpoint); err != nil {
				return "", errors.New("download checkpoint is missing or invalid")
			}
			staged, stagedSHA, stagedVersion = checkpoint.Path, checkpoint.SHA256, checkpoint.Version
		}
		if staged == "" || stagedSHA == "" || stagedVersion == "" {
			return "", errors.New("staged update details are incomplete")
		}
		target, err := os.Executable()
		if err != nil {
			return "", err
		}
		if resolved, err := filepath.EvalSymlinks(target); err == nil {
			target = resolved
		}
		if err := platform.ValidateAbsolutePath(target, false); err != nil {
			return "", err
		}
		configPath := a.Config.ConfigPath
		if configPath == "" {
			configPath = config.DefaultPath
		}
		if err := update.ScheduleInstall(ctx, platform.ExecRunner{}, staged, stagedSHA, stagedVersion, configPath, target, "pbmanager", 7*time.Second); err != nil {
			return "", err
		}
		return "Detached updater started; the service will restart after a short delay.", nil
	}); err != nil {
		return nil, err
	}
	ex.Progress(ctx, 100)
	return map[string]any{
		"version": stagedVersion, "sha256": stagedSHA,
		"status": "scheduled", "note": "The manager will restart automatically.",
	}, nil
}
