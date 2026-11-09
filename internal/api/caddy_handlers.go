package api

import (
	"net/http"

	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/services/caddy"
)

func (s *Server) handleCaddy(w http.ResponseWriter, r *http.Request) {
	view, err := s.App.Caddy.ReadConfig(r.Context())
	if err != nil {
		status := s.App.Caddy.Status(r.Context())
		writeJSON(w, http.StatusOK, map[string]any{"status": status, "sites": []any{}, "imports": []any{}, "snippets": []any{}, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleCaddyEnsureImport(w http.ResponseWriter, r *http.Request) {
	if s.hasActiveJobType(r, "caddy.ensure-import") {
		writeError(w, r, http.StatusConflict, "job_in_progress", "A Caddy configuration job is already running", nil)
		return
	}
	job, _, err := s.App.Jobs.Enqueue(r.Context(), "caddy.ensure-import", map[string]any{}, jobs.EnqueueOptions{Priority: 8, MaxAttempts: 3, CreatedBy: adminFrom(r).ID})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "enqueue_failed", err.Error(), nil)
		return
	}
	s.audit(r, "caddy.import_requested", "caddy", "config", map[string]any{"jobId": job.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) handleCaddyValidate(w http.ResponseWriter, r *http.Request) {
	if err := s.App.Caddy.Validate(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error(), "checkedAt": nowString()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "checkedAt": nowString()})
}

func (s *Server) handleCaddyLogs(w http.ResponseWriter, r *http.Request) {
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		if n := parsePositiveInt(v); n > 0 {
			lines = n
		}
	}
	logs, err := s.App.Caddy.Logs(r.Context(), lines)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "logs_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "lines": lines})
}

func (s *Server) handleCaddyRemoveRoute(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	if !caddy.ValidDomain(domain) {
		writeError(w, r, http.StatusBadRequest, "invalid_domain", "A valid hostname is required.", nil)
		return
	}
	if err := s.App.Caddy.RemoveRoute(r.Context(), domain); err != nil {
		writeError(w, r, http.StatusConflict, "caddy_remove_failed", err.Error(), nil)
		return
	}
	s.audit(r, "caddy.route_removed", "caddy", domain, map[string]any{"domain": domain})
	writeJSON(w, http.StatusOK, map[string]any{"removed": true, "domain": domain})
}

func (s *Server) hasActiveJobType(r *http.Request, jobType string) bool {
	active, err := s.App.Store.ListActiveJobs(r.Context())
	if err != nil {
		return false
	}
	for _, j := range active {
		if j.Type == jobType {
			return true
		}
	}
	return false
}
