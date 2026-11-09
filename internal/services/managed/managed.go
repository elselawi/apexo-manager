// Package managed owns durable per-instance PocketBase superusers and keeps
// them healthy by deleting and recreating the account on confirmed drift.
package managed

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
	"sync"
	"time"

	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/secret"
	"github.com/apexo/pbmanager/internal/services/pocketbase"
	"github.com/apexo/pbmanager/internal/services/systemd"
	"github.com/apexo/pbmanager/internal/store"
)

const (
	jobCreate = "managed.create"
	jobRepair = "managed.repair"
	jobDelete = "managed.delete"
)

// Manager reconciles manager-owned superusers.
type Manager struct {
	Store   *store.Store
	Secrets *secret.Box
	Systemd *systemd.Manager
	Jobs    *jobs.Engine
	Runner  platform.Runner
	DataDir string
	Log     *slog.Logger

	mu sync.Mutex
}

type journal struct {
	InstanceID  string `json:"instanceId"`
	Email       string `json:"email"`
	ServiceName string `json:"serviceName"`
	WasRunning  bool   `json:"wasRunning"`
	Phase       string `json:"phase"`
	CreatedAt   string `json:"createdAt"`
}

// Payload is used for managed credential jobs.
type Payload struct {
	InstanceID string `json:"instanceId"`
}

// Register installs the persistent job handlers.
func (m *Manager) Register() {
	m.Jobs.Register(jobCreate, m.CreateJob)
	m.Jobs.Register(jobRepair, m.RepairJob)
	m.Jobs.Register(jobDelete, m.DeleteJob)
}

// Watch periodically verifies managed credentials and queues repair jobs.
func (m *Manager) Watch(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	// Give the service and managed accounts time to settle after startup.
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	_ = m.InspectAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = m.InspectAll(ctx)
		}
	}
}

// InspectAll checks all running managed instances and queues repairs.
func (m *Manager) InspectAll(ctx context.Context) error {
	instances, err := m.Store.ListManagedInstances(ctx)
	if err != nil {
		return err
	}
	for i := range instances {
		inst := &instances[i]
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		st, err := m.Systemd.Status(ctx, inst.ServiceName)
		if err != nil || !st.Running {
			continue
		}
		client := pocketbase.New(inst.Port)
		if err := client.Health(ctx); err != nil {
			continue
		}
		session, authErr := m.authenticate(ctx, inst)
		if authErr == nil {
			_ = m.Store.MarkPBAuthVerified(ctx, inst.ID, session.RecordID, store.Now())
			continue
		}
		if !errors.Is(authErr, pocketbase.ErrUnauthorized) {
			continue
		}
		count, err := m.Store.MarkPBAuthFailure(ctx, inst.ID, truncate(authErr.Error(), 500))
		if err != nil {
			m.log().Error("record managed credential failure", "instance", inst.Name, "error", err)
			continue
		}
		if count < 2 || m.hasActiveRepair(ctx, inst.ID) {
			continue
		}
		if _, _, err := m.Jobs.Enqueue(ctx, jobRepair, Payload{InstanceID: inst.ID}, jobs.EnqueueOptions{
			Priority: 20, MaxAttempts: 3, CreatedBy: "system:managed-watchdog",
		}); err != nil {
			m.log().Error("queue managed credential repair", "instance", inst.Name, "error", err)
		}
	}
	return nil
}

