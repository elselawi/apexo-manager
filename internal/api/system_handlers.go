package api

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/netutil"
	"github.com/apexo/pbmanager/internal/services/monitor"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	instances, _ := s.App.Store.ListInstances(r.Context())
	metrics, _ := s.App.Store.LatestInstanceMetrics(r.Context())
	active, _ := s.App.Store.ListActiveJobs(r.Context())
	openJobs := map[string]string{}
	for _, j := range active {
		var p struct {
			InstanceID string `json:"instanceId"`
		}
		if jsonUnmarshal(j.Payload, &p) && p.InstanceID != "" {
			openJobs[p.InstanceID] = j.ID
		}
	}
	for i := range instances {
		if m, ok := metrics[instances[i].ID]; ok {
			instances[i].MemoryBytes = m.MemBytes
			instances[i].DiskBytes = m.DiskBytes
			instances[i].PortListening = m.PortListening
		}
		instances[i].OpenJobID = openJobs[instances[i].ID]
	}
	counts, _ := s.App.Store.InstanceCounts(r.Context())
	jobCounts, _ := s.App.Store.CountJobsByStatus(r.Context())
	backupCount, backupBytes, lastBackup, _ := s.App.Store.BackupStats(r.Context())
	latest, _ := s.App.Store.LatestServerMetrics(r.Context())
	serverInfo := monitor.ServerInfoFrom(s.App.Settings.Get(), s.App.Config.DataDir)
	failedJobs, _, _ := s.App.Store.ListJobs(r.Context(), "failed", "", "", 5, 0)
	recentJobs, _, _ := s.App.Store.ListJobs(r.Context(), "", "", "", 8, 0)
	recentAudit, _ := s.App.Store.RecentAudit(r.Context(), 8)
	alerts := buildAlerts(instances, jobCounts, lastBackup, r)
	writeJSON(w, http.StatusOK, map[string]any{
		"instances": instances, "instanceCounts": counts, "jobCounts": jobCounts,
		"backups": map[string]any{"count": backupCount, "bytes": backupBytes, "lastAt": lastBackup},
		"server":  serverInfo, "serverMetrics": latest, "activeJobs": active,
		"failedJobs": failedJobs, "recentJobs": recentJobs, "recentAudit": recentAudit,
		"alerts": alerts, "generatedAt": store.Now(),
	})
}

type alert struct {
	Level   string `json:"level"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Target  string `json:"target,omitempty"`
}

func buildAlerts(instances []store.Instance, jobCounts map[string]int, lastBackup string, r *http.Request) []alert {
	var out []alert
	for _, inst := range instances {
		if inst.Status == "failed" {
			out = append(out, alert{Level: "critical", Title: "Instance failed", Message: inst.Name + " is in a failed state", Target: "/instances/" + inst.ID})
		}
		if inst.Status == "running" && inst.Health == "unhealthy" {
			out = append(out, alert{Level: "warning", Title: "Health check failing", Message: inst.Name + " is running but not answering its health endpoint", Target: "/instances/" + inst.ID})
		}
		if inst.BackupEnabled && inst.NextBackupAt != "" {
			if next := store.ParseTime(inst.NextBackupAt); !next.IsZero() && next.Before(time.Now().Add(-2*time.Hour)) {
				out = append(out, alert{Level: "warning", Title: "Backup overdue", Message: inst.Name + " has an overdue scheduled backup", Target: "/instances/" + inst.ID})
			}
		}
	}
	if jobCounts["failed"] > 0 {
		out = append(out, alert{Level: "warning", Title: "Failed background jobs", Message: "One or more background operations have failed", Target: "/jobs?status=failed"})
	}
	if lastBackup == "" {
		out = append(out, alert{Level: "info", Title: "No verified backups yet", Message: "Create a backup for at least one instance", Target: "/backups"})
	}
	return out
}

func (s *Server) handleMonitoring(w http.ResponseWriter, r *http.Request) {
	serverMetrics, _ := s.App.Store.ListServerMetrics(r.Context(), 240)
	latest, _ := s.App.Store.LatestServerMetrics(r.Context())
	instances, _ := s.App.Store.ListInstances(r.Context())
	latestInst, _ := s.App.Store.LatestInstanceMetrics(r.Context())
	metricsByInstance := map[string][]store.InstanceMetrics{}
	for _, inst := range instances {
		m, _ := s.App.Store.ListInstanceMetrics(r.Context(), inst.ID, 60)
		metricsByInstance[inst.ID] = m
	}
	caddyStatus := s.App.Caddy.Status(r.Context())
	jobCounts, _ := s.App.Store.CountJobsByStatus(r.Context())
	backupCount, backupBytes, lastBackup, _ := s.App.Store.BackupStats(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"server":        monitor.ServerInfoFrom(s.App.Settings.Get(), s.App.Config.DataDir),
		"serverMetrics": serverMetrics, "latest": latest,
		"instances": instances, "instanceMetrics": metricsByInstance, "latestInstanceMetrics": latestInst,
		"caddy": caddyStatus, "jobCounts": jobCounts,
		"backups":     map[string]any{"count": backupCount, "bytes": backupBytes, "lastAt": lastBackup},
		"generatedAt": store.Now(),
	})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.App.Settings.View())
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in settings.Config
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	view, changed, err := s.App.Settings.Update(r.Context(), in)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "settings_invalid", err.Error(), nil)
		return
	}
	s.App.Systemd.SystemctlBinary = view.Config.SystemctlBinary
	s.App.Systemd.JournalctlBinary = view.Config.JournalctlBinary
	s.audit(r, "settings.updated", "server", "settings", map[string]any{"changed": changed})
	writeJSON(w, http.StatusOK, map[string]any{"settings": view, "changed": changed})
}

func (s *Server) handleDetectIP(w http.ResponseWriter, r *http.Request) {
	ip, err := netutil.DetectPublicIPv4(r.Context())
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "detect_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"publicIpv4": ip})
}

func (s *Server) handleListAdmins(w http.ResponseWriter, r *http.Request) {
	list, err := s.App.Auth.ListAdmins(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list administrators", nil)
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	out := list[:0]
	for _, a := range list {
		if q != "" && !strings.Contains(strings.ToLower(a.Username+" "+a.DisplayName), q) {
			continue
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

func (s *Server) handleCreateAdmin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
		Role        string `json:"role"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if in.Role == "" {
		in.Role = "admin"
	}
	a, err := s.App.Auth.CreateAdmin(r.Context(), in.Username, in.DisplayName, in.Password, in.Role)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "create_failed", err.Error(), nil)
		return
	}
	s.audit(r, "admin.created", "admin", a.ID, map[string]any{"username": a.Username, "role": a.Role})
	writeJSON(w, http.StatusCreated, map[string]any{"admin": a})
}

