// Package pbauth owns PocketBase application credentials. Linked credentials
// are reused when present; otherwise one-shot operations can create a
// short-lived superuser, use it, and remove it with a durable cleanup journal.
package pbauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/secret"
	"github.com/apexo/pbmanager/internal/services/pocketbase"
	"github.com/apexo/pbmanager/internal/services/systemd"
	"github.com/apexo/pbmanager/internal/store"
)

// Manager coordinates linked and ephemeral PocketBase application credentials.
type Manager struct {
	Store   *store.Store
	Secrets *secret.Box
	Systemd *systemd.Manager
	Runner  platform.Runner
	DataDir string
	Log     *slog.Logger
}

type journal struct {
	InstanceID  string `json:"instanceId"`
	Email       string `json:"email"`
	DataDir     string `json:"dataDir"`
	BinaryPath  string `json:"binaryPath"`
	RunAsUser   string `json:"runAsUser,omitempty"`
	ServiceName string `json:"serviceName"`
	WasRunning  bool   `json:"wasRunning"`
	Purpose     string `json:"purpose,omitempty"`
	Phase       string `json:"phase"`
	CreatedAt   string `json:"createdAt"`
}

// Linked reports whether a durable superuser is stored for the instance.
func (m *Manager) Linked(inst *store.Instance) bool {
	return inst != nil && inst.PBSuperuserEmail != "" && inst.PBSuperuserSecret != "" && m.Secrets != nil
}

// AuthLinked authenticates with the stored durable superuser.
func (m *Manager) AuthLinked(ctx context.Context, inst *store.Instance) (*pocketbase.Client, string, error) {
	if !m.Linked(inst) {
		return nil, "", errors.New("PocketBase management credentials are not linked")
	}
	password, err := m.Secrets.Open("pb_superuser:"+inst.ID, inst.PBSuperuserSecret)
	if err != nil {
		return nil, "", errors.New("stored PocketBase credential could not be decrypted")
	}
	client := pocketbase.New(inst.Port)
	token, err := client.TestAuth(ctx, inst.PBSuperuserEmail, password)
	if err != nil {
		_ = m.Store.SetPBAuthState(ctx, inst.ID, inst.PBSuperuserEmail, inst.PBSuperuserSecret, "error", store.Now(), truncate(err.Error(), 500))
		if errors.Is(err, pocketbase.ErrUnauthorized) {
			return nil, "", errors.New("PocketBase credentials are invalid or expired; update or clear them in instance settings")
		}
		return nil, "", err
	}
	_ = m.Store.SetPBAuthState(ctx, inst.ID, inst.PBSuperuserEmail, inst.PBSuperuserSecret, "linked", store.Now(), "")
	return client, token, nil
}

// With authenticates once and calls fn. Linked credentials are reused. With no
// linked credential, a random superuser is created for the duration of fn and
// removed afterward. fn must return only after any asynchronous PocketBase
// work it started has completed.
func (m *Manager) With(ctx context.Context, inst *store.Instance, purpose string, fn func(*pocketbase.Client, string) error) error {
	if m.Linked(inst) {
		client, token, err := m.AuthLinked(ctx, inst)
		if err != nil {
			return err
		}
		return fn(client, token)
	}
	return m.withEphemeral(ctx, inst, purpose, fn)
}

