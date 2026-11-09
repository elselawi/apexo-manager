package backup

import (
	"context"
	"database/sql"
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
	"github.com/apexo/pbmanager/internal/services/pbauth"
	"github.com/apexo/pbmanager/internal/services/pocketbase"
	"github.com/apexo/pbmanager/internal/services/systemd"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

// Reporter receives human-readable progress updates for the job step.
type Reporter func(string)

// Manager creates, verifies, restores, and prunes backups.
type Manager struct {
	Store   *store.Store
	Secrets *secret.Box
	Runner  platform.Runner
	Cfg     func() settings.Config
	Systemd *systemd.Manager
	PBAuth  *pbauth.Manager
	Log     *slog.Logger
}

const (
	MethodNative  = "pocketbase_native"
	MethodSQLite  = "sqlite_snapshot"
	FormatManager = "pbmanager.backup.v1"
)

type manifest struct {
	Format       string `json:"format"`
	InstanceID   string `json:"instanceId"`
	InstanceName string `json:"instanceName"`
	CreatedAt    string `json:"createdAt"`
	Method       string `json:"method"`
	PocketBase   string `json:"pocketbaseVersion,omitempty"`
	SourceData   string `json:"sourceDataDir"`
	DatabaseHash string `json:"databaseSha256"`
	Note         string `json:"note,omitempty"`
}

// Create creates a backup. It prefers PocketBase's official backup API when
// linked superuser credentials work and otherwise uses a consistent SQLite
// snapshot with uploads/configuration archived separately.
func (m *Manager) Create(ctx context.Context, inst *store.Instance, automatic bool, note string, report Reporter) (*store.Backup, error) {
	if report == nil {
		report = func(string) {}
	}
	cfg := m.Cfg()
	if inst.DataDir == "" || !filepath.IsAbs(inst.DataDir) {
		return nil, errors.New("instance has no valid data directory")
	}
	root := filepath.Join(cfg.BackupDir, inst.Name)
	if err := platform.EnsureDir(root, 0o700); err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	base := sanitize(inst.Name) + "-" + stamp + "-" + store.NewID()[:8]
	b := &store.Backup{
		InstanceID: inst.ID,
		Filename:   base + ".tar.gz",
		Method:     MethodSQLite,
		Status:     "creating",
		Note:       note,
		Automatic:  automatic,
		Path:       filepath.Join(root, base+".tar.gz"),
	}
	if err := m.Store.CreateBackup(ctx, b); err != nil {
		return nil, err
	}
	fail := func(err error) (*store.Backup, error) {
		b.Status = "failed"
		b.Error = truncate(err.Error(), 2000)
		_ = m.Store.UpdateBackup(ctx, b)
		return b, err
	}
	report("Creating backup record " + b.ID)

	client, token, authErr := m.auth(ctx, inst)
	if authErr == nil && client != nil {
		native, nativeErr := m.tryNative(ctx, inst, client, token, root, b, report)
		if native {
			if nativeErr != nil {
				return fail(nativeErr)
			}
			return b, nil
		}
		if nativeErr != nil {
			report("Official backup failed; falling back to a consistent SQLite snapshot: " + truncate(nativeErr.Error(), 300))
		}
	} else if authErr != nil {
		if m.PBAuth != nil && m.PBAuth.Linked(inst) {
			report("Linked PocketBase credentials failed; falling back to a consistent SQLite snapshot")
		} else if automatic {
			// Scheduled native backups would require stopping and restarting the
			// service to create a temporary superuser. Use the safe live SQLite
			// snapshot instead so a schedule never introduces downtime.
			report("No linked PocketBase superuser; scheduled backup uses the safe SQLite snapshot")
		} else if m.PBAuth != nil {
			report("No linked PocketBase superuser; trying a short-lived superuser for this manual backup")
			err := m.PBAuth.With(ctx, inst, "manual-native-backup", func(client *pocketbase.Client, token string) error {
				native, nativeErr := m.tryNative(ctx, inst, client, token, root, b, report)
				if native && nativeErr != nil {
					return nativeErr
				}
				if !native && nativeErr != nil {
					return nativeErr
				}
				return nil
			})
			if err == nil && b.Method == MethodNative {
				return b, nil
			}
			if b.Method == MethodNative {
				return fail(err)
			}
			report("Short-lived superuser backup failed; falling back to a consistent SQLite snapshot: " + truncate(err.Error(), 300))
		}
	}
	report("Snapshotting SQLite database safely with VACUUM INTO")
	if err := m.snapshotBackup(ctx, inst, b.Path); err != nil {
		return fail(err)
	}
	if err := m.finishBackup(ctx, b, func(ctx context.Context) error { return ValidateTarBackup(ctx, b.Path) }); err != nil {
		return fail(err)
	}
	return b, nil
}

func (m *Manager) tryNative(ctx context.Context, inst *store.Instance, client *pocketbase.Client, token, root string, b *store.Backup, report Reporter) (bool, error) {
	report("Using PocketBase's official backup API")
	tmp, err := os.CreateTemp(root, ".native-*.zip")
	if err != nil {
		return false, err
	}
	nativeZip := tmp.Name()
	_ = tmp.Close()
	if err := m.nativeBackup(ctx, inst, client, token, nativeZip, report); err != nil {
		_ = os.Remove(nativeZip)
		return false, err
	}
	report("Bundling PocketBase database backup with uploads and configuration")
	if err := m.packageNativeBundle(ctx, inst, nativeZip, b.Path); err != nil {
		_ = os.Remove(nativeZip)
		return false, err
	}
	_ = os.Remove(nativeZip)
	b.Method = MethodNative
	b.Filename = filepath.Base(b.Path)
	if err := m.finishBackup(ctx, b, func(ctx context.Context) error { return ValidateTarBackup(ctx, b.Path) }); err != nil {
		return true, err
	}
	return true, nil
}

func (m *Manager) auth(ctx context.Context, inst *store.Instance) (*pocketbase.Client, string, error) {
	if inst.PBSuperuserEmail == "" || inst.PBSuperuserSecret == "" || m.Secrets == nil {
		return nil, "", errors.New("no PocketBase management credentials linked")
	}
	password, err := m.Secrets.Open("pb_superuser:"+inst.ID, inst.PBSuperuserSecret)
	if err != nil {
		return nil, "", errors.New("stored PocketBase credential could not be decrypted")
	}
	client := pocketbase.New(inst.Port)
	token, err := client.TestAuth(ctx, inst.PBSuperuserEmail, password)
	if err != nil {
		_ = m.Store.SetPBAuthState(ctx, inst.ID, inst.PBSuperuserEmail, inst.PBSuperuserSecret, "error", store.Now(), truncate(err.Error(), 500))
		return nil, "", err
	}
	return client, token, nil
}

func (m *Manager) nativeBackup(ctx context.Context, inst *store.Instance, client *pocketbase.Client, token, dest string, report Reporter) error {
	name := "pbmanager-" + store.NewID()[:12] + ".zip"
	report("Requesting PocketBase backup " + name)
	if err := client.CreateBackup(ctx, token, name); err != nil {
		return err
	}
	report("Waiting for PocketBase to finish the backup")
	info, err := client.WaitBackup(ctx, token, name, 5*time.Minute)
	if err != nil {
		return err
	}
	report(fmt.Sprintf("Downloading backup (%d bytes)", info.Size))
	fileToken, err := client.FileToken(ctx, token)
	if err != nil {
		return err
	}
	if err := client.DownloadBackup(ctx, token, fileToken, name, dest); err != nil {
		return err
	}
	if err := client.DeleteBackup(ctx, token, name); err != nil {
		report("Warning: could not remove the transient backup from PocketBase")
	}
	return nil
}

// packageNativeBundle turns PocketBase's database-only backup into a complete
// manager artifact that also contains uploads and relevant configuration.
func (m *Manager) packageNativeBundle(ctx context.Context, inst *store.Instance, nativeZip, dest string) error {
	tmp, err := os.MkdirTemp("", "pbmanager-native-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := ExtractPocketBaseZip(nativeZip, tmp); err != nil {
		return err
	}
	dbPath := filepath.Join(tmp, "data.db")
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("native backup contains no data.db: %w", err)
	}
	dbHash, _, err := SHA256File(dbPath)
	if err != nil {
		return err
	}
	entries := []TarEntry{{Name: "data/data.db", Source: dbPath, Mode: 0o600}}
	if aux := filepath.Join(tmp, "auxiliary.db"); fileExists(aux) {
		entries = append(entries, TarEntry{Name: "data/auxiliary.db", Source: aux, Mode: 0o600})
	}
	uploads := filepath.Join(inst.DataDir, "uploads")
	if info, err := os.Stat(uploads); err == nil && info.IsDir() {
		var files []TarEntry
		err := walkRegular(uploads, func(path, rel string, mode os.FileMode) error {
			files = append(files, TarEntry{Name: "data/uploads/" + filepath.ToSlash(rel), Source: path, Mode: mode})
			return nil
		})
		if err != nil {
			return err
		}
		entries = append(entries, files...)
	}
	if inst.ServiceFile != "" {
		if info, err := os.Stat(inst.ServiceFile); err == nil && info.Mode().IsRegular() {
			entries = append(entries, TarEntry{Name: "config/systemd.service", Source: inst.ServiceFile, Mode: 0o600})
		}
	}
	domains, _ := m.Store.ListDomainsByInstance(ctx, inst.ID)
	for _, d := range domains {
		if d.SnippetPath != "" && strings.HasSuffix(d.SnippetPath, ".caddy") {
			if info, err := os.Stat(d.SnippetPath); err == nil && info.Mode().IsRegular() {
				entries = append(entries, TarEntry{Name: "config/caddy/" + filepath.Base(d.SnippetPath), Source: d.SnippetPath, Mode: 0o600})
			}
		}
	}
	instanceJSON, _ := json.MarshalIndent(map[string]any{
		"id": inst.ID, "name": inst.Name, "hostname": inst.Hostname, "baseDir": inst.BaseDir,
		"dataDir": inst.DataDir, "binaryPath": inst.BinaryPath, "port": inst.Port,
		"serviceName": inst.ServiceName, "serviceFile": inst.ServiceFile, "pbVersion": inst.PBVersion,
	}, "", "  ")
	entries = append(entries, TarEntry{Name: "config/instance.json", Data: instanceJSON, Mode: 0o600})
	man := manifest{
		Format: FormatManager, InstanceID: inst.ID, InstanceName: inst.Name, CreatedAt: store.Now(),
		Method: MethodNative, PocketBase: inst.PBVersion, SourceData: inst.DataDir, DatabaseHash: dbHash,
		Note: "Database captured with PocketBase's official backup API; uploads and configuration added by pbmanager.",
	}
	manRaw, _ := json.MarshalIndent(man, "", "  ")
	entries = append(entries, TarEntry{Name: "manifest.json", Data: manRaw, Mode: 0o600})
	return WriteTarGz(dest, entries)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (m *Manager) snapshotBackup(ctx context.Context, inst *store.Instance, dest string) error {
	cfg := m.Cfg()
	tmp, err := os.MkdirTemp(cfg.BackupDir, ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	dataDB := filepath.Join(inst.DataDir, "data.db")
	if _, err := os.Stat(dataDB); err != nil {
		return fmt.Errorf("PocketBase database not found: %w", err)
	}
	snapDB := filepath.Join(tmp, "data.db")
	if err := snapshotSQLite(ctx, dataDB, snapDB); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	dbHash, _, err := SHA256File(snapDB)
	if err != nil {
		return err
	}
	entries := []TarEntry{{Name: "data/data.db", Source: snapDB, Mode: 0o600}}
	aux := filepath.Join(inst.DataDir, "auxiliary.db")
	if _, err := os.Stat(aux); err == nil {
		snapAux := filepath.Join(tmp, "auxiliary.db")
		if err := snapshotSQLite(ctx, aux, snapAux); err == nil {
			entries = append(entries, TarEntry{Name: "data/auxiliary.db", Source: snapAux, Mode: 0o600})
		}
	}
	uploads := filepath.Join(inst.DataDir, "uploads")
	if info, err := os.Stat(uploads); err == nil && info.IsDir() {
		var files []TarEntry
		err := walkRegular(uploads, func(path, rel string, mode os.FileMode) error {
			files = append(files, TarEntry{Name: "data/uploads/" + filepath.ToSlash(rel), Source: path, Mode: mode})
			return nil
		})
		if err != nil {
			return err
		}
		entries = append(entries, files...)
	}
	if inst.ServiceFile != "" {
		if info, err := os.Stat(inst.ServiceFile); err == nil && info.Mode().IsRegular() {
			entries = append(entries, TarEntry{Name: "config/systemd.service", Source: inst.ServiceFile, Mode: 0o600})
		}
	}
	domains, _ := m.Store.ListDomainsByInstance(ctx, inst.ID)
	for _, d := range domains {
		if d.SnippetPath != "" && strings.HasSuffix(d.SnippetPath, ".caddy") {
			if info, err := os.Stat(d.SnippetPath); err == nil && info.Mode().IsRegular() {
				entries = append(entries, TarEntry{Name: "config/caddy/" + filepath.Base(d.SnippetPath), Source: d.SnippetPath, Mode: 0o600})
			}
		}
	}
	instanceJSON, _ := json.MarshalIndent(map[string]any{
		"id": inst.ID, "name": inst.Name, "hostname": inst.Hostname, "baseDir": inst.BaseDir,
		"dataDir": inst.DataDir, "binaryPath": inst.BinaryPath, "port": inst.Port,
		"serviceName": inst.ServiceName, "serviceFile": inst.ServiceFile, "pbVersion": inst.PBVersion,
	}, "", "  ")
	entries = append(entries, TarEntry{Name: "config/instance.json", Data: instanceJSON, Mode: 0o600})
	man := manifest{
		Format: FormatManager, InstanceID: inst.ID, InstanceName: inst.Name, CreatedAt: store.Now(),
		Method: MethodSQLite, PocketBase: inst.PBVersion, SourceData: inst.DataDir, DatabaseHash: dbHash,
		Note: "SQLite consistent snapshot via VACUUM INTO; uploads and config copied alongside. Use PocketBase credentials for a fully native backup.",
	}
	manRaw, _ := json.MarshalIndent(man, "", "  ")
	entries = append(entries, TarEntry{Name: "manifest.json", Data: manRaw, Mode: 0o600})
	if err := WriteTarGz(dest, entries); err != nil {
		return err
	}
	return nil
}

func snapshotSQLite(ctx context.Context, src, dest string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		dsn := "file:" + src + "?_pragma=busy_timeout(15000)"
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return err
		}
		quoted := strings.ReplaceAll(dest, "'", "''")
		_, err = db.ExecContext(ctx, "VACUUM INTO '"+quoted+"'")
		db.Close()
		if err == nil {
			if err := os.Chmod(dest, 0o600); err != nil {
				return err
			}
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return fmt.Errorf("VACUUM INTO failed after retries: %w", lastErr)
}

func (m *Manager) finishBackup(ctx context.Context, b *store.Backup, verify func(context.Context) error) error {
	hash, size, err := SHA256File(b.Path)
	if err != nil {
		return err
	}
	b.SHA256 = hash
	b.Size = size
	if err := verify(ctx); err != nil {
		b.Status = "failed"
		b.Error = truncate("backup integrity verification failed: "+err.Error(), 2000)
		_ = m.Store.UpdateBackup(ctx, b)
		return err
	}
	b.Status = "verified"
	b.VerifiedAt = store.Now()
	if err := m.Store.UpdateBackup(ctx, b); err != nil {
		return err
	}
	return nil
}

// Verify re-checks size, checksum, and database integrity.
func (m *Manager) Verify(ctx context.Context, b *store.Backup) error {
	root := m.Cfg().BackupDir
	if !platform.IsWithin(root, b.Path) {
		return errors.New("backup path is outside the configured backup directory")
	}
	hash, size, err := SHA256File(b.Path)
	if err != nil {
		return err
	}
	if b.SHA256 != "" && hash != b.SHA256 {
		return errors.New("backup checksum does not match the recorded digest")
	}
	if b.Size > 0 && size != b.Size {
		return errors.New("backup size does not match the recorded size")
	}
	switch b.Method {
	case MethodNative:
		if strings.HasSuffix(b.Path, ".zip") {
			err = ValidateZipBackup(ctx, b.Path)
		} else {
			err = ValidateTarBackup(ctx, b.Path)
		}
	case MethodSQLite:
		err = ValidateTarBackup(ctx, b.Path)
	default:
		err = fmt.Errorf("unknown backup method %q", b.Method)
	}
	status := "verified"
	if err != nil {
		status = "failed"
		_ = m.Store.SetBackupVerified(ctx, b.ID, status, store.Now(), truncate(err.Error(), 2000))
		return err
	}
	return m.Store.SetBackupVerified(ctx, b.ID, status, store.Now(), "")
}

type restoreJournal struct {
	BackupID    string `json:"backupId"`
	InstanceID  string `json:"instanceId"`
	Instance    string `json:"instanceName"`
	DataDir     string `json:"dataDir"`
	OldPath     string `json:"oldPath"`
	ServiceName string `json:"serviceName"`
	RunAsUser   string `json:"runAsUser"`
	Phase       string `json:"phase"`
	CreatedAt   string `json:"createdAt"`
}

// Restore restores a backup. The caller must supply the exact instance name.
func (m *Manager) Restore(ctx context.Context, inst *store.Instance, b *store.Backup, confirmation string, report Reporter) error {
	if report == nil {
		report = func(string) {}
	}
	if confirmation != inst.Name {
		return errors.New("confirmation does not match the instance name")
	}
	if b.InstanceID != inst.ID {
		return errors.New("backup does not belong to this instance")
	}
	if b.Status != "completed" && b.Status != "verified" {
		return fmt.Errorf("backup status %q cannot be restored", b.Status)
	}
	if err := m.Verify(ctx, b); err != nil {
		return fmt.Errorf("backup verification failed; restore aborted: %w", err)
	}
	cfg := m.Cfg()
	if !platform.IsWithin(cfg.BackupDir, b.Path) {
		return errors.New("backup path is outside the configured backup directory")
	}
	if _, err := m.readRestoreJournal(inst.ID); err == nil {
		return errors.New("an interrupted restore exists for this instance; run startup recovery or the CLI recover command")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if inst.ServiceName == "" || !systemd.ValidServiceName(inst.ServiceName) {
		return errors.New("instance has no manageable systemd service")
	}
	if inst.DataDir == "" || inst.DataDir == inst.BaseDir || platform.IsWithin(inst.DataDir, inst.BaseDir) {
		return errors.New("refusing to restore into an unsafe data directory")
	}
	info, err := os.Lstat(inst.DataDir)
	if err != nil || !info.IsDir() {
		return errors.New("instance data directory does not exist")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("instance data directory must not be a symlink")
	}
	report("Creating a pre-restore safety backup")
	safety, err := m.Create(ctx, inst, false, "pre-restore safety backup for "+b.ID, report)
	if err != nil {
		return fmt.Errorf("could not create safety backup; restore aborted: %w", err)
	}
	report("Safety backup created: " + safety.ID)

	stamp := time.Now().UTC().Format("20060102T150405Z") + "-" + store.NewID()[:6]
	// The data directory itself is moved aside. Keep it inside the same parent.
	oldPath := inst.DataDir + ".pbmanager-pre-restore-" + stamp
	for i := 2; ; i++ {
		if _, err := os.Lstat(oldPath); errors.Is(err, os.ErrNotExist) {
			break
		}
		oldPath = fmt.Sprintf("%s.pbmanager-pre-restore-%s-%d", inst.DataDir, stamp, i)
	}
	journal := restoreJournal{
		BackupID: b.ID, InstanceID: inst.ID, Instance: inst.Name, DataDir: inst.DataDir,
		OldPath: oldPath, ServiceName: inst.ServiceName, RunAsUser: inst.RunAsUser,
		Phase: "prepared", CreatedAt: store.Now(),
	}
	if err := m.writeRestoreJournal(journal); err != nil {
		return err
	}
	rollback := func(cause error) error {
		report("Restore failed; rolling back to the previous data directory")
		_ = m.stopService(ctx, inst.ServiceName)
		if _, err := os.Lstat(inst.DataDir); err == nil {
			failed := inst.DataDir + ".pbmanager-failed-restore-" + stamp
			_ = os.Rename(inst.DataDir, failed)
		}
		if _, err := os.Lstat(oldPath); err == nil {
			_ = os.Rename(oldPath, inst.DataDir)
		}
		if inst.RunAsUser != "" {
			_ = m.Systemd.Chown(ctx, inst.DataDir, inst.RunAsUser)
		}
		_ = m.startService(ctx, inst.ServiceName)
		journal.Phase = "rolled-back"
		_ = m.writeRestoreJournal(journal)
		_ = os.Remove(m.journalPath(inst.ID))
		return fmt.Errorf("restore failed and was rolled back: %w", cause)
	}
	journal.Phase = "old-moved"
	if err := m.writeRestoreJournal(journal); err != nil {
		return err
	}
	report("Stopping " + inst.ServiceName)
	if err := m.stopService(ctx, inst.ServiceName); err != nil {
		_ = os.Remove(m.journalPath(inst.ID))
		return err
	}
	if err := m.waitStopped(ctx, inst.ServiceName, 30*time.Second); err != nil {
		_ = m.startService(ctx, inst.ServiceName)
		_ = os.Remove(m.journalPath(inst.ID))
		return err
	}
	if err := os.Rename(inst.DataDir, oldPath); err != nil {
		_ = m.startService(ctx, inst.ServiceName)
		_ = os.Remove(m.journalPath(inst.ID))
		return fmt.Errorf("move current data aside: %w", err)
	}
	journal.Phase = "staging"
	_ = m.writeRestoreJournal(journal)
	report("Extracting backup")
	staging := inst.DataDir + ".pbmanager-restore-tmp-" + stamp
	_ = os.RemoveAll(staging)
	if b.Method != MethodNative && b.Method != MethodSQLite {
		return rollback(fmt.Errorf("unsupported backup method %q", b.Method))
	}
	if b.Method == MethodNative && strings.HasSuffix(b.Path, ".zip") {
		// Legacy official PocketBase archive (database only).
		if err := ExtractPocketBaseZip(b.Path, staging); err != nil {
			return rollback(err)
		}
		if err := os.Rename(staging, inst.DataDir); err != nil {
			return rollback(err)
		}
	} else {
		if err := SafeExtractTarGz(b.Path, staging, ""); err != nil {
			return rollback(err)
		}
		dataDir := filepath.Join(staging, "data")
		if info, err := os.Stat(dataDir); err != nil || !info.IsDir() {
			return rollback(errors.New("backup archive has no data directory"))
		}
		if err := ValidateSQLite(ctx, filepath.Join(dataDir, "data.db")); err != nil {
			return rollback(err)
		}
		m.log().Debug("restore staging prepared", "path", dataDir)
		if err := os.Rename(dataDir, inst.DataDir); err != nil {
			return rollback(err)
		}
		m.log().Debug("restore staging activated", "path", inst.DataDir)
		_ = os.RemoveAll(staging)
	}
	if inst.RunAsUser != "" {
		if err := m.Systemd.Chown(ctx, inst.DataDir, inst.RunAsUser); err != nil {
			return rollback(err)
		}
	}
	journal.Phase = "extracted"
	_ = m.writeRestoreJournal(journal)
	report("Starting " + inst.ServiceName)
	if err := m.startService(ctx, inst.ServiceName); err != nil {
		return rollback(err)
	}
	if err := m.waitHealthy(ctx, inst.Port, 90*time.Second); err != nil {
		return rollback(err)
	}
	if err := m.Store.SetBackupRestored(ctx, b.ID, store.Now()); err != nil {
		return err
	}
	journal.Phase = "complete"
	_ = m.writeRestoreJournal(journal)
	_ = os.Remove(m.journalPath(inst.ID))
	report("Restore complete. Previous data retained at " + oldPath)
	return nil
}

func (m *Manager) stopService(ctx context.Context, service string) error {
	_, err := m.Systemd.Status(ctx, service)
	if err != nil {
		return err
	}
	if err := m.Systemd.Stop(ctx, service); err != nil {
		return err
	}
	return nil
}

func (m *Manager) startService(ctx context.Context, service string) error {
	return m.Systemd.Start(ctx, service)
}

func (m *Manager) waitStopped(ctx context.Context, service string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := m.Systemd.Status(ctx, service)
		if err == nil && !st.Running {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for service to stop")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (m *Manager) waitHealthy(ctx context.Context, port int, timeout time.Duration) error {
	return pocketbase.New(port).WaitHealthy(ctx, timeout)
}

func (m *Manager) journalPath(instanceID string) string {
	return filepath.Join(m.Cfg().BackupDir, "restores", sanitize(instanceID)+".json")
}

func (m *Manager) writeRestoreJournal(j restoreJournal) error {
	raw, _ := json.MarshalIndent(j, "", "  ")
	return platform.AtomicWrite(m.journalPath(j.InstanceID), raw, 0o600)
}

func (m *Manager) readRestoreJournal(instanceID string) (restoreJournal, error) {
	var j restoreJournal
	raw, err := os.ReadFile(m.journalPath(instanceID))
	if err != nil {
		return j, err
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return j, err
	}
	return j, nil
}

// RecoverInterrupted rolls back restores interrupted by a crash. It is called
// before the job worker starts.
func (m *Manager) RecoverInterrupted(ctx context.Context, report Reporter) error {
	if report == nil {
		report = func(string) {}
	}
	dir := filepath.Join(m.Cfg().BackupDir, "restores")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		var j restoreJournal
		if err := json.Unmarshal(raw, &j); err != nil {
			errs = append(errs, "invalid restore journal "+e.Name())
			continue
		}
		report("Recovering interrupted restore for " + j.Instance)
		if j.Phase == "complete" {
			_ = os.Remove(filepath.Join(dir, e.Name()))
			continue
		}
		if !systemd.ValidServiceName(j.ServiceName) || j.DataDir == "" || j.OldPath == "" {
			errs = append(errs, "restore journal is incomplete: "+e.Name())
			continue
		}
		if err := m.stopService(ctx, j.ServiceName); err != nil {
			// A stopped/failed service is expected during recovery.
			report("Service stop during recovery: " + truncate(err.Error(), 200))
		}
		_ = m.waitStopped(ctx, j.ServiceName, 20*time.Second)
		if _, err := os.Lstat(j.DataDir); err == nil {
			_ = os.Rename(j.DataDir, j.DataDir+".pbmanager-failed-recovery-"+time.Now().UTC().Format("20060102T150405Z"))
		}
		if _, err := os.Lstat(j.OldPath); err == nil {
			if err := os.Rename(j.OldPath, j.DataDir); err != nil {
				errs = append(errs, err.Error())
				continue
			}
		}
		if j.RunAsUser != "" {
			_ = m.Systemd.Chown(ctx, j.DataDir, j.RunAsUser)
		}
		if err := m.startService(ctx, j.ServiceName); err != nil {
			errs = append(errs, err.Error())
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
		report("Restored previous data for " + j.Instance)
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Prune keeps the newest retention completed backups for an instance.
func (m *Manager) Prune(ctx context.Context, instanceID string, retention int) (int, error) {
	if retention < 1 {
		retention = 1
	}
	list, err := m.Store.ListBackupsForRetention(ctx, instanceID)
	if err != nil {
		return 0, err
	}
	root := m.Cfg().BackupDir
	removed := 0
	for i, b := range list {
		if i < retention {
			continue
		}
		if platform.IsWithin(root, b.Path) {
			if err := os.Remove(b.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, err
			}
		}
		if err := m.Store.DeleteBackup(ctx, b.ID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

func walkRegular(root string, fn func(path, rel string, mode os.FileMode) error) error {
	count := 0
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		count++
		if count > 100000 {
			return errors.New("too many files in uploads directory")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return fn(path, rel, info.Mode().Perm())
	})
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "instance"
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}
