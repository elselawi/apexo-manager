package api

import (
	"net/http"
	"net/mail"
	"net/url"
	"strings"

	smtpsvc "github.com/apexo/pbmanager/internal/services/smtp"
	"github.com/apexo/pbmanager/internal/services/storage"
	"github.com/apexo/pbmanager/internal/store"
)

func validateSMTPInput(host string, port int, authMethod, tlsMode, from string) error {
	if strings.TrimSpace(host) == "" {
		return errString("SMTP host is required")
	}
	if port < 1 || port > 65535 {
		return errString("SMTP port must be between 1 and 65535")
	}
	if authMethod == "" {
		authMethod = "PLAIN"
	}
	if authMethod != "PLAIN" && authMethod != "LOGIN" {
		return errString("authentication method must be PLAIN or LOGIN")
	}
	if tlsMode == "" {
		tlsMode = "starttls"
	}
	if tlsMode != "starttls" && tlsMode != "tls" && tlsMode != "none" {
		return errString("TLS mode must be starttls, tls, or none")
	}
	if from != "" {
		if _, err := mail.ParseAddress(from); err != nil {
			return errString("sender address is not a valid email address")
		}
	}
	return nil
}

type errString string

func (e errString) Error() string { return string(e) }

func (s *Server) handleListSMTP(w http.ResponseWriter, r *http.Request) {
	list, err := s.App.Store.ListSMTPProfiles(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list SMTP profiles", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleCreateSMTP(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name"`
		Host        string `json:"host"`
		Port        int    `json:"port"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		AuthMethod  string `json:"authMethod"`
		TLSMode     string `json:"tlsMode"`
		LocalName   string `json:"localName"`
		FromAddress string `json:"fromAddress"`
		FromName    string `json:"fromName"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, r, http.StatusBadRequest, "name_required", "Profile name is required", nil)
		return
	}
	if err := validateSMTPInput(in.Host, in.Port, in.AuthMethod, in.TLSMode, in.FromAddress); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_profile", err.Error(), nil)
		return
	}
	if in.AuthMethod == "" {
		in.AuthMethod = "PLAIN"
	}
	if in.TLSMode == "" {
		in.TLSMode = "starttls"
	}
	id := store.NewID()
	enc, err := s.App.Secrets.Seal("smtp:"+id, in.Password)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "encryption_failed", "Could not encrypt SMTP password", nil)
		return
	}
	p := &store.SMTPProfile{
		ID: id, Name: strings.TrimSpace(in.Name), Host: strings.TrimSpace(in.Host), Port: in.Port,
		Username: in.Username, Password: enc, AuthMethod: in.AuthMethod, TLSMode: in.TLSMode,
		LocalName: in.LocalName, FromAddress: strings.TrimSpace(in.FromAddress), FromName: in.FromName, Status: "created",
	}
	if err := s.App.Store.CreateSMTPProfile(r.Context(), p); err != nil {
		writeError(w, r, http.StatusBadRequest, "create_failed", err.Error(), nil)
		return
	}
	s.audit(r, "smtp.profile_created", "smtp_profile", p.ID, map[string]any{"name": p.Name, "host": p.Host})
	p.HasPassword = in.Password != ""
	writeJSON(w, http.StatusCreated, map[string]any{"profile": p})
}

func (s *Server) handleUpdateSMTP(w http.ResponseWriter, r *http.Request) {
	p, err := s.App.Store.GetSMTPProfile(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "SMTP profile") {
		return
	}
	var in struct {
		Name          string `json:"name"`
		Host          string `json:"host"`
		Port          int    `json:"port"`
		Username      string `json:"username"`
		Password      string `json:"password"`
		ClearPassword bool   `json:"clearPassword"`
		AuthMethod    string `json:"authMethod"`
		TLSMode       string `json:"tlsMode"`
		LocalName     string `json:"localName"`
		FromAddress   string `json:"fromAddress"`
		FromName      string `json:"fromName"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if in.Name != "" {
		p.Name = strings.TrimSpace(in.Name)
	}
	p.Host = strings.TrimSpace(in.Host)
	p.Port = in.Port
	p.Username = in.Username
	p.AuthMethod = in.AuthMethod
	p.TLSMode = in.TLSMode
	p.LocalName = in.LocalName
	p.FromAddress = strings.TrimSpace(in.FromAddress)
	p.FromName = in.FromName
	if err := validateSMTPInput(p.Host, p.Port, p.AuthMethod, p.TLSMode, p.FromAddress); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_profile", err.Error(), nil)
		return
	}
	if in.ClearPassword {
		p.Password = ""
	} else if in.Password != "" {
		enc, err := s.App.Secrets.Seal("smtp:"+p.ID, in.Password)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "encryption_failed", "Could not encrypt SMTP password", nil)
			return
		}
		p.Password = enc
	}
	p.Status = "created"
	p.LastError = ""
	if err := s.App.Store.UpdateSMTPProfile(r.Context(), p); err != nil {
		writeError(w, r, http.StatusBadRequest, "update_failed", err.Error(), nil)
		return
	}
	s.audit(r, "smtp.profile_updated", "smtp_profile", p.ID, map[string]any{"name": p.Name, "host": p.Host})
	p.HasPassword = p.Password != ""
	writeJSON(w, http.StatusOK, map[string]any{"profile": p})
}