func (m *Manager) withEphemeral(ctx context.Context, inst *store.Instance, purpose string, fn func(*pocketbase.Client, string) error) (err error) {
	if inst.BinaryPath == "" || inst.DataDir == "" || inst.ServiceName == "" {
		return errors.New("instance has incomplete binary, data, or service metadata")
	}
	runner := m.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	status, err := m.Systemd.Status(ctx, inst.ServiceName)
	if err != nil {
		return err
	}
	if !status.Exists {
		return fmt.Errorf("systemd unit %s does not exist", inst.ServiceName)
	}
	wasRunning := status.Running
	email, password, err := ephemeralIdentity()
	if err != nil {
		return err
	}
	j := journal{
		InstanceID: inst.ID, Email: email, DataDir: inst.DataDir, BinaryPath: inst.BinaryPath,
		RunAsUser: inst.RunAsUser, ServiceName: inst.ServiceName, WasRunning: wasRunning,
		Purpose: purpose, Phase: "creating", CreatedAt: store.Now(),
	}
	if err := m.writeJournal(j); err != nil {
		return err
	}
	cleanupNeeded := true
	defer func() {
		if cleanupNeeded && err != nil {
			// The normal path performs cleanup explicitly so it can wait for the
			// service. This is a best-effort guard for early failures.
			_ = m.cleanup(context.Background(), inst, j, wasRunning, runner)
		}
	}()

	if err := m.stopAndWait(ctx, inst, status.Running); err != nil {
		return err
	}
	if _, stderr, runErr := m.runSuperuser(ctx, runner, j, "upsert", password); runErr != nil {
		_ = m.restoreRunning(context.Background(), j, wasRunning)
		return fmt.Errorf("create ephemeral PocketBase superuser: %s", commandError(runErr, stderr))
	}
	j.Phase = "created"
	if err := m.writeJournal(j); err != nil {
		_ = m.cleanup(context.Background(), inst, j, wasRunning, runner)
		return err
	}
	m.audit(ctx, inst.ID, purpose, "pb.ephemeral_superuser_created", email)
	if err := m.startAndWait(ctx, inst); err != nil {
		_ = m.cleanup(context.Background(), inst, j, wasRunning, runner)
		return err
	}
	client := pocketbase.New(inst.Port)
	token, err := client.TestAuth(ctx, email, password)
	if err != nil {
		_ = m.cleanup(context.Background(), inst, j, wasRunning, runner)
		return fmt.Errorf("authenticate ephemeral PocketBase superuser: %w", err)
	}
	opErr := fn(client, token)
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cleanupErr := m.cleanup(cleanupCtx, inst, j, wasRunning, runner)
	cleanupNeeded = false
	if opErr != nil {
		if cleanupErr != nil {
			return fmt.Errorf("%s; ephemeral superuser cleanup also failed: %v", opErr, cleanupErr)
		}
		return opErr
	}
	if cleanupErr != nil {
		return fmt.Errorf("operation succeeded but ephemeral superuser cleanup failed: %w", cleanupErr)
	}
	return nil
}

func (m *Manager) cleanup(ctx context.Context, inst *store.Instance, j journal, wasRunning bool, runner platform.Runner) error {
	if err := m.stopAndWait(ctx, inst, false); err != nil {
		return fmt.Errorf("stop service for ephemeral cleanup: %w", err)
	}
	if _, stderr, err := m.runSuperuser(ctx, runner, j, "delete", ""); err != nil {
		// Restore the prior running state before reporting the cleanup failure.
		_ = m.restoreRunning(ctx, j, wasRunning)
		return fmt.Errorf("delete ephemeral superuser: %s", commandError(err, stderr))
	}
	m.audit(ctx, j.InstanceID, j.Purpose, "pb.ephemeral_superuser_deleted", j.Email)
	if err := m.restoreRunning(ctx, j, wasRunning); err != nil {
		return fmt.Errorf("restore service after ephemeral cleanup: %w", err)
	}
	if err := m.removeJournal(j); err != nil {
		return err
	}
	return nil
}

