package api

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/apexo/pbmanager/internal/jobs"
)

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	instanceID := r.URL.Query().Get("instanceId")
	list, err := s.App.Store.ListBackups(r.Context(), instanceID, 300)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list backups", nil)
		return
	}
	bytes := int64(0)
	for _, b := range list {
		bytes += b.Size
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": len(list), "totalBytes": bytes})
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		InstanceID string `json:"instanceId"`
		Note       string `json:"note"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	inst, err := s.App.Store.GetInstance(r.Context(), in.InstanceID)
	if notFoundOr(w, r, err, "Instance") {
		return
	}
	if s.hasActiveInstanceJob(r.Context(), inst.ID) {
		writeError(w, r, http.StatusConflict, "job_in_progress", "Another operation is already running for this instance", nil)
		return
	}
	job, _, err := s.App.Jobs.Enqueue(r.Context(), "backup.create", struct {
		InstanceID string `json:"instanceId"`
		Automatic  bool   `json:"automatic"`
		Note       string `json:"note"`
	}{inst.ID, false, strings.TrimSpace(in.Note)}, jobs.EnqueueOptions{Priority: 4, MaxAttempts: 3, CreatedBy: adminFrom(r).ID})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "enqueue_failed", err.Error(), nil)
		return
	}
	s.audit(r, "backup.create_requested", "instance", inst.ID, map[string]any{"jobId": job.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) handleGetBackup(w http.ResponseWriter, r *http.Request) {
	b, err := s.App.Store.GetBackup(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Backup") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backup": b})
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	b, err := s.App.Store.GetBackup(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Backup") {
		return
	}
	root := s.App.Settings.Get().BackupDir
	clean := filepath.Clean(b.Path)
	if !strings.HasPrefix(clean, filepath.Clean(root)+string(os.PathSeparator)) {
		writeError(w, r, http.StatusForbidden, "unsafe_path", "Backup path is outside the managed directory", nil)
		return
	}
	lst, err := os.Lstat(clean)
	if err != nil || !lst.Mode().IsRegular() || lst.Mode()&os.ModeSymlink != 0 {
		writeError(w, r, http.StatusNotFound, "missing_file", "Backup file is not a regular file", nil)
		return
	}
	f, err := os.Open(clean)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "missing_file", "Backup file is no longer available", nil)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, r, http.StatusNotFound, "missing_file", "Backup file is not a regular file", nil)
		return
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": b.Filename})
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	s.audit(r, "backup.downloaded", "backup", b.ID, map[string]any{"instanceId": b.InstanceID})
	http.ServeContent(w, r, b.Filename, info.ModTime(), f)
}

func (s *Server) handleVerifyBackup(w http.ResponseWriter, r *http.Request) {
	b, err := s.App.Store.GetBackup(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Backup") {
		return
	}
	job, _, err := s.App.Jobs.Enqueue(r.Context(), "backup.verify", map[string]string{"backupId": b.ID}, jobs.EnqueueOptions{Priority: 2, MaxAttempts: 2, CreatedBy: adminFrom(r).ID})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "enqueue_failed", err.Error(), nil)
		return
	}
	s.audit(r, "backup.verify_requested", "backup", b.ID, map[string]any{"jobId": job.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	b, err := s.App.Store.GetBackup(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Backup") {
		return
	}
	inst, err := s.App.Store.GetInstance(r.Context(), b.InstanceID)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "instance_missing", "The source instance no longer exists", nil)
		return
	}
	var in struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if in.Confirmation != inst.Name {
		writeError(w, r, http.StatusBadRequest, "confirmation_mismatch", "Type the exact instance name to confirm restoration", nil)
		return
	}
	if s.hasActiveInstanceJob(r.Context(), inst.ID) {
		writeError(w, r, http.StatusConflict, "job_in_progress", "Another operation is already running for this instance", nil)
		return
	}
	if b.Status != "completed" && b.Status != "verified" {
		writeError(w, r, http.StatusBadRequest, "invalid_backup", "This backup is not in a restorable state", nil)
		return
	}
	job, _, err := s.App.Jobs.Enqueue(r.Context(), "backup.restore", struct {
		BackupID     string `json:"backupId"`
		Confirmation string `json:"confirmation"`
	}{b.ID, in.Confirmation}, jobs.EnqueueOptions{Priority: 30, MaxAttempts: 1, CreatedBy: adminFrom(r).ID})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "enqueue_failed", err.Error(), nil)
		return
	}
	s.audit(r, "backup.restore_requested", "instance", inst.ID, map[string]any{"backupId": b.ID, "jobId": job.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "warning": "A safety backup is created automatically. The instance is stopped during restoration."})
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	b, err := s.App.Store.GetBackup(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Backup") {
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		writeError(w, r, http.StatusBadRequest, "confirmation_required", "Pass confirm=true to delete a backup artifact", nil)
		return
	}
	root := s.App.Settings.Get().BackupDir
	clean := filepath.Clean(b.Path)
	if strings.HasPrefix(clean, filepath.Clean(root)+string(os.PathSeparator)) {
		if err := os.Remove(clean); err != nil && !errors.Is(err, os.ErrNotExist) {
			writeError(w, r, http.StatusInternalServerError, "delete_failed", err.Error(), nil)
			return
		}
	}
	if err := s.App.Store.DeleteBackup(r.Context(), b.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "delete_failed", err.Error(), nil)
		return
	}
	s.audit(r, "backup.deleted", "backup", b.ID, map[string]any{"instanceId": b.InstanceID})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	page, perPage, offset := parsePage(r)
	list, total, err := s.App.Store.ListJobs(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("type"), r.URL.Query().Get("q"), perPage, offset)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list jobs", nil)
		return
	}
	counts, _ := s.App.Store.CountJobsByStatus(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": total, "page": page, "perPage": perPage, "counts": counts})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.App.Store.GetJob(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Job") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": job})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if err := s.App.Jobs.Cancel(r.Context(), jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, "cancel_failed", err.Error(), nil)
		return
	}
	s.audit(r, "job.cancel_requested", "job", jobID, map[string]any{})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if err := s.App.Jobs.Retry(r.Context(), jobID); err != nil {
		writeError(w, r, http.StatusBadRequest, "retry_failed", err.Error(), nil)
		return
	}
	s.audit(r, "job.retry_requested", "job", jobID, map[string]any{})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}