func (s *Server) handleDeleteSMTP(w http.ResponseWriter, r *http.Request) {
	p, err := s.App.Store.GetSMTPProfile(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "SMTP profile") {
		return
	}
	if p.UsageCount > 0 {
		writeError(w, r, http.StatusConflict, "profile_in_use", "This profile is assigned to one or more instances; unassign it first", nil)
		return
	}
	if err := s.App.Store.DeleteSMTPProfile(r.Context(), p.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "delete_failed", err.Error(), nil)
		return
	}
	s.audit(r, "smtp.profile_deleted", "smtp_profile", p.ID, map[string]any{"name": p.Name})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTestSMTP(w http.ResponseWriter, r *http.Request) {
	p, err := s.App.Store.GetSMTPProfile(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "SMTP profile") {
		return
	}
	password, err := s.App.Secrets.Open("smtp:"+p.ID, p.Password)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "decrypt_failed", "SMTP password could not be decrypted", nil)
		return
	}
	res, err := smtpsvc.Test(r.Context(), p, password)
	if err != nil {
		_ = s.App.Store.SetSMTPTestState(r.Context(), p.ID, "failed", store.Now(), truncate(err.Error(), 500))
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = s.App.Store.SetSMTPTestState(r.Context(), p.ID, "validated", store.Now(), "")
	s.audit(r, "smtp.profile_tested", "smtp_profile", p.ID, map[string]any{"ok": true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}

func validProvider(p string) bool {
	switch p {
	case "digitalocean", "r2", "aws", "custom":
		return true
	default:
		return false
	}
}

func validateStorageInput(p string, endpoint, region, bucket, accessKey string) error {
	if !validProvider(p) {
		return errString("provider must be digitalocean, r2, aws, or custom")
	}
	if strings.TrimSpace(region) == "" || strings.TrimSpace(bucket) == "" || strings.TrimSpace(accessKey) == "" {
		return errString("region, bucket, and access key are required")
	}
	if strings.ContainsAny(bucket, `/\`) {
		return errString("invalid bucket name")
	}
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return errString("endpoint must be a valid http(s) URL")
		}
	} else if p != "aws" {
		return errString("endpoint is required for non-AWS providers")
	}
	return nil
}

func (s *Server) handleListStorage(w http.ResponseWriter, r *http.Request) {
	list, err := s.App.Store.ListStorageProfiles(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list storage profiles", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleCreateStorage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name           string `json:"name"`
		Provider       string `json:"provider"`
		Endpoint       string `json:"endpoint"`
		Region         string `json:"region"`
		Bucket         string `json:"bucket"`
		AccessKey      string `json:"accessKey"`
		SecretKey      string `json:"secretKey"`
		ForcePathStyle bool   `json:"forcePathStyle"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, r, http.StatusBadRequest, "name_required", "Profile name is required", nil)
		return
	}
	if err := validateStorageInput(in.Provider, in.Endpoint, in.Region, in.Bucket, in.AccessKey); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_profile", err.Error(), nil)
		return
	}
	if in.SecretKey == "" {
		writeError(w, r, http.StatusBadRequest, "secret_required", "Secret key is required", nil)
		return
	}
	id := store.NewID()
	enc, err := s.App.Secrets.Seal("storage:"+id, in.SecretKey)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "encryption_failed", "Could not encrypt storage secret", nil)
		return
	}
	p := &store.StorageProfile{
		ID: id, Name: strings.TrimSpace(in.Name), Provider: in.Provider, Endpoint: strings.TrimSpace(in.Endpoint),
		Region: strings.TrimSpace(in.Region), Bucket: strings.TrimSpace(in.Bucket), AccessKey: strings.TrimSpace(in.AccessKey),
		SecretKey: enc, ForcePathStyle: in.ForcePathStyle, Status: "created",
	}
	if err := s.App.Store.CreateStorageProfile(r.Context(), p); err != nil {
		writeError(w, r, http.StatusBadRequest, "create_failed", err.Error(), nil)
		return
	}
	s.audit(r, "storage.profile_created", "storage_profile", p.ID, map[string]any{"name": p.Name, "provider": p.Provider, "bucket": p.Bucket})
	p.HasSecret = true
	writeJSON(w, http.StatusCreated, map[string]any{"profile": p})
}