func (m *Manager) stopAndWait(ctx context.Context, inst *store.Instance, knownRunning bool) error {
	status, err := m.Systemd.Status(ctx, inst.ServiceName)
	if err != nil {
		return err
	}
	if !status.Exists {
		return fmt.Errorf("systemd unit %s does not exist", inst.ServiceName)
	}
	if !status.Running && !knownRunning {
		return nil
	}
	if status.Running {
		if err := m.Systemd.Stop(ctx, inst.ServiceName); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := m.Systemd.Status(ctx, inst.ServiceName)
		if err == nil && !st.Running {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return errors.New("service did not stop")
}

func (m *Manager) startAndWait(ctx context.Context, inst *store.Instance) error {
	if err := m.Systemd.Start(ctx, inst.ServiceName); err != nil {
		return err
	}
	if err := pocketbase.New(inst.Port).WaitHealthy(ctx, 90*time.Second); err != nil {
		return err
	}
	return nil
}

func (m *Manager) restoreRunning(ctx context.Context, j journal, wasRunning bool) error {
	if !wasRunning {
		return nil
	}
	inst := &store.Instance{ServiceName: j.ServiceName, Port: 0}
	// The journal carries the service; health needs the port. Look it up if
	// possible, otherwise rely on systemd state.
	if m.Store != nil && j.InstanceID != "" {
		if got, err := m.Store.GetInstance(ctx, j.InstanceID); err == nil && got.Port > 0 {
			inst = got
		}
	}
	if err := m.Systemd.Start(ctx, j.ServiceName); err != nil {
		return err
	}
	if inst.Port > 0 {
		return pocketbase.New(inst.Port).WaitHealthy(ctx, 90*time.Second)
	}
	return nil
}

func (m *Manager) runSuperuser(ctx context.Context, runner platform.Runner, j journal, action, password string) ([]byte, []byte, error) {
	name := j.BinaryPath
	var args []string
	if j.RunAsUser != "" {
		name = "runuser"
		args = append(args, "-u", j.RunAsUser, "--")
	}
	args = append(args, j.BinaryPath, "superuser", action, j.Email)
	if action == "upsert" {
		args = append(args, password)
	}
	args = append(args, "--dir", j.DataDir)
	return runner.Run(ctx, name, args...)
}

func (m *Manager) journalDir() string {
	if m.DataDir == "" {
		return ""
	}
	return filepath.Join(m.DataDir, "ephemeral")
}

func (m *Manager) writeJournal(j journal) error {
	dir := m.journalDir()
	if dir == "" {
		return errors.New("ephemeral journal directory is not configured")
	}
	if err := platform.EnsureDir(dir, 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(j, "", "  ")
	return platform.AtomicWrite(filepath.Join(dir, j.InstanceID+".json"), raw, 0o600)
}

func (m *Manager) removeJournal(j journal) error {
	path := filepath.Join(m.journalDir(), j.InstanceID+".json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Recover removes ephemeral superusers left behind by a manager crash.
func (m *Manager) Recover(ctx context.Context) error {
	dir := m.journalDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	runner := m.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	var errs []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		var j journal
		if err := json.Unmarshal(raw, &j); err != nil {
			errs = append(errs, "invalid ephemeral journal "+entry.Name())
			continue
		}
		if j.ServiceName == "" || j.BinaryPath == "" || j.DataDir == "" || j.Email == "" {
			errs = append(errs, "incomplete ephemeral journal "+entry.Name())
			continue
		}
		if m.Log != nil {
			m.Log.Warn("recovering ephemeral PocketBase superuser", "instance", j.InstanceID, "email", j.Email)
		}
		inst := &store.Instance{ID: j.InstanceID, ServiceName: j.ServiceName, Port: 0}
		if m.Store != nil {
			if got, err := m.Store.GetInstance(ctx, j.InstanceID); err == nil {
				inst = got
			}
		}
		_ = m.stopAndWait(ctx, inst, true)
		if _, stderr, err := m.runSuperuser(ctx, runner, j, "delete", ""); err != nil {
			if !strings.Contains(strings.ToLower(string(stderr)), "not found") {
				errs = append(errs, fmt.Sprintf("%s: %s", j.InstanceID, commandError(err, stderr)))
				_ = m.restoreRunning(ctx, j, j.WasRunning)
				continue
			}
		}
		_ = m.restoreRunning(ctx, j, j.WasRunning)
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (m *Manager) audit(ctx context.Context, instanceID, purpose, action, email string) {
	if m.Store == nil {
		return
	}
	detail, _ := json.Marshal(map[string]string{"purpose": purpose, "email": email, "mode": "ephemeral"})
	_ = m.Store.CreateAudit(ctx, &store.AuditEntry{
		ActorType: "system", ActorName: "pbmanager", Action: action,
		TargetType: "instance", TargetID: instanceID, Detail: string(detail),
	})
}

func ephemeralIdentity() (string, string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	id := hex.EncodeToString(raw)
	return "ephemeral-" + id[:20] + "@pbmanager.local", id[20:] + id[:20], nil
}

func commandError(err error, stderr []byte) string {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = err.Error()
	}
	return truncate(msg, 500)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
