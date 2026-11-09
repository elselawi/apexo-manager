package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/apexo/pbmanager/internal/store"
)

func (s *Server) handleHostedStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(r.PathValue("hostname"))), ".")
	if host == "" || strings.ContainsAny(host, " /\\") {
		_, _ = w.Write([]byte("{}\n"))
		return
	}
	d, err := s.App.Store.GetDomainByHostname(r.Context(), host)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			_, _ = w.Write([]byte("{}\n"))
			return
		}
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not resolve hostname", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": d.HostedActive, "dismissable": d.HostedDismissable, "message": d.HostedMessage})
}

func (s *Server) handleHostedOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.WriteHeader(http.StatusNoContent)
}