func (s *Server) handleUpdateStorage(w http.ResponseWriter, r *http.Request) {
	p, err := s.App.Store.GetStorageProfile(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Storage profile") {
		return
	}
	var in struct {
		Name           string `json:"name"`
		Provider       string `json:"provider"`
		Endpoint       string `json:"endpoint"`
		Region         string `json:"region"`
		Bucket         string `json:"bucket"`
		AccessKey      string `json:"accessKey"`
		SecretKey      string `json:"secretKey"`
		ClearSecret    bool   `json:"clearSecret"`
		ForcePathStyle bool   `json:"forcePathStyle"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if in.Name != "" {
		p.Name = strings.TrimSpace(in.Name)
	}
	p.Provider = in.Provider
	p.Endpoint = strings.TrimSpace(in.Endpoint)
	p.Region = strings.TrimSpace(in.Region)
	p.Bucket = strings.TrimSpace(in.Bucket)
	p.AccessKey = strings.TrimSpace(in.AccessKey)
	p.ForcePathStyle = in.ForcePathStyle
	if err := validateStorageInput(p.Provider, p.Endpoint, p.Region, p.Bucket, p.AccessKey); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_profile", err.Error(), nil)
		return
	}
	if in.ClearSecret {
		p.SecretKey = ""
	} else if in.SecretKey != "" {
		enc, err := s.App.Secrets.Seal("storage:"+p.ID, in.SecretKey)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "encryption_failed", "Could not encrypt storage secret", nil)
			return
		}
		p.SecretKey = enc
	}
	p.Status = "created"
	p.LastError = ""
	if err := s.App.Store.UpdateStorageProfile(r.Context(), p); err != nil {
		writeError(w, r, http.StatusBadRequest, "update_failed", err.Error(), nil)
		return
	}
	s.audit(r, "storage.profile_updated", "storage_profile", p.ID, map[string]any{"name": p.Name, "provider": p.Provider})
	p.HasSecret = p.SecretKey != ""
	writeJSON(w, http.StatusOK, map[string]any{"profile": p})
}

func (s *Server) handleDeleteStorage(w http.ResponseWriter, r *http.Request) {
	p, err := s.App.Store.GetStorageProfile(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Storage profile") {
		return
	}
	if p.UsageCount > 0 {
		writeError(w, r, http.StatusConflict, "profile_in_use", "This profile is assigned to one or more instances; unassign it first", nil)
		return
	}
	if err := s.App.Store.DeleteStorageProfile(r.Context(), p.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "delete_failed", err.Error(), nil)
		return
	}
	s.audit(r, "storage.profile_deleted", "storage_profile", p.ID, map[string]any{"name": p.Name})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTestStorage(w http.ResponseWriter, r *http.Request) {
	p, err := s.App.Store.GetStorageProfile(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Storage profile") {
		return
	}
	secret, err := s.App.Secrets.Open("storage:"+p.ID, p.SecretKey)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "decrypt_failed", "Storage secret could not be decrypted", nil)
		return
	}
	res, err := storage.Test(r.Context(), p, secret)
	if err != nil {
		_ = s.App.Store.SetStorageTestState(r.Context(), p.ID, "failed", store.Now(), truncate(err.Error(), 500))
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = s.App.Store.SetStorageTestState(r.Context(), p.ID, "validated", store.Now(), "")
	s.audit(r, "storage.profile_tested", "storage_profile", p.ID, map[string]any{"ok": true})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": res})
}
