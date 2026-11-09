package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/netutil"
	"github.com/apexo/pbmanager/internal/services/instances"
	"github.com/apexo/pbmanager/internal/services/pocketbase"
	"github.com/apexo/pbmanager/internal/store"
)

func (s *Server) handleListInstances(w http.ResponseWriter, r *http.Request) {
	all, err := s.App.Store.ListInstances(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list instances", nil)
		return
	}
	metrics, _ := s.App.Store.LatestInstanceMetrics(r.Context())
	active, _ := s.App.Store.ListActiveJobs(r.Context())
	openJobs := map[string]string{}
	for _, j := range active {
		var payload struct {
			InstanceID string `json:"instanceId"`
		}
		if json.Unmarshal([]byte(j.Payload), &payload) == nil && payload.InstanceID != "" {
			openJobs[payload.InstanceID] = j.ID
		}
	}
	statusFilter := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	filtered := make([]store.Instance, 0, len(all))
	for _, inst := range all {
		if statusFilter != "" && inst.Status != statusFilter {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(inst.Name+" "+inst.Hostname+" "+inst.Notes), query) {
			continue
		}
		if m, ok := metrics[inst.ID]; ok {
			inst.MemoryBytes = m.MemBytes
			inst.DiskBytes = m.DiskBytes
			inst.CPUPercent = m.CPUPercent
			inst.PortListening = m.PortListening
		}
		inst.OpenJobID = openJobs[inst.ID]
		filtered = append(filtered, inst)
	}
	page, perPage, offset := parsePage(r)
	if offset > len(filtered) {
		offset = len(filtered)
	}
	end := offset + perPage
	if end > len(filtered) {
		end = len(filtered)
	}
	writeJSON(w, http.StatusOK, store.Page[store.Instance]{Items: filtered[offset:end], Total: len(filtered), Page: page, PerPage: perPage})
}

func (s *Server) handleInstanceDefaults(w http.ResponseWriter, r *http.Request) {
	defaults, err := s.App.Instances.Defaults(r.Context(), r.URL.Query().Get("name"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "defaults_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, defaults)
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	found, warnings, err := s.App.Discovery.Discover(ctx)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "discovery_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": found, "warnings": warnings})
}

func (s *Server) handleCreateInstance(w http.ResponseWriter, r *http.Request) {
	var in instances.CreateInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	a := adminFrom(r)
	inst, job, err := s.App.Instances.Create(r.Context(), in, a.Username)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "provision_failed", err.Error(), nil)
		return
	}
	s.audit(r, "instance.provision_requested", "instance", inst.ID, map[string]any{
		"name": inst.Name, "version": inst.PBVersion, "port": inst.Port, "jobId": job.ID,
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"instance": inst, "job": job})
}

func (s *Server) handleImportInstance(w http.ResponseWriter, r *http.Request) {
	var in instances.ImportInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	a := adminFrom(r)
	inst, err := s.App.Instances.Import(r.Context(), in, a.Username)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "import_failed", err.Error(), nil)
		return
	}
	s.audit(r, "instance.imported", "instance", inst.ID, map[string]any{"name": inst.Name, "service": inst.ServiceName})
	writeJSON(w, http.StatusCreated, map[string]any{"instance": inst})
}

func (s *Server) handleGetInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inst, err := s.App.Store.GetInstance(r.Context(), id)
	if notFoundOr(w, r, err, "Instance") {
		return
	}
	domains, _ := s.App.Store.ListDomainsByInstance(r.Context(), id)
	backups, _ := s.App.Store.ListBackups(r.Context(), id, 10)
	active, _ := s.App.Store.ListActiveJobs(r.Context())
	activeJobs := []store.Job{}
	for _, j := range active {
		var p struct {
			InstanceID string `json:"instanceId"`
		}
		if json.Unmarshal([]byte(j.Payload), &p) == nil && p.InstanceID == id {
			activeJobs = append(activeJobs, j)
		}
	}
	latest, _ := s.App.Store.LatestInstanceMetrics(r.Context())
	metrics, _ := s.App.Store.ListInstanceMetrics(r.Context(), id, 120)
	var current any
	if m, ok := latest[id]; ok {
		current = m
	}
	url := ""
	for _, d := range domains {
		if d.TLSStatus == "valid" || d.TLSStatus == "expiring" {
			url = "https://" + d.Hostname
			break
		}
	}
	if url == "" {
		url = fmt.Sprintf("http://127.0.0.1:%d", inst.Port)
	}
	_, serviceErr := os.Stat(inst.ServiceFile)
	writeJSON(w, http.StatusOK, map[string]any{
		"instance": inst, "domains": domains, "backups": backups, "jobs": activeJobs,
		"metrics": metrics, "currentMetrics": current, "url": url,
		"capabilities": map[string]any{
			"systemd":        inst.ServiceName != "" && serviceErr == nil,
			"caddy":          s.App.Caddy != nil,
			"dns":            inst.CloudflareAccountID != "",
			"pocketbaseAuth": inst.PBAuthStatus == "linked",
		},
	})
}

