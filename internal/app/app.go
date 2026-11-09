// Package app wires the shared application core used by both the REST server
// and the root CLI.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/apexo/pbmanager/internal/auth"
	"github.com/apexo/pbmanager/internal/config"
	"github.com/apexo/pbmanager/internal/cron"
	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/platform"
	"github.com/apexo/pbmanager/internal/secret"
	"github.com/apexo/pbmanager/internal/services/backup"
	"github.com/apexo/pbmanager/internal/services/caddy"
	"github.com/apexo/pbmanager/internal/services/discovery"
	"github.com/apexo/pbmanager/internal/services/dns"
	"github.com/apexo/pbmanager/internal/services/instances"
	"github.com/apexo/pbmanager/internal/services/monitor"
	"github.com/apexo/pbmanager/internal/services/systemd"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

// App is the shared core.
type App struct {
	Config    config.Runtime
	Store     *store.Store
	Secrets   *secret.Box
	Auth      *auth.Service
	Settings  *settings.Service
	Systemd   *systemd.Manager
	Caddy     *caddy.Manager
	DNS       *dns.Manager
	Backup    *backup.Manager
	Instances *instances.Service
	Jobs      *jobs.Engine
	Monitor   *monitor.Service
	Discovery *discovery.Service
	Log       *slog.Logger

	wg      sync.WaitGroup
	started bool
	worker  *workerLock
	mu      sync.Mutex
}

// Open initializes storage and all services without starting background work.
func Open(ctx context.Context, cfg config.Runtime, log *slog.Logger) (*App, error) {
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	if err := cfg.EnsureDirectories(); err != nil {
		return nil, err
	}
	box, err := secret.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, err
	}
	bootstrap := settings.Bootstrap{
		ConfigPath: cfg.ConfigPath, DataDir: cfg.DataDir, DBPath: cfg.DBPath,
		ListenAddr: cfg.ListenAddr, PublicURL: cfg.PublicURL,
	}
	settingsSvc, err := settings.New(ctx, st, bootstrap)
	if err != nil {
		st.Close()
		return nil, err
	}
	runner := platform.ExecRunner{}
	cfgFunc := func() settings.Config { return settingsSvc.Get() }
	systemdMgr := &systemd.Manager{Runner: runner, SystemctlBinary: settingsSvc.Get().SystemctlBinary, JournalctlBinary: settingsSvc.Get().JournalctlBinary, AllowUnitWrite: true}
	caddyMgr := &caddy.Manager{Runner: runner, Config: cfgFunc, BackupDir: func() string { return settingsSvc.Get().BackupDir }, Log: log}
	dnsMgr := &dns.Manager{Store: st, Secrets: box, Cfg: cfgFunc}
	backupMgr := &backup.Manager{Store: st, Secrets: box, Runner: runner, Cfg: cfgFunc, Systemd: systemdMgr, Log: log}
	instSvc := &instances.Service{
		Store: st, Secrets: box, Cfg: cfgFunc, Systemd: systemdMgr, Caddy: caddyMgr,
		DNS: dnsMgr, Backup: backupMgr, Runner: runner, Log: log,
	}
	jobEngine := jobs.New(st, log, map[string]jobs.Handler{})
	instSvc.Jobs = jobEngine
	monitorSvc := &monitor.Service{Store: st, Cfg: cfgFunc, Systemd: systemdMgr, Caddy: caddyMgr, DataDir: cfg.DataDir, Log: log}
	discoverySvc := &discovery.Service{Store: st, Cfg: cfgFunc}
	app := &App{
		Config: cfg, Store: st, Secrets: box, Auth: auth.NewService(st, cfg.SessionTTL()),
		Settings: settingsSvc, Systemd: systemdMgr, Caddy: caddyMgr, DNS: dnsMgr,
		Backup: backupMgr, Instances: instSvc, Jobs: jobEngine, Monitor: monitorSvc,
		Discovery: discoverySvc, Log: log,
	}
	app.registerHandlers()
	return app, nil
}

func (a *App) registerHandlers() {
	a.Jobs.Register("instance.provision", a.Instances.Provision)
	a.Jobs.Register("instance.update", a.Instances.Update)
	a.Jobs.Register("instance.start", a.Instances.Start)
	a.Jobs.Register("instance.stop", a.Instances.Stop)
	a.Jobs.Register("instance.restart", a.Instances.Restart)
	a.Jobs.Register("instance.delete", a.Instances.Delete)
	a.Jobs.Register("instance.apply-smtp", a.Instances.ApplySMTP)
	a.Jobs.Register("instance.apply-storage", a.Instances.ApplyStorage)
	a.Jobs.Register("instance.sync-caddy", a.Instances.SyncCaddy)
	a.Jobs.Register("domain.attach", a.Instances.AttachDomainJob)
	a.Jobs.Register("domain.remove", a.Instances.RemoveDomainJob)
	a.Jobs.Register("backup.create", a.backupCreate)
	a.Jobs.Register("backup.verify", a.backupVerify)
	a.Jobs.Register("backup.restore", a.backupRestore)
	a.Jobs.Register("caddy.ensure-import", a.caddyEnsureImport)
}

