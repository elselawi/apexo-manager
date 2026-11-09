package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/apexo/pbmanager/internal/jobs"
	"github.com/apexo/pbmanager/internal/services/managed"
)

func (s *Server) enqueueManagedJob(w http.ResponseWriter, r *http.Request, jobType string) {
	instanceID := r.PathValue("id")
	if instanceID == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_instance", "Instance ID is required", nil)
		return
	}
	if _, err := s.App.Store.GetInstance(r.Context(), instanceID); err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "Instance not found", nil)
		return
	}
	if s.activeManagedJob(r, instanceID) {
		writeError(w, r, http.StatusConflict, "managed_job_active", "A managed credential operation is already queued or running.", nil)
		return
	}
	admin := adminFrom(r)
	job, _, err := s.App.Jobs.Enqueue(r.Context(), jobType, managed.Payload{InstanceID: instanceID}, jobs.EnqueueOptions{
		Priority: 30, MaxAttempts: 3, CreatedBy: "admin:" + admin.Username,
	})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "managed_job_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pb.managed_credentials_requested", "instance", instanceID, map[string]any{"jobType": jobType, "jobId": job.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) activeManagedJob(r *http.Request, instanceID string) bool {
	active, err := s.App.Store.ListActiveJobs(r.Context())
	if err != nil {
		return false
	}
	for _, j := range active {
		if !strings.HasPrefix(j.Type, "managed.") {
			continue
		}
		var p managed.Payload
		if json.Unmarshal([]byte(j.Payload), &p) == nil && p.InstanceID == instanceID {
			return true
		}
	}
	return false
}

func (s *Server) handleCreateManagedCredentials(w http.ResponseWriter, r *http.Request) {
	s.enqueueManagedJob(w, r, "managed.create")
}

func (s *Server) handleRepairManagedCredentials(w http.ResponseWriter, r *http.Request) {
	s.enqueueManagedJob(w, r, "managed.repair")
}

func (s *Server) handleDeleteManagedCredentials(w http.ResponseWriter, r *http.Request) {
	s.enqueueManagedJob(w, r, "managed.delete")
}
