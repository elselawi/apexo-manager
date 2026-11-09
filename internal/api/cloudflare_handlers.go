package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/apexo/pbmanager/internal/services/cloudflare"
	"github.com/apexo/pbmanager/internal/store"
)

func (s *Server) handleListCloudflare(w http.ResponseWriter, r *http.Request) {
	list, err := s.App.Store.ListCloudflareAccounts(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list Cloudflare accounts", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleCreateCloudflare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name      string `json:"name"`
		Token     string `json:"token"`
		AccountID string `json:"accountId"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, r, http.StatusBadRequest, "name_required", "Account name is required", nil)
		return
	}
	if err := cloudflare.ValidateTokenShape(in.Token); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_token", err.Error(), nil)
		return
	}
	client := cloudflare.New(strings.TrimSpace(in.Token))
	status, err := client.VerifyToken(r.Context())
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "token_validation_failed", err.Error(), nil)
		return
	}
	id := store.NewID()
	enc, err := s.App.Secrets.Seal("cloudflare:"+id, strings.TrimSpace(in.Token))
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "encryption_failed", "Could not encrypt token", nil)
		return
	}
	account := &store.CloudflareAccount{ID: id, Name: strings.TrimSpace(in.Name), APIToken: enc, AccountID: strings.TrimSpace(in.AccountID), Status: status, LastValidatedAt: store.Now()}
	if err := s.App.Store.CreateCloudflareAccount(r.Context(), account); err != nil {
		writeError(w, r, http.StatusBadRequest, "create_failed", err.Error(), nil)
		return
	}
	s.audit(r, "cloudflare.account_created", "cloudflare_account", account.ID, map[string]any{"name": account.Name})
	writeJSON(w, http.StatusCreated, map[string]any{"account": account})
}

func (s *Server) handleUpdateCloudflare(w http.ResponseWriter, r *http.Request) {
	account, err := s.App.Store.GetCloudflareAccount(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Cloudflare account") {
		return
	}
	var in struct {
		Name      string `json:"name"`
		Token     string `json:"token"`
		AccountID string `json:"accountId"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if strings.TrimSpace(in.Name) != "" {
		account.Name = strings.TrimSpace(in.Name)
	}
	account.AccountID = strings.TrimSpace(in.AccountID)
	if strings.TrimSpace(in.Token) != "" {
		if err := cloudflare.ValidateTokenShape(in.Token); err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_token", err.Error(), nil)
			return
		}
		client := cloudflare.New(strings.TrimSpace(in.Token))
		status, err := client.VerifyToken(r.Context())
		if err != nil {
			writeError(w, r, http.StatusBadGateway, "token_validation_failed", err.Error(), nil)
			return
		}
		account.Status = status
		account.LastValidatedAt = store.Now()
		account.LastError = ""
		enc, err := s.App.Secrets.Seal("cloudflare:"+account.ID, strings.TrimSpace(in.Token))
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "encryption_failed", "Could not encrypt token", nil)
			return
		}
		account.APIToken = enc
	}
	if err := s.App.Store.UpdateCloudflareAccount(r.Context(), account); err != nil {
		writeError(w, r, http.StatusBadRequest, "update_failed", err.Error(), nil)
		return
	}
	s.audit(r, "cloudflare.account_updated", "cloudflare_account", account.ID, map[string]any{"name": account.Name, "tokenChanged": in.Token != ""})
	writeJSON(w, http.StatusOK, map[string]any{"account": account})
}

func (s *Server) handleDeleteCloudflare(w http.ResponseWriter, r *http.Request) {
	account, err := s.App.Store.GetCloudflareAccount(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Cloudflare account") {
		return
	}
	var in struct {
		Confirmation string `json:"confirmation"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if in.Confirmation != account.Name {
		writeError(w, r, http.StatusBadRequest, "confirmation_mismatch", "Type the exact account name to confirm", nil)
		return
	}
	domains, _ := s.App.Store.ListDomains(r.Context())
	for _, d := range domains {
		if d.CloudflareAccountID == account.ID {
			writeError(w, r, http.StatusConflict, "account_in_use", "This account is used by one or more domains; detach or reassign them first", nil)
			return
		}
	}
	if err := s.App.Store.DeleteCloudflareAccount(r.Context(), account.ID); err != nil {
		writeError(w, r, http.StatusInternalServerError, "delete_failed", err.Error(), nil)
		return
	}
	s.audit(r, "cloudflare.account_deleted", "cloudflare_account", account.ID, map[string]any{"name": account.Name})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) cloudflareToken(ctx context.Context, id string) (string, error) {
	account, err := s.App.Store.GetCloudflareAccount(ctx, id)
	if err != nil {
		return "", err
	}
	return s.App.Secrets.Open("cloudflare:"+account.ID, account.APIToken)
}

func (s *Server) handleValidateCloudflare(w http.ResponseWriter, r *http.Request) {
	account, err := s.App.Store.GetCloudflareAccount(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Cloudflare account") {
		return
	}
	token, err := s.App.Secrets.Open("cloudflare:"+account.ID, account.APIToken)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "decrypt_failed", "Cloudflare token could not be decrypted", nil)
		return
	}
	status, err := cloudflare.New(token).VerifyToken(r.Context())
	if err != nil {
		_ = s.App.Store.SetCloudflareValidation(r.Context(), account.ID, "error", store.Now(), truncate(err.Error(), 500))
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	_ = s.App.Store.SetCloudflareValidation(r.Context(), account.ID, status, store.Now(), "")
	s.audit(r, "cloudflare.account_validated", "cloudflare_account", account.ID, map[string]any{"status": status})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
}

func (s *Server) handleCloudflareZones(w http.ResponseWriter, r *http.Request) {
	token, err := s.cloudflareToken(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "account_error", err.Error(), nil)
		return
	}
	zones, err := cloudflare.New(token).ListZones(r.Context())
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "cloudflare_api_error", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": zones})
}

func (s *Server) handleCloudflareRecords(w http.ResponseWriter, r *http.Request) {
	token, err := s.cloudflareToken(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "account_error", err.Error(), nil)
		return
	}
	zone := r.PathValue("zone")
	records, err := cloudflare.New(token).ListRecords(r.Context(), zone, strings.ToUpper(r.URL.Query().Get("type")), r.URL.Query().Get("name"))
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "cloudflare_api_error", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": records})
}