// Start launches background workers and recovery.
func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.started {
		a.mu.Unlock()
		return errors.New("app already started")
	}
	a.started = true
	a.mu.Unlock()

	acquired, err := a.TryAcquireWorker()
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("another pbmanager process already owns the job worker; refusing to start a duplicate")
	}
	ok := false
	defer func() {
		if !ok {
			a.ReleaseWorker()
		}
	}()
	if err := a.Backup.RecoverInterrupted(ctx, func(msg string) { a.Log.Info(msg) }); err != nil {
		a.Log.Error("interrupted restore recovery failed", "error", err)
	}
	recovered, err := a.Jobs.Recover(ctx)
	if err != nil {
		return fmt.Errorf("recover jobs: %w", err)
	}
	if len(recovered) > 0 {
		a.Log.Warn("recovered interrupted jobs", "count", len(recovered), "ids", recovered)
	}
	a.initializeSchedules(ctx)
	a.wg.Add(3)
	go func() {
		defer a.wg.Done()
		a.Jobs.Start(ctx)
	}()
	go func() {
		defer a.wg.Done()
		a.Monitor.Start(ctx)
	}()
	go func() {
		defer a.wg.Done()
		a.scheduler(ctx)
	}()
	a.Log.Info("background services started")
	ok = true
	return nil
}

// Stop asks job workers to stop.
func (a *App) Stop() {
	a.Jobs.Stop()
}

// Wait waits for background goroutines.
func (a *App) Wait() { a.wg.Wait() }

// Close releases resources.
func (a *App) Close() error {
	a.ReleaseWorker()
	return a.Store.Close()
}

func (a *App) initializeSchedules(ctx context.Context) {
	list, err := a.Store.ListInstances(ctx)
	if err != nil {
		return
	}
	for i := range list {
		inst := &list[i]
		if !inst.BackupEnabled {
			continue
		}
		if inst.NextBackupAt != "" {
			continue
		}
		next, err := cron.NextFromString(inst.BackupCron, time.Now().UTC())
		if err != nil {
			a.Log.Warn("invalid backup schedule", "instance", inst.Name, "error", err)
			continue
		}
		_ = a.Store.SetNextBackup(ctx, inst.ID, store.FormatTime(next))
	}
}

func (a *App) scheduler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	maintenance := time.NewTicker(6 * time.Hour)
	defer maintenance.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.enqueueDueBackups(ctx)
		case <-maintenance.C:
			a.maintenance(ctx)
		}
	}
}

func (a *App) enqueueDueBackups(ctx context.Context) {
	due, err := a.Store.ListDueBackups(ctx)
	if err != nil {
		a.Log.Error("list due backups", "error", err)
		return
	}
	for i := range due {
		inst := &due[i]
		next, err := cron.NextFromString(inst.BackupCron, time.Now().UTC())
		if err != nil {
			a.Log.Warn("invalid backup cron", "instance", inst.Name, "error", err)
			continue
		}
		job, _, err := a.Jobs.Enqueue(ctx, "backup.create", backupCreatePayload{
			InstanceID: inst.ID, Automatic: true, Note: "scheduled backup",
		}, jobs.EnqueueOptions{
			Priority: 3, MaxAttempts: 4, CreatedBy: "scheduler",
			IdempotencyKey: "backup:auto:" + inst.ID + ":" + inst.NextBackupAt,
		})
		if err != nil {
			a.Log.Error("enqueue scheduled backup", "instance", inst.Name, "error", err)
			continue
		}
		_ = a.Store.SetNextBackup(ctx, inst.ID, store.FormatTime(next))
		a.Log.Info("scheduled backup queued", "instance", inst.Name, "job", job.ID, "next", next)
	}
}

func (a *App) maintenance(ctx context.Context) {
	cfg := a.Settings.Get()
	if n, err := a.Store.DeleteFinishedJobs(ctx, time.Now().UTC().AddDate(0, 0, -cfg.JobRetentionDays)); err == nil && n > 0 {
		a.Log.Info("pruned finished jobs", "count", n)
	}
	if n, err := a.Store.DeleteOldAudit(ctx, time.Now().UTC().AddDate(0, 0, -cfg.AuditRetentionDays)); err == nil && n > 0 {
		a.Log.Info("pruned audit history", "count", n)
	}
	if n, err := a.Store.CleanupSessions(ctx, time.Now().UTC().AddDate(0, 0, -7)); err == nil && n > 0 {
		a.Log.Info("pruned sessions", "count", n)
	}
}

