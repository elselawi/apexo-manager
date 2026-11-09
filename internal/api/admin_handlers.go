package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/apexo/pbmanager/internal/services/pbadmin"
)

func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.App.PBAdmin.ReadSettings(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusConflict, "settings_unavailable", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

func (s *Server) handleAdminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in pbadmin.Settings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "Invalid settings payload", nil)
		return
	}
	settings, err := s.App.PBAdmin.UpdateSettings(r.Context(), r.PathValue("id"), in)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "settings_update_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pocketbase.settings_updated", "instance", r.PathValue("id"), map[string]any{"sections": []string{"meta", "smtp", "s3", "backups", "rateLimits", "trustedProxy", "logs", "batch"}})
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

func (s *Server) handleAdminListBackups(w http.ResponseWriter, r *http.Request) {
	items, err := s.App.PBAdmin.ListBackups(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusConflict, "backups_unavailable", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAdminCreateBackup(w http.ResponseWriter, r *http.Request) {
	item, err := s.App.PBAdmin.CreateBackup(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "backup_create_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pocketbase.backup_created", "instance", r.PathValue("id"), map[string]any{"key": item.Key})
	writeJSON(w, http.StatusCreated, map[string]any{"backup": item})
}

func (s *Server) handleAdminDownloadBackup(w http.ResponseWriter, r *http.Request) {
	path, cleanup, err := s.App.PBAdmin.DownloadBackup(r.Context(), r.PathValue("id"), r.PathValue("key"))
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "backup_download_failed", err.Error(), nil)
		return
	}
	defer cleanup()
	s.audit(r, "pocketbase.backup_downloaded", "instance", r.PathValue("id"), map[string]any{"key": r.PathValue("key")})
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
	http.ServeFile(w, r, path)
}

func (s *Server) handleAdminDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("confirm") != "true" {
		writeError(w, r, http.StatusBadRequest, "confirmation_required", "Pass confirm=true to delete a PocketBase backup", nil)
		return
	}
	key := r.PathValue("key")
	if err := s.App.PBAdmin.DeleteBackup(r.Context(), r.PathValue("id"), key); err != nil {
		writeError(w, r, http.StatusBadGateway, "backup_delete_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pocketbase.backup_deleted", "instance", r.PathValue("id"), map[string]any{"key": key})
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func (s *Server) handleAdminRestoreBackup(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("confirm") != "true" {
		writeError(w, r, http.StatusBadRequest, "confirmation_required", "Pass confirm=true to restore a PocketBase backup", nil)
		return
	}
	key := r.PathValue("key")
	if err := s.App.PBAdmin.RestoreBackup(r.Context(), r.PathValue("id"), key); err != nil {
		writeError(w, r, http.StatusBadGateway, "backup_restore_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pocketbase.backup_restored", "instance", r.PathValue("id"), map[string]any{"key": key})
	writeJSON(w, http.StatusOK, map[string]any{"status": "restored"})
}

func (s *Server) handleAdminListSuperusers(w http.ResponseWriter, r *http.Request) {
	items, err := s.App.PBAdmin.ListSuperusers(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusConflict, "superusers_unavailable", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAdminCreateSuperuser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "Invalid superuser payload", nil)
		return
	}
	if err := s.App.PBAdmin.CreateSuperuser(r.Context(), r.PathValue("id"), in.Email, in.Password); err != nil {
		writeError(w, r, http.StatusBadRequest, "superuser_create_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pocketbase.superuser_created", "instance", r.PathValue("id"), map[string]any{"email": in.Email})
	writeJSON(w, http.StatusCreated, map[string]any{"status": "created"})
}

func (s *Server) handleAdminUpdateSuperuser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "Invalid superuser payload", nil)
		return
	}
	if err := s.App.PBAdmin.UpdateSuperuser(r.Context(), r.PathValue("id"), r.PathValue("recordId"), in.Email, in.Password); err != nil {
		writeError(w, r, http.StatusBadRequest, "superuser_update_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pocketbase.superuser_updated", "instance", r.PathValue("id"), map[string]any{"recordId": r.PathValue("recordId"), "email": in.Email})
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated"})
}

func (s *Server) handleAdminDeleteSuperuser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirmation string `json:"confirmation"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in)
	if err := s.App.PBAdmin.DeleteSuperuser(r.Context(), r.PathValue("id"), r.PathValue("recordId"), in.Confirmation); err != nil {
		writeError(w, r, http.StatusBadRequest, "superuser_delete_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pocketbase.superuser_deleted", "instance", r.PathValue("id"), map[string]any{"recordId": r.PathValue("recordId")})
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func (s *Server) handleAdminAuthCollections(w http.ResponseWriter, r *http.Request) {
	items, err := s.App.PBAdmin.ListAuthCollections(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusConflict, "auth_collections_unavailable", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAdminListAuthUsers(w http.ResponseWriter, r *http.Request) {
	items, err := s.App.PBAdmin.ListAuthUsers(r.Context(), r.PathValue("id"), r.PathValue("collectionId"))
	if err != nil {
		writeError(w, r, http.StatusConflict, "auth_users_unavailable", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAdminCreateAuthUser(w http.ResponseWriter, r *http.Request) {
	data, err := decodeUserPayload(w, r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	record, err := s.App.PBAdmin.CreateAuthUser(r.Context(), r.PathValue("id"), r.PathValue("collectionId"), data)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "auth_user_create_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pocketbase.auth_user_created", "instance", r.PathValue("id"), map[string]any{"collectionId": r.PathValue("collectionId"), "email": data["email"]})
	writeJSON(w, http.StatusCreated, map[string]any{"record": record})
}

func (s *Server) handleAdminUpdateAuthUser(w http.ResponseWriter, r *http.Request) {
	data, err := decodeUserPayload(w, r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", err.Error(), nil)
		return
	}
	record, err := s.App.PBAdmin.UpdateAuthUser(r.Context(), r.PathValue("id"), r.PathValue("collectionId"), r.PathValue("recordId"), data)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "auth_user_update_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pocketbase.auth_user_updated", "instance", r.PathValue("id"), map[string]any{"collectionId": r.PathValue("collectionId"), "recordId": r.PathValue("recordId")})
	writeJSON(w, http.StatusOK, map[string]any{"record": record})
}

func (s *Server) handleAdminDeleteAuthUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Confirmation string `json:"confirmation"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in)
	if err := s.App.PBAdmin.DeleteAuthUser(r.Context(), r.PathValue("id"), r.PathValue("collectionId"), r.PathValue("recordId"), in.Confirmation); err != nil {
		writeError(w, r, http.StatusBadRequest, "auth_user_delete_failed", err.Error(), nil)
		return
	}
	s.audit(r, "pocketbase.auth_user_deleted", "instance", r.PathValue("id"), map[string]any{"collectionId": r.PathValue("collectionId"), "recordId": r.PathValue("recordId")})
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

func decodeUserPayload(w http.ResponseWriter, r *http.Request) (map[string]interface{}, error) {
	var raw map[string]interface{}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	for _, key := range []string{"email", "username", "name", "password", "verified"} {
		if value, ok := raw[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}
