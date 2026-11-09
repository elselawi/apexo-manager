package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/netutil"
	"github.com/apexo/pbmanager/internal/services/dns"
	"github.com/apexo/pbmanager/internal/services/instances"
	"github.com/apexo/pbmanager/internal/store"
)

func (s *Server) handleListDomains(w http.ResponseWriter, r *http.Request) {
	list, err := s.App.Store.ListDomains(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list domains", nil)
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	instanceID := r.URL.Query().Get("instanceId")
	filtered := list[:0]
	for _, d := range list {
		if instanceID != "" && d.InstanceID != instanceID {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(d.Hostname+" "+d.InstanceName), q) {
			continue
		}
		filtered = append(filtered, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": filtered, "total": len(filtered)})
}

func (s *Server) handleGetDomain(w http.ResponseWriter, r *http.Request) {
	d, err := s.App.Store.GetDomain(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Domain") {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"domain": d})
}

func (s *Server) handleAttachDomain(w http.ResponseWriter, r *http.Request) {
	var in instances.DomainInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	d, job, err := s.App.Instances.AttachDomain(r.Context(), r.PathValue("id"), in, adminFrom(r).Username)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "attach_failed", err.Error(), nil)
		return
	}
	detail := map[string]any{"hostname": d.Hostname, "manageCaddy": in.ManageCaddy, "manageDns": in.ManageDNS}
	if job != nil {
		detail["jobId"] = job.ID
	}
	s.audit(r, "domain.attached", "domain", d.ID, detail)
	writeJSON(w, http.StatusAccepted, map[string]any{"domain": d, "job": job})
}

func (s *Server) handleDetachDomain(w http.ResponseWriter, r *http.Request) {
	d, err := s.App.Store.GetDomain(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Domain") {
		return
	}
	var in struct {
		Confirmation string `json:"confirmation"`
		RemoveCaddy  bool   `json:"removeCaddy"`
		RemoveDNS    bool   `json:"removeDns"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if in.Confirmation != d.Hostname {
		writeError(w, r, http.StatusBadRequest, "confirmation_mismatch", "Type the exact domain name to confirm", nil)
		return
	}
	job, err := s.App.Instances.RemoveDomain(r.Context(), d.ID, in.RemoveCaddy, in.RemoveDNS, in.Confirmation, adminFrom(r).Username)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "detach_failed", err.Error(), nil)
		return
	}
	s.audit(r, "domain.detach_requested", "domain", d.ID, map[string]any{"removeCaddy": in.RemoveCaddy, "removeDns": in.RemoveDNS, "jobId": job.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"job": job})
}

func (s *Server) handleDomainStatus(w http.ResponseWriter, r *http.Request) {
	d, err := s.App.Store.GetDomain(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Domain") {
		return
	}
	var in struct {
		Active  bool   `json:"active"`
		Message string `json:"message"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_body", err.Error(), nil)
		return
	}
	if len(in.Message) > 2000 {
		writeError(w, r, http.StatusBadRequest, "message_too_long", "Message must be 2000 characters or fewer", nil)
		return
	}
	if err := s.App.Store.SetDomainStatus(r.Context(), d.ID, in.Active, in.Message); err != nil {
		if notFoundOr(w, r, err, "Domain") {
			return
		}
		writeError(w, r, http.StatusInternalServerError, "status_update_failed", "Could not update domain status", nil)
		return
	}
	s.audit(r, "domain.status_updated", "domain", d.ID, map[string]any{"active": in.Active})
	d.HostedActive = in.Active
	d.HostedMessage = in.Message
	writeJSON(w, http.StatusOK, map[string]any{"domain": d})
}

func (s *Server) handleCheckDomain(w http.ResponseWriter, r *http.Request) {
	d, err := s.App.Store.GetDomain(r.Context(), r.PathValue("id"))
	if notFoundOr(w, r, err, "Domain") {
		return
	}
	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()

	dnsStatus, tlsStatus := "pending", "unknown"
	var errs []string
	var addresses []string
	if res, err := dns.Check(ctx, d.Hostname); err != nil {
		dnsStatus = "error"
		errs = append(errs, "dns: "+err.Error())
	} else {
		addresses = res.Addresses
		if len(addresses) > 0 {
			dnsStatus = "ok"
		} else {
			dnsStatus = "error"
			errs = append(errs, "dns: no addresses returned")
		}
	}
	tlsInfo := netutil.CheckTLS(ctx, d.Hostname)
	tlsExpiry := ""
	if tlsInfo.Error == "" {
		tlsStatus = "valid"
		if tlsInfo.DaysLeft < 14 {
			tlsStatus = "expiring"
		}
		tlsExpiry = tlsInfo.NotAfter
	} else {
		tlsStatus = "error"
		errs = append(errs, "tls: "+tlsInfo.Error)
	}
	caddyStatus := d.CaddyStatus
	if d.CaddyManaged && d.SnippetPath != "" {
		caddyStatus = "ok"
	}
	errText := strings.Join(errs, "; ")
	_ = s.App.Store.UpdateDomainChecks(ctx, d.ID, dnsStatus, caddyStatus, tlsStatus, tlsExpiry, store.Now(), errText)
	writeJSON(w, http.StatusOK, map[string]any{
		"domainId": d.ID, "hostname": d.Hostname, "dnsStatus": dnsStatus, "addresses": addresses,
		"caddyStatus": caddyStatus, "tls": tlsInfo, "tlsStatus": tlsStatus, "checkedAt": store.Now(),
	})
}