type backupCreatePayload struct {
	InstanceID string `json:"instanceId"`
	Automatic  bool   `json:"automatic"`
	Note       string `json:"note"`
}

type backupVerifyPayload struct {
	BackupID string `json:"backupId"`
}

type backupRestorePayload struct {
	BackupID     string `json:"backupId"`
	Confirmation string `json:"confirmation"`
}

func (a *App) backupCreate(ctx context.Context, ex *jobs.Execution) (any, error) {
	var p backupCreatePayload
	if err := ex.Payload(&p); err != nil {
		return nil, err
	}
	inst, err := a.Store.GetInstance(ctx, p.InstanceID)
	if err != nil {
		return nil, err
	}
	var b *store.Backup
	err = ex.Step(ctx, "backup", "Create and verify a safe backup", func(ctx context.Context) (string, error) {
		var e error
		b, e = a.Backup.Create(ctx, inst, p.Automatic, p.Note, func(msg string) {
			ex.Log.Info("backup progress", "job", ex.Job.ID, "message", msg)
		})
		if e != nil {
			return "", e
		}
		return fmt.Sprintf(`{"backupId":%q,"method":%q,"size":%d}`, b.ID, b.Method, b.Size), nil
	})
	if err != nil {
		if b != nil {
			a.auditSystem(ctx, "backup.failed", "instance", inst.ID, map[string]any{"backupId": b.ID, "error": truncate(err.Error(), 500)})
		}
		return nil, err
	}
	if b == nil {
		var ref struct {
			BackupID string `json:"backupId"`
		}
		if err := json.Unmarshal([]byte(ex.StepOutput(ctx, "backup")), &ref); err != nil || ref.BackupID == "" {
			return nil, errors.New("backup checkpoint exists but its artifact reference is missing")
		}
		b, err = a.Store.GetBackup(ctx, ref.BackupID)
		if err != nil {
			return nil, err
		}
	}
	next := ""
	if inst.BackupEnabled {
		if n, e := cron.NextFromString(inst.BackupCron, time.Now().UTC()); e == nil {
			next = store.FormatTime(n)
		}
	}
	_ = a.Store.RecordBackupRun(ctx, inst.ID, store.Now(), next)
	if _, err := a.Backup.Prune(ctx, inst.ID, inst.BackupRetention); err != nil {
		a.Log.Warn("backup retention prune failed", "instance", inst.Name, "error", err)
	}
	a.auditSystem(ctx, "backup.created", "instance", inst.ID, map[string]any{"backupId": b.ID, "method": b.Method, "size": b.Size, "automatic": p.Automatic})
	return map[string]any{"backupId": b.ID, "method": b.Method, "size": b.Size, "sha256": b.SHA256}, nil
}

func (a *App) backupVerify(ctx context.Context, ex *jobs.Execution) (any, error) {
	var p backupVerifyPayload
	if err := ex.Payload(&p); err != nil {
		return nil, err
	}
	b, err := a.Store.GetBackup(ctx, p.BackupID)
	if err != nil {
		return nil, err
	}
	if err := ex.Step(ctx, "verify", "Verify checksum and database integrity", func(ctx context.Context) (string, error) {
		if err := a.Backup.Verify(ctx, b); err != nil {
			return "", err
		}
		return "Backup checksum and database integrity verified", nil
	}); err != nil {
		return nil, err
	}
	a.auditSystem(ctx, "backup.verified", "backup", b.ID, map[string]any{"instanceId": b.InstanceID})
	return map[string]any{"backupId": b.ID, "status": "verified"}, nil
}

func (a *App) backupRestore(ctx context.Context, ex *jobs.Execution) (any, error) {
	var p backupRestorePayload
	if err := ex.Payload(&p); err != nil {
		return nil, err
	}
	b, err := a.Store.GetBackup(ctx, p.BackupID)
	if err != nil {
		return nil, err
	}
	inst, err := a.Store.GetInstance(ctx, b.InstanceID)
	if err != nil {
		return nil, err
	}
	if err := ex.Step(ctx, "restore", "Restore backup with safety backup and rollback", func(ctx context.Context) (string, error) {
		if err := a.Backup.Restore(ctx, inst, b, p.Confirmation, func(msg string) {
			ex.Log.Info("restore progress", "job", ex.Job.ID, "message", msg)
		}); err != nil {
			return "", err
		}
		return "Restore completed and instance verified healthy", nil
	}); err != nil {
		a.auditSystem(ctx, "backup.restore_failed", "instance", inst.ID, map[string]any{"backupId": b.ID, "error": truncate(err.Error(), 500)})
		return nil, err
	}
	a.auditSystem(ctx, "backup.restored", "instance", inst.ID, map[string]any{"backupId": b.ID})
	return map[string]any{"backupId": b.ID, "instanceId": inst.ID, "status": "restored"}, nil
}