func (s *Server) handleUpdateAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target, err := s.App.Auth.AdminByID(r.Context(), id)
	if notFoundOr(w, r, err, "Administrator") {
		return
	}
	var in struct {
		DisplayName string `json:"displayName"`
		Role        string `json:"role"`
		Disabled    bool   `json:"disabled"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	self := adminFrom(r)
	if self.ID == target.ID && in.Disabled {
		writeError(w, r, http.StatusBadRequest, "self_disable", "You cannot disable your own account", nil)
		return
	}
	if in.Role == "" {
		in.Role = target.Role
	}
	if in.DisplayName == "" {
		in.DisplayName = target.DisplayName
	}
	if err := s.App.Auth.UpdateAdmin(r.Context(), id, in.DisplayName, in.Role, in.Disabled); err != nil {
		writeError(w, r, http.StatusBadRequest, "update_failed", err.Error(), nil)
		return
	}
	s.audit(r, "admin.updated", "admin", id, map[string]any{"role": in.Role, "disabled": in.Disabled})
	updated, _ := s.App.Auth.AdminByID(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"admin": updated})
}

func (s *Server) handleDeleteAdmin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if adminFrom(r).ID == id {
		writeError(w, r, http.StatusBadRequest, "self_delete", "You cannot delete your own account", nil)
		return
	}
	if err := s.App.Auth.DeleteAdmin(r.Context(), id); err != nil {
		writeError(w, r, http.StatusBadRequest, "delete_failed", err.Error(), nil)
		return
	}
	s.audit(r, "admin.deleted", "admin", id, map[string]any{})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRevokeAdminSessions(w http.ResponseWriter, r *http.Request) {
	if err := s.App.Auth.RevokeAdminSessions(r.Context(), r.PathValue("id"), ""); err != nil {
		writeError(w, r, http.StatusInternalServerError, "revoke_failed", err.Error(), nil)
		return
	}
	s.audit(r, "admin.sessions_revoked", "admin", r.PathValue("id"), map[string]any{})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	page, perPage, offset := parsePage(r)
	list, total, err := s.App.Store.ListAudit(r.Context(), r.URL.Query().Get("action"), r.URL.Query().Get("actor"), r.URL.Query().Get("q"), perPage, offset)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list audit entries", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": total, "page": page, "perPage": perPage})
}

func sortedInstanceNames(list []store.Instance) []string {
	names := make([]string, len(list))
	for i := range list {
		names[i] = list[i].Name
	}
	sort.Strings(names)
	return names
}