func (s *Server) handleUpdateInstance(w http.ResponseWriter, r *http.Request) {
	var in instances.SettingsInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	inst, err := s.App.Instances.UpdateSettings(r.Context(), r.PathValue("id"), in)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, r, http.StatusNotFound, "not_found", "Instance not found", nil)
			return
		}
		writeError(w, r, http.StatusBadRequest, "update_failed", err.Error(), nil)
		return
	}
	s.audit(r, "instance.settings_updated", "instance", inst.ID, map[string]any{"backupEnabled": inst.BackupEnabled, "backupCron": inst.BackupCron, "smtpProfileId": inst.SMTPProfileID, "storageProfileId": inst.StorageProfileID})
	writeJSON(w, http.StatusOK, map[string]any{"instance": inst})
}

type deleteInstanceInput struct {
	Confirmation string `json:"confirmation"`
	PurgeData    bool   `json:"purgeData"`
	RemoveCaddy  bool   `json:"removeCaddy"`
	RemoveDNS    bool   `json:"removeDns"`
}

func (s *Server) handleDeleteInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.App.Store.GetInstance(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Instance") {
		return
	}
	var in deleteInstanceInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if in.Confirmation != inst.Name {
		writeError(w, r, http.StatusBadRequest, "confirmation_mismatch", "Type the exact instance name to confirm", nil)
		return
	}
	if s.hasActiveInstanceJob(r.Context(), inst.ID) {
		writeError(w, r, http.StatusConflict, "job_in_progress", "Another operation is already running for this instance", nil)
		return
	}
	job, _, err := s.App.Jobs.Enqueue(r.Context(), "instance.delete", instances.DeletePayload{
		InstanceID: inst.ID, Confirmation: in.Confirmation, PurgeData: in.PurgeData, RemoveCaddy: in.RemoveCaddy, RemoveDNS: in.RemoveDNS,
	}, jobs.EnqueueOptions{Priority: 20, MaxAttempts: 2, CreatedBy: adminFrom(r).ID})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "enqueue_failed", err.Error(), nil)
		return
	}
	s.audit(r, "instance.delete_requested", "instance", inst.ID, map[string]any{"purgeData": in.PurgeData, "removeCaddy": in.RemoveCaddy, "removeDns": in.RemoveDNS, "jobId": job.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) handleStartInstance(w http.ResponseWriter, r *http.Request) {
	s.enqueueInstanceAction(w, r, "instance.start", "start", 5)
}
func (s *Server) handleStopInstance(w http.ResponseWriter, r *http.Request) {
	s.enqueueInstanceAction(w, r, "instance.stop", "stop", 5)
}
func (s *Server) handleRestartInstance(w http.ResponseWriter, r *http.Request) {
	s.enqueueInstanceAction(w, r, "instance.restart", "restart", 5)
}
func (s *Server) handleSyncCaddy(w http.ResponseWriter, r *http.Request) {
	s.enqueueInstanceAction(w, r, "instance.sync-caddy", "sync-caddy", 0)
}
func (s *Server) handleApplySMTP(w http.ResponseWriter, r *http.Request) {
	s.enqueueInstanceAction(w, r, "instance.apply-smtp", "apply-smtp", 0)
}
func (s *Server) handleApplyStorage(w http.ResponseWriter, r *http.Request) {
	s.enqueueInstanceAction(w, r, "instance.apply-storage", "apply-storage", 0)
}

func (s *Server) enqueueInstanceAction(w http.ResponseWriter, r *http.Request, jobType, action string, maxAttempts int) {
	inst, err := s.App.Store.GetInstance(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Instance") {
		return
	}
	if s.hasActiveInstanceJob(r.Context(), inst.ID) {
		writeError(w, r, http.StatusConflict, "job_in_progress", "Another operation is already running for this instance", nil)
		return
	}
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	job, _, err := s.App.Jobs.Enqueue(r.Context(), jobType, instances.ActionPayload{InstanceID: inst.ID}, jobs.EnqueueOptions{
		Priority: 5, MaxAttempts: maxAttempts, CreatedBy: adminFrom(r).ID,
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "enqueue_failed", err.Error(), nil)
		return
	}
	s.audit(r, "instance."+action, "instance", inst.ID, map[string]any{"jobId": job.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) handleUpdatePocketBase(w http.ResponseWriter, r *http.Request) {
	inst, err := s.App.Store.GetInstance(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Instance") {
		return
	}
	var in struct {
		Version  string `json:"version"`
		Checksum string `json:"checksum"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if strings.TrimSpace(in.Version) == "" {
		writeError(w, r, http.StatusBadRequest, "version_required", "A target version is required", nil)
		return
	}
	if s.hasActiveInstanceJob(r.Context(), inst.ID) {
		writeError(w, r, http.StatusConflict, "job_in_progress", "Another operation is already running for this instance", nil)
		return
	}
	job, _, err := s.App.Jobs.Enqueue(r.Context(), "instance.update", instances.UpdatePayload{
		InstanceID: inst.ID, Version: strings.TrimSpace(in.Version), Checksum: strings.TrimSpace(in.Checksum),
	}, jobs.EnqueueOptions{Priority: 10, MaxAttempts: 3, CreatedBy: adminFrom(r).ID})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "enqueue_failed", err.Error(), nil)
		return
	}
	s.audit(r, "instance.update_requested", "instance", inst.ID, map[string]any{"from": inst.PBVersion, "to": in.Version, "jobId": job.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) handleVerifyInstance(w http.ResponseWriter, r *http.Request) {
	inst, err := s.App.Store.GetInstance(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Instance") {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	sysStatus, sysErr := s.App.Systemd.Status(ctx, inst.ServiceName)
	health := "unknown"
	var healthErr string
	if sysStatus.Running {
		err := pocketbase.New(inst.Port).Health(ctx)
		if err == nil {
			health = "healthy"
		} else {
			health = "unhealthy"
			healthErr = err.Error()
		}
	}
	listening := netutil.PortListening(ctx, inst.Port)
	m, sampleErr := s.App.Monitor.SampleInstance(ctx, inst)
	status := "unknown"
	if sysErr != nil {
		status = "unknown"
	} else if !sysStatus.Exists {
		status = "not-found"
	} else if sysStatus.Running {
		status = "running"
	} else if sysStatus.ActiveState == "failed" {
		status = "failed"
	} else {
		status = "stopped"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": status, "health": health, "healthError": healthErr, "portListening": listening,
		"systemd": sysStatus, "systemdError": errorString(sysErr), "metrics": m, "sampleError": errorString(sampleErr),
		"checkedAt": store.Now(),
	})
}

func (s *Server) handleInstanceLogs(w http.ResponseWriter, r *http.Request) {
	inst, err := s.App.Store.GetInstance(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Instance") {
		return
	}
	if inst.ServiceName == "" {
		writeError(w, r, http.StatusBadRequest, "no_service", "No systemd service is configured for this instance", nil)
		return
	}
	lines := 200
	if v := r.URL.Query().Get("lines"); v != "" {
		fmt.Sscanf(v, "%d", &lines)
	}
	logs, err := s.App.Systemd.Logs(r.Context(), inst.ServiceName, lines)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "logs_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": logs, "service": inst.ServiceName, "lines": lines})
}

func (s *Server) handleInstanceMetrics(w http.ResponseWriter, r *http.Request) {
	if _, err := s.App.Store.GetInstance(r.Context(), r.PathValue("id")); notFoundOr(w, r, err, "Instance") {
		return
	}
	metrics, err := s.App.Store.ListInstanceMetrics(r.Context(), r.PathValue("id"), 240)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not load metrics", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": metrics})
}

func (s *Server) handleSetCredentials(w http.ResponseWriter, r *http.Request) {
	var in instances.CredentialInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if err := s.App.Instances.SetCredentials(r.Context(), r.PathValue("id"), in); err != nil {
		writeError(w, r, http.StatusBadRequest, "credentials_failed", err.Error(), nil)
		return
	}
	s.audit(r, "instance.credentials_linked", "instance", r.PathValue("id"), map[string]any{"email": strings.ToLower(in.Email)})
	writeJSON(w, http.StatusOK, map[string]any{"status": "linked", "email": strings.ToLower(in.Email)})
}

func (s *Server) handleClearCredentials(w http.ResponseWriter, r *http.Request) {
	if err := s.App.Instances.ClearCredentials(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, r, http.StatusBadRequest, "credentials_failed", err.Error(), nil)
		return
	}
	s.audit(r, "instance.credentials_unlinked", "instance", r.PathValue("id"), map[string]any{})
	writeJSON(w, http.StatusOK, map[string]any{"status": "unlinked"})
}

func (s *Server) handleTestCredentials(w http.ResponseWriter, r *http.Request) {
	if err := s.App.Instances.TestCredentials(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleInstanceBackups(w http.ResponseWriter, r *http.Request) {
	if _, err := s.App.Store.GetInstance(r.Context(), r.PathValue("id")); notFoundOr(w, r, err, "Instance") {
		return
	}
	list, err := s.App.Store.ListBackups(r.Context(), r.PathValue("id"), 200)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list backups", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) hasActiveInstanceJob(ctx context.Context, instanceID string) bool {
	active, err := s.App.Store.ListActiveJobs(ctx)
	if err != nil {
		return false
	}
	for _, j := range active {
		var p struct {
			InstanceID string `json:"instanceId"`
		}
		if json.Unmarshal([]byte(j.Payload), &p) == nil && p.InstanceID == instanceID {
			return true
		}
	}
	return false
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func sortInstances(list []store.Instance) {
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
}

var _ = sortInstances
