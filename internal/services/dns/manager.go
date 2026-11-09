// Package dns manages explicitly-owned Cloudflare DNS records.
package dns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/netutil"
	"github.com/apexo/pbmanager/internal/secret"
	"github.com/apexo/pbmanager/internal/services/cloudflare"
	"github.com/apexo/pbmanager/internal/settings"
	"github.com/apexo/pbmanager/internal/store"
)

// Manager ensures DNS records for domains.
type Manager struct {
	Store   *store.Store
	Secrets *secret.Box
	Cfg     func() settings.Config
}

// Ensure creates or updates the manager-owned A record for a domain.
func (m *Manager) Ensure(ctx context.Context, inst *store.Instance, d *store.Domain, accountID string, proxied bool) (*cloudflare.DNSRecord, error) {
	if accountID == "" {
		return nil, errors.New("a Cloudflare account is required for DNS management")
	}
	account, err := m.Store.GetCloudflareAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.APIToken == "" {
		return nil, errors.New("Cloudflare account has no token")
	}
	token, err := m.Secrets.Open("cloudflare:"+account.ID, account.APIToken)
	if err != nil {
		return nil, errors.New("Cloudflare token could not be decrypted")
	}
	ip := strings.TrimSpace(m.Cfg().PublicIPv4)
	if ip == "" {
		detected, derr := netutil.DetectPublicIPv4(ctx)
		if derr != nil {
			return nil, fmt.Errorf("public IPv4 is not configured and could not be detected: %w", derr)
		}
		ip = detected
	}
	if net.ParseIP(ip) == nil || net.ParseIP(ip).To4() == nil {
		return nil, errors.New("configured public IPv4 is invalid")
	}
	client := cloudflare.New(token)
	zone, err := client.FindZone(ctx, d.Hostname)
	if err != nil {
		return nil, err
	}
	records, err := client.ListRecords(ctx, zone.ID, "", d.Hostname)
	if err != nil {
		return nil, err
	}
	var owned *cloudflare.DNSRecord
	for i := range records {
		r := &records[i]
		if r.Name != d.Hostname && !strings.EqualFold(r.Name, d.Hostname+".") {
			continue
		}
		if r.Type != "A" && r.Type != "AAAA" && r.Type != "CNAME" {
			continue
		}
		if d.DNSRecordID != "" && r.ID == d.DNSRecordID {
			owned = r
			break
		}
		if owned == nil && cloudflare.IsManagedRecord(*r, inst.ID) {
			owned = r
		}
	}
	if owned != nil && owned.ID != d.DNSRecordID {
		if !cloudflare.IsManagedRecord(*owned, inst.ID) {
			return nil, fmt.Errorf("conflicting DNS record %s (%s) is not managed by pbmanager", owned.Name, owned.Type)
		}
		d.DNSRecordID = owned.ID
	}
	if owned != nil {
		if !cloudflare.IsManagedRecord(*owned, inst.ID) {
			return nil, fmt.Errorf("refusing to modify Cloudflare record %s: ownership marker is missing", owned.ID)
		}
		if owned.Type == "A" && owned.Content == ip && owned.Proxied == proxied {
			d.DNSRecordID = owned.ID
			return owned, nil
		}
		updated, err := client.UpdateRecord(ctx, zone.ID, owned.ID, cloudflare.DNSRecord{
			Type: "A", Name: d.Hostname, Content: ip, TTL: 300, Proxied: proxied, Comment: cloudflare.Marker(inst.ID),
		})
		if err != nil {
			return nil, err
		}
		d.DNSRecordID = updated.ID
		return &updated, nil
	}
	for _, r := range records {
		if (r.Name == d.Hostname || strings.EqualFold(r.Name, d.Hostname+".")) && (r.Type == "A" || r.Type == "AAAA" || r.Type == "CNAME") {
			return nil, fmt.Errorf("conflicting DNS record exists: %s %s %s (id %s); resolve it manually", r.Name, r.Type, r.Content, r.ID)
		}
	}
	created, err := client.CreateRecord(ctx, zone.ID, cloudflare.DNSRecord{
		Type: "A", Name: d.Hostname, Content: ip, TTL: 300, Proxied: proxied, Comment: cloudflare.Marker(inst.ID),
	})
	if err != nil {
		return nil, err
	}
	d.DNSRecordID = created.ID
	d.ZoneID = zone.ID
	d.ZoneName = zone.Name
	d.CloudflareAccountID = account.ID
	return &created, nil
}

// Remove deletes a manager-owned record after verifying ownership.
func (m *Manager) Remove(ctx context.Context, d *store.Domain) error {
	if d.DNSRecordID == "" || d.CloudflareAccountID == "" {
		return nil
	}
	account, err := m.Store.GetCloudflareAccount(ctx, d.CloudflareAccountID)
	if err != nil {
		return err
	}
	token, err := m.Secrets.Open("cloudflare:"+account.ID, account.APIToken)
	if err != nil {
		return errors.New("Cloudflare token could not be decrypted")
	}
	client := cloudflare.New(token)
	record, err := client.GetRecord(ctx, d.ZoneID, d.DNSRecordID)
	if err != nil {
		return err
	}
	if !cloudflare.IsManagedRecord(record, d.InstanceID) {
		return errors.New("refusing to delete a Cloudflare record without the pbmanager ownership marker")
	}
	if err := client.DeleteRecord(ctx, d.ZoneID, d.DNSRecordID); err != nil {
		return err
	}
	d.DNSRecordID = ""
	return nil
}

// Check resolves a hostname through public DNS.
func Check(ctx context.Context, hostname string) (netutil.DNSResult, error) {
	return netutil.CheckPublicDNS(ctx, hostname)
}

// VerifyOwned checks that a tracked record still exists and is ours.
func VerifyOwned(ctx context.Context, m *Manager, d *store.Domain, token string) error {
	client := cloudflare.New(token)
	record, err := client.GetRecord(ctx, d.ZoneID, d.DNSRecordID)
	if err != nil {
		return err
	}
	if !cloudflare.IsManagedRecord(record, d.InstanceID) {
		return errors.New("DNS record ownership marker is missing")
	}
	return nil
}

// WaitPropagation polls public DNS until the hostname resolves or timeout.
func WaitPropagation(ctx context.Context, hostname string, timeout time.Duration) (netutil.DNSResult, error) {
	deadline := time.Now().Add(timeout)
	var last netutil.DNSResult
	var lastErr error
	for {
		res, err := netutil.CheckPublicDNS(ctx, hostname)
		last, lastErr = res, err
		if err == nil && len(res.Addresses) > 0 {
			return res, nil
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return last, fmt.Errorf("DNS did not propagate: %w", lastErr)
			}
			return last, errors.New("DNS did not propagate before timeout")
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