func (a *App) caddyEnsureImport(ctx context.Context, ex *jobs.Execution) (any, error) {
	added := false
	if err := ex.Step(ctx, "import", "Add the managed import directive if missing, validate, and reload", func(ctx context.Context) (string, error) {
		var err error
		added, err = a.Caddy.EnsureImport(ctx)
		if err != nil {
			return "", jobs.Retryable(err)
		}
		if added {
			return "Managed import directive added; Caddy validated and reloaded", nil
		}
		return "Managed import directive already present", nil
	}); err != nil {
		return nil, err
	}
	a.auditSystem(ctx, "caddy.import_ensured", "caddy", "config", map[string]any{"added": added})
	return map[string]any{"added": added}, nil
}

func (a *App) auditSystem(ctx context.Context, action, targetType, targetID string, detail any) {
	raw, _ := json.Marshal(detail)
	_ = a.Store.CreateAudit(ctx, &store.AuditEntry{
		ActorType: "system", ActorName: "pbmanager", Action: action, TargetType: targetType,
		TargetID: targetID, Detail: string(raw),
	})
}

// EnqueueAudit writes an audit record for an API enqueue.
func (a *App) EnqueueAudit(ctx context.Context, actorType, actorID, actorName, action, targetType, targetID string, detail any) {
	raw, _ := json.Marshal(detail)
	_ = a.Store.CreateAudit(ctx, &store.AuditEntry{
		ActorType: actorType, ActorID: actorID, ActorName: actorName, Action: action,
		TargetType: targetType, TargetID: targetID, Detail: string(raw),
	})
}

// Diagnose contains read-only health facts used by the CLI and API.
type Diagnose struct {
	DataDir        string   `json:"dataDir"`
	DBPath         string   `json:"dbPath"`
	DBExists       bool     `json:"dbExists"`
	DBPermissions  string   `json:"dbPermissions"`
	KeyExists      bool     `json:"keyExists"`
	KeyPermissions string   `json:"keyPermissions"`
	SQLiteCheck    string   `json:"sqliteCheck"`
	Migrations     []int    `json:"migrations"`
	AdminCount     int      `json:"adminCount"`
	InstanceCount  int      `json:"instanceCount"`
	ServiceActive  bool     `json:"serviceActive"`
	Warnings       []string `json:"warnings"`
	Errors         []string `json:"errors"`
}

// Diagnose performs a read-only self check.
func (a *App) Diagnose(ctx context.Context) Diagnose {
	d := Diagnose{DataDir: a.Config.DataDir, DBPath: a.Config.DBPath}
	if info, err := os.Stat(a.Config.DBPath); err == nil {
		d.DBExists = true
		d.DBPermissions = fmt.Sprintf("%04o", info.Mode().Perm())
		if info.Mode().Perm()&0o077 != 0 {
			d.Warnings = append(d.Warnings, "manager database is accessible by group/world")
		}
	} else {
		d.Errors = append(d.Errors, "database missing: "+err.Error())
	}
	if info, err := os.Stat(filepath.Join(a.Config.DataDir, "secret.key")); err == nil {
		d.KeyExists = true
		d.KeyPermissions = fmt.Sprintf("%04o", info.Mode().Perm())
		if info.Mode().Perm()&0o077 != 0 {
			d.Warnings = append(d.Warnings, "secret key permissions are too broad")
		}
	} else {
		d.Errors = append(d.Errors, "secret key missing: "+err.Error())
	}
	if result, err := a.Store.QuickCheck(ctx); err == nil {
		d.SQLiteCheck = result
	} else {
		d.SQLiteCheck = "failed"
		d.Errors = append(d.Errors, err.Error())
	}
	if versions, err := a.Store.MigrateStatus(ctx); err == nil {
		d.Migrations = versions
	}
	if n, err := a.Store.CountAdmins(ctx); err == nil {
		d.AdminCount = n
	}
	if n, err := a.Store.CountInstances(ctx); err == nil {
		d.InstanceCount = n
	}
	if _, _, err := (platform.ExecRunner{}).Run(ctx, a.Settings.Get().SystemctlBinary, "is-active", "pbmanager"); err == nil {
		d.ServiceActive = true
	}
	return d
}

// UserFacingError chooses a safe error message for CLI output.
func UserFacingError(err error) string {
	if err == nil {
		return ""
	}
	var dup *store.DuplicateError
	if errors.As(err, &dup) {
		return dup.Error()
	}
	return strings.TrimSpace(err.Error())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