// CreateJob adopts a linked credential or creates a managed one.
func (m *Manager) CreateJob(ctx context.Context, ex *jobs.Execution) (any, error) {
	inst, err := m.instanceFromPayload(ctx, ex)
	if err != nil {
		return nil, err
	}
	if in, err := m.inspectAuth(ctx, inst); err == nil && in.Token != "" {
		if inst.PBSuperuserManaged {
			_ = m.Store.MarkPBAuthVerified(ctx, inst.ID, in.RecordID, store.Now())
			return map[string]any{"instanceId": inst.ID, "status": "already_managed"}, nil
		}
		if err := m.Store.SetManagedPBAuth(ctx, inst.ID, inst.PBSuperuserEmail, inst.PBSuperuserSecret, in.RecordID, store.Now()); err != nil {
			return nil, err
		}
		m.audit(ctx, inst.ID, "pb.managed_superuser_adopted", inst.PBSuperuserEmail)
		return map[string]any{"instanceId": inst.ID, "status": "adopted"}, nil
	}
	email := inst.PBSuperuserEmail
	if email == "" {
		email = managedEmail(inst.ID)
	}
	password, err := randomPassword()
	if err != nil {
		return nil, err
	}
	wasRunning, err := m.stopForWrite(ctx, inst)
	if err != nil {
		return nil, err
	}
	j := journal{InstanceID: inst.ID, Email: email, ServiceName: inst.ServiceName, WasRunning: wasRunning, Phase: "creating", CreatedAt: store.Now()}
	if err := m.writeJournal(j); err != nil {
		return nil, err
	}
	if _, stderr, err := m.runSuperuser(ctx, inst, "delete", email, ""); err != nil && !notFound(stderr) {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, fmt.Errorf("remove old managed account: %s", commandError(err, stderr))
	}
	if _, stderr, err := m.runSuperuser(ctx, inst, "upsert", email, password); err != nil {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, fmt.Errorf("create managed account: %s", commandError(err, stderr))
	}
	j.Phase = "created"
	_ = m.writeJournal(j)
	if err := m.startAndWait(ctx, inst); err != nil {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, err
	}
	client := pocketbase.New(inst.Port)
	session, err := client.AuthSession(ctx, email, password)
	if err != nil {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, fmt.Errorf("verify managed account: %w", err)
	}
	sealed, err := m.Secrets.Seal("pb_superuser:"+inst.ID, password)
	if err != nil {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, err
	}
	if err := m.Store.MarkPBAuthRepaired(ctx, inst.ID, email, sealed, session.RecordID, store.Now()); err != nil {
		return nil, err
	}
	if !wasRunning {
		_ = m.stop(ctx, inst)
	}
	_ = m.removeJournal(inst.ID)
	m.audit(ctx, inst.ID, "pb.managed_superuser_created", email)
	return map[string]any{"instanceId": inst.ID, "status": "created", "email": email}, nil
}

// RepairJob deletes and recreates a drifted managed account.
func (m *Manager) RepairJob(ctx context.Context, ex *jobs.Execution) (any, error) {
	inst, err := m.instanceFromPayload(ctx, ex)
	if err != nil {
		return nil, err
	}
	if !inst.PBSuperuserManaged {
		return nil, errors.New("instance does not use a managed superuser")
	}
	if session, err := m.authenticate(ctx, inst); err == nil {
		_ = m.Store.MarkPBAuthVerified(ctx, inst.ID, session.RecordID, store.Now())
		return map[string]any{"instanceId": inst.ID, "status": "already_current"}, nil
	}
	wasRunning, err := m.stopForWrite(ctx, inst)
	if err != nil {
		return nil, err
	}
	email := inst.PBSuperuserEmail
	if email == "" {
		email = managedEmail(inst.ID)
	}
	oldRecordID := inst.PBSuperuserRecordID
	password, err := randomPassword()
	if err != nil {
		return nil, err
	}
	j := journal{InstanceID: inst.ID, Email: email, ServiceName: inst.ServiceName, WasRunning: wasRunning, Phase: "repairing", CreatedAt: store.Now()}
	if err := m.writeJournal(j); err != nil {
		return nil, err
	}
	if _, stderr, err := m.runSuperuser(ctx, inst, "delete", email, ""); err != nil && !notFound(stderr) {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, fmt.Errorf("delete drifted account: %s", commandError(err, stderr))
	}
	if _, stderr, err := m.runSuperuser(ctx, inst, "upsert", email, password); err != nil {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, fmt.Errorf("recreate managed account: %s", commandError(err, stderr))
	}
	j.Phase = "created"
	_ = m.writeJournal(j)
	if err := m.startAndWait(ctx, inst); err != nil {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, err
	}
	client := pocketbase.New(inst.Port)
	session, err := client.AuthSession(ctx, email, password)
	if err != nil {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, fmt.Errorf("verify recreated account: %w", err)
	}
	if oldRecordID != "" && oldRecordID != session.RecordID {
		m.cleanupOldRecord(ctx, inst, client, session.Token, oldRecordID, session.RecordID)
	}
	sealed, err := m.Secrets.Seal("pb_superuser:"+inst.ID, password)
	if err != nil {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, err
	}
	if err := m.Store.MarkPBAuthRepaired(ctx, inst.ID, email, sealed, session.RecordID, store.Now()); err != nil {
		return nil, err
	}
	if !wasRunning {
		_ = m.stop(ctx, inst)
	}
	_ = m.removeJournal(inst.ID)
	m.audit(ctx, inst.ID, "pb.managed_superuser_repaired", email)
	return map[string]any{"instanceId": inst.ID, "status": "repaired", "email": email}, nil
}

