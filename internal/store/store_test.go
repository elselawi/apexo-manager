package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manager.db")
	st, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestOpenMigratesAndPersists(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "manager.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.QuickCheck(ctx); err != nil {
		t.Fatalf("integrity: %v", err)
	}
	versions, err := st.MigrateStatus(ctx)
	if err != nil || len(versions) == 0 {
		t.Fatalf("migrations: %v %v", versions, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	versions2, _ := st.MigrateStatus(ctx)
	if len(versions2) != len(versions) {
		t.Fatalf("migrations were reapplied: %v != %v", versions2, versions)
	}
}

func TestAdminAndSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	a := &Admin{Username: "owner", DisplayName: "Owner", PasswordHash: "hash", Role: "owner"}
	if err := st.CreateAdmin(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetAdminByUsername(ctx, "OWNER"); err != nil {
		t.Fatalf("case-insensitive lookup: %v", err)
	}
	dup := &Admin{Username: "OWNER", PasswordHash: "hash", Role: "admin"}
	if err := st.CreateAdmin(ctx, dup); err == nil {
		t.Fatal("expected duplicate username error")
	}
	sess := &Session{AdminID: a.ID, TokenHash: "tokenhash", CSRFToken: "csrf", CreatedAt: Now(), ExpiresAt: Now(), LastSeenAt: Now()}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSessionByTokenHash(ctx, "tokenhash")
	if err != nil || got.AdminID != a.ID {
		t.Fatalf("session: %v %#v", err, got)
	}
	if err := st.RevokeAdminSessions(ctx, a.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetSessionByTokenHash(ctx, "tokenhash")
	if got.RevokedAt == "" {
		t.Fatal("session was not revoked")
	}
	if err := st.DeleteAdmin(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetAdminByID(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestInstanceDomainCRUD(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	inst := &Instance{
		Name: "clinic-a", Hostname: "clinic-a", BaseDir: "/opt/clinic-a", BinaryPath: "/opt/clinic-a/pocketbase",
		DataDir: "/opt/clinic-a/pb_data", Port: 8090, ServiceName: "clinic-a", ServiceFile: "/etc/systemd/system/clinic-a.service",
		Managed: true, Source: "provisioned", BackupEnabled: true, BackupCron: "0 3 * * *", BackupRetention: 7,
	}
	if err := st.CreateInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetInstanceByName(ctx, "CLINIC-A")
	if err != nil || got.ID != inst.ID {
		t.Fatalf("get instance: %v %#v", err, got)
	}
	if err := st.UpdateInstanceRuntime(ctx, inst.ID, "running", "healthy", Now(), "", Now()); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetInstance(ctx, inst.ID)
	if got.Status != "running" || got.Health != "healthy" {
		t.Fatalf("runtime not updated: %#v", got)
	}
	d := &Domain{InstanceID: inst.ID, Hostname: "clinic-a.example.com", CaddyManaged: true}
	if err := st.CreateDomain(ctx, d); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListDomainsByInstance(ctx, inst.ID)
	if err != nil || len(list) != 1 || list[0].InstanceName != "clinic-a" {
		t.Fatalf("domains: %v %#v", err, list)
	}
	if err := st.DeleteInstance(ctx, inst.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetDomain(ctx, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("domain should cascade, got %v", err)
	}
}

func TestJobLifecycleAndRecovery(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	job := &Job{Type: "test.job", Payload: `{}`, IdempotencyKey: "once", MaxAttempts: 3}
	created, isNew, err := st.CreateJob(ctx, job)
	if err != nil || !isNew {
		t.Fatalf("create job: %v %v", err, isNew)
	}
	dup, isNew, err := st.CreateJob(ctx, &Job{Type: "test.job", Payload: `{}`, IdempotencyKey: "once"})
	if err != nil || isNew || dup.ID != created.ID {
		t.Fatalf("idempotency: %v %v %#v", err, isNew, dup)
	}
	claimed, err := st.ClaimNextJob(ctx)
	if err != nil || claimed.ID != created.ID || claimed.Status != "running" {
		t.Fatalf("claim: %v %#v", err, claimed)
	}
	if _, err := st.ClaimNextJob(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second claim should be empty, got %v", err)
	}
	if err := st.BeginJobStep(ctx, job.ID, "download", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecoverRunningJobs(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, _ := st.GetJob(ctx, job.ID)
	if recovered.Status != "queued" || recovered.RecoveredCount != 1 {
		t.Fatalf("not recovered: %#v", recovered)
	}
	step, _ := st.GetJobStep(ctx, job.ID, "download")
	if step.Status != "pending" {
		t.Fatalf("running step should reset: %#v", step)
	}
	if err := st.BeginJobStep(ctx, job.ID, "download", "retry"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishJobStep(ctx, job.ID, "download", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteJob(ctx, job.ID, `{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	final, _ := st.GetJob(ctx, job.ID)
	if final.Status != "succeeded" || final.Progress != 100 {
		t.Fatalf("final: %#v", final)
	}
}

func TestProfileDeletionUnsetsAssignments(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	p := &SMTPProfile{Name: "mail", Host: "smtp.example.com", Port: 587, Status: "created", TLSMode: "starttls", AuthMethod: "PLAIN"}
	if err := st.CreateSMTPProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	inst := &Instance{
		Name: "clinic-b", Hostname: "clinic-b", BaseDir: "/opt/clinic-b", BinaryPath: "/opt/clinic-b/pocketbase",
		DataDir: "/opt/clinic-b/pb_data", Port: 8091, ServiceName: "clinic-b", ServiceFile: "/etc/systemd/system/clinic-b.service",
		SMTPProfileID: p.ID, BackupRetention: 7,
	}
	if err := st.CreateInstance(ctx, inst); err != nil {
		t.Fatal(err)
	}
	list, _ := st.ListSMTPProfiles(ctx)
	if len(list) != 1 || list[0].UsageCount != 1 {
		t.Fatalf("usage count: %#v", list)
	}
	if err := st.DeleteSMTPProfile(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetInstance(ctx, inst.ID)
	if got.SMTPProfileID != "" {
		t.Fatalf("assignment not cleared: %#v", got)
	}
}
