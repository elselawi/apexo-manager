package api

import (
	"context"
	"net/http"
	"time"

	"github.com/apexo/pbmanager/internal/jobs"
)

func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	info, err := s.App.Update.Check(ctx, s.App.Version)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "update_check_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleInstallUpdate(w http.ResponseWriter, r *http.Request) {
	if s.hasActiveJobType(r, "manager.update") {
		writeError(w, r, http.StatusConflict, "update_in_progress", "A manager update is already queued or running.", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	info, err := s.App.Update.Check(ctx, s.App.Version)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "update_check_failed", err.Error(), nil)
		return
	}
	if !info.Available {
		writeError(w, r, http.StatusConflict, "no_update", info.Reason, nil)
		return
	}
	admin := adminFrom(r)
	job, _, err := s.App.Jobs.Enqueue(r.Context(), "manager.update", map[string]string{"version": info.Latest}, jobs.EnqueueOptions{
		Priority: 50, MaxAttempts: 2, CreatedBy: "admin:" + admin.Username,
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "update_enqueue_failed", err.Error(), nil)
		return
	}
	s.audit(r, "manager.update_requested", "manager", info.Latest, map[string]any{
		"current": info.Current, "latest": info.Latest, "asset": info.AssetName, "sha256": info.SHA256,
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job, "update": info})
}