// DeleteJob removes the managed account and stops managing it.
func (m *Manager) DeleteJob(ctx context.Context, ex *jobs.Execution) (any, error) {
	inst, err := m.instanceFromPayload(ctx, ex)
	if err != nil {
		return nil, err
	}
	if !inst.PBSuperuserManaged || inst.PBSuperuserEmail == "" {
		return nil, errors.New("instance does not have a managed superuser")
	}
	wasRunning, err := m.stopForWrite(ctx, inst)
	if err != nil {
		return nil, err
	}
	if _, stderr, err := m.runSuperuser(ctx, inst, "delete", inst.PBSuperuserEmail, ""); err != nil && !notFound(stderr) {
		_ = m.restoreState(context.Background(), inst, wasRunning)
		return nil, fmt.Errorf("delete managed account: %s", commandError(err, stderr))
	}
	if err := m.Store.ClearManagedPBAuth(ctx, inst.ID); err != nil {
		return nil, err
	}
	if wasRunning {
		if err := m.startAndWait(ctx, inst); err != nil {
			return nil, err
		}
	}
	m.audit(ctx, inst.ID, "pb.managed_superuser_deleted", inst.PBSuperuserEmail)
	return map[string]any{"instanceId": inst.ID, "status": "deleted"}, nil
}

// Recover cleans up a create/repair interrupted by a crash.
func (m *Manager) Recover(ctx context.Context) error {
	dir := m.journalDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
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
			errs = append(errs, "invalid managed journal "+entry.Name())
			continue
		}
		if j.ServiceName == "" || j.Email == "" {
			continue
		}
		inst := &store.Instance{ID: j.InstanceID, ServiceName: j.ServiceName, Port: 0}
		if m.Store != nil {
			if got, err := m.Store.GetInstance(ctx, j.InstanceID); err == nil {
				inst = got
			}
		}
		_ = m.stop(ctx, inst)
		_, stderr, err := m.runSuperuser(ctx, inst, "delete", j.Email, "")
		if err != nil && !notFound(stderr) {
			errs = append(errs, commandError(err, stderr))
			_ = m.restoreState(ctx, inst, j.WasRunning)
			continue
		}
		_ = m.restoreState(ctx, inst, j.WasRunning)
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (m *Manager) instanceFromPayload(ctx context.Context, ex *jobs.Execution) (*store.Instance, error) {
	var p Payload
	if err := ex.Payload(&p); err != nil {
		return nil, err
	}
	if p.InstanceID == "" {
		return nil, errors.New("instanceId is required")
	}
	return m.Store.GetInstance(ctx, p.InstanceID)
}

func (m *Manager) authenticate(ctx context.Context, inst *store.Instance) (pocketbase.AuthSession, error) {
	if inst.PBSuperuserEmail == "" || inst.PBSuperuserSecret == "" {
		return pocketbase.AuthSession{}, pocketbase.ErrUnauthorized
	}
	password, err := m.Secrets.Open("pb_superuser:"+inst.ID, inst.PBSuperuserSecret)
	if err != nil {
		return pocketbase.AuthSession{}, err
	}
	return pocketbase.New(inst.Port).AuthSession(ctx, inst.PBSuperuserEmail, password)
}

func (m *Manager) inspectAuth(ctx context.Context, inst *store.Instance) (pocketbase.AuthSession, error) {
	return m.authenticate(ctx, inst)
}

