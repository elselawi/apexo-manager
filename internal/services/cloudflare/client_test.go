package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientZoneAndRecordOperations(t *testing.T) {
	var created bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token-value-123456" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []map[string]any{{"code": 10000, "message": "auth"}}})
			return
		}
		switch {
		case r.URL.Path == "/user/tokens/verify":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"status": "active"}})
		case r.URL.Path == "/zones" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"result": []map[string]any{
					{"id": "zone1", "name": "example.com", "status": "active"},
					{"id": "zone2", "name": "sub.example.com", "status": "active"},
				},
				"result_info": map[string]any{"page": 1, "total_pages": 1},
			})
		case strings.HasSuffix(r.URL.Path, "/dns_records") && r.Method == http.MethodGet:
			if r.URL.Query().Get("name") != "clinic-a.example.com" {
				t.Errorf("unexpected name filter %q", r.URL.Query().Get("name"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []map[string]any{}, "result_info": map[string]any{"page": 1, "total_pages": 1}})
		case strings.HasSuffix(r.URL.Path, "/dns_records") && r.Method == http.MethodPost:
			created = true
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["name"] != "clinic-a.example.com" || payload["content"] != "203.0.113.5" {
				t.Errorf("unexpected create payload: %#v", payload)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{
				"id": "rec1", "type": "A", "name": payload["name"], "content": payload["content"], "proxied": true, "comment": payload["comment"],
			}})
		case strings.HasSuffix(r.URL.Path, "/dns_records/rec1") && r.Method == http.MethodDelete:
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"id": "rec1"}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := New("test-token-value-123456")
	client.BaseURL = srv.URL
	status, err := client.VerifyToken(context.Background())
	if err != nil || status != "active" {
		t.Fatalf("verify: %v %v", status, err)
	}
	zones, err := client.ListZones(context.Background())
	if err != nil || len(zones) != 2 {
		t.Fatalf("zones: %v %#v", err, zones)
	}
	zone, err := client.FindZone(context.Background(), "clinic-a.example.com")
	if err != nil || zone.ID != "zone1" {
		t.Fatalf("find zone: %v %#v", err, zone)
	}
	zone, err = client.FindZone(context.Background(), "x.sub.example.com")
	if err != nil || zone.ID != "zone2" {
		t.Fatalf("longest zone: %v %#v", err, zone)
	}
	rec, err := client.CreateRecord(context.Background(), "zone1", DNSRecord{
		Type: "A", Name: "clinic-a.example.com", Content: "203.0.113.5", TTL: 300, Proxied: true, Comment: Marker("inst-1"),
	})
	if err != nil || rec.ID != "rec1" || !created {
		t.Fatalf("create: %v %#v", err, rec)
	}
	if !IsManagedRecord(rec, "inst-1") || IsManagedRecord(rec, "inst-2") {
		t.Fatal("ownership check failed")
	}
	if err := client.DeleteRecord(context.Background(), "zone1", "rec1"); err != nil {
		t.Fatal(err)
	}
}

func TestTokenValidation(t *testing.T) {
	if err := ValidateTokenShape("short"); err == nil {
		t.Fatal("short token accepted")
	}
	if err := ValidateTokenShape("has spaces and is long enough maybe"); err == nil {
		t.Fatal("whitespace token accepted")
	}
	if err := ValidateTokenShape("abcdefghijklmnopqrstuvwxyz1234567890"); err != nil {
		t.Fatal(err)
	}
}