func (m *Manager) stopForWrite(ctx context.Context, inst *store.Instance) (bool, error) {
	st, err := m.Systemd.Status(ctx, inst.ServiceName)
	if err != nil {
		return false, err
	}
	if !st.Exists {
		return false, fmt.Errorf("systemd unit %s does not exist", inst.ServiceName)
	}
	wasRunning := st.Running
	if st.Running {
		if err := m.Systemd.Stop(ctx, inst.ServiceName); err != nil {
			return false, err
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := m.Systemd.Status(ctx, inst.ServiceName)
		if err == nil && !cur.Running {
			return wasRunning, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return wasRunning, errors.New("service did not stop")
}

func (m *Manager) stop(ctx context.Context, inst *store.Instance) error {
	_, err := m.stopForWrite(ctx, inst)
	return err
}

func (m *Manager) startAndWait(ctx context.Context, inst *store.Instance) error {
	_ = m.Systemd.ResetFailed(ctx, inst.ServiceName)
	if err := m.Systemd.Start(ctx, inst.ServiceName); err != nil {
		return err
	}
	if inst.Port <= 0 {
		return nil
	}
	return pocketbase.New(inst.Port).WaitHealthy(ctx, 90*time.Second)
}

func (m *Manager) restoreState(ctx context.Context, inst *store.Instance, wasRunning bool) error {
	if !wasRunning {
		return nil
	}
	return m.startAndWait(ctx, inst)
}

func (m *Manager) runSuperuser(ctx context.Context, inst *store.Instance, action, email, password string) ([]byte, []byte, error) {
	runner := m.Runner
	if runner == nil {
		runner = platform.ExecRunner{}
	}
	name := inst.BinaryPath
	var args []string
	if inst.RunAsUser != "" {
		name = "runuser"
		args = append(args, "-u", inst.RunAsUser, "--")
	}
	args = append(args, inst.BinaryPath, "superuser", action, email)
	if action == "upsert" {
		args = append(args, password)
	}
	args = append(args, "--dir", inst.DataDir)
	return runner.Run(ctx, name, args...)
}

func (m *Manager) cleanupOldRecord(ctx context.Context, inst *store.Instance, client *pocketbase.Client, token, oldID, newID string) {
	if oldID == "" || oldID == newID {
		return
	}
	users, err := client.ListSuperusers(ctx, token)
	if err != nil {
		m.log().Warn("managed account cleanup: list superusers", "error", err)
		return
	}
	var stale *pocketbase.Superuser
	for i := range users {
		if users[i].ID == oldID && users[i].ID != newID {
			stale = &users[i]
			break
		}
	}
	if stale == nil || stale.Email == "" {
		return
	}
	// The record-level API is not available for system collections on all
	// PocketBase versions, so use the official CLI. This path is only reached
	// when the account email was changed rather than deleted.
	wasRunning, err := m.stopForWrite(ctx, inst)
	if err != nil {
		m.log().Warn("managed account cleanup: stop service", "error", err)
		return
	}
	if _, stderr, err := m.runSuperuser(ctx, inst, "delete", stale.Email, ""); err != nil && !notFound(stderr) {
		m.log().Warn("managed account cleanup: delete stale email", "email", stale.Email, "error", commandError(err, stderr))
	}
	_ = m.restoreState(ctx, inst, wasRunning)
}

func (m *Manager) hasActiveRepair(ctx context.Context, instanceID string) bool {
	active, err := m.Store.ListActiveJobs(ctx)
	if err != nil {
		return false
	}
	for _, j := range active {
		if j.Type != jobRepair {
			continue
		}
		var p Payload
		if json.Unmarshal([]byte(j.Payload), &p) == nil && p.InstanceID == instanceID {
			return true
		}
	}
	return false
}

func (m *Manager) audit(ctx context.Context, instanceID, action, email string) {
	if m.Store == nil {
		return
	}
	raw, _ := json.Marshal(map[string]string{"email": email, "mode": "managed"})
	_ = m.Store.CreateAudit(ctx, &store.AuditEntry{
		ActorType: "system", ActorName: "pbmanager", Action: action,
		TargetType: "instance", TargetID: instanceID, Detail: string(raw),
	})
}

func (m *Manager) journalDir() string { return filepath.Join(m.DataDir, "managed") }

func (m *Manager) writeJournal(j journal) error {
	if err := platform.EnsureDir(m.journalDir(), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(j, "", "  ")
	return platform.AtomicWrite(filepath.Join(m.journalDir(), j.InstanceID+".json"), raw, 0o600)
}

func (m *Manager) removeJournal(instanceID string) error {
	err := os.Remove(filepath.Join(m.journalDir(), instanceID+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func managedEmail(instanceID string) string {
	return "pbmanager-" + instanceID[:min(12, len(instanceID))] + "@managed.local"
}

func randomPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func notFound(stderr []byte) bool {
	s := strings.ToLower(string(stderr))
	return strings.Contains(s, "not found") || strings.Contains(s, "no superuser")
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}
