// Package cloudflare is a small client for the official Cloudflare API. It
// only touches DNS records explicitly tracked by the manager.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

// Client is a Cloudflare API client bound to one token.
type Client struct {
	Token   string
	BaseURL string
	HTTP    *http.Client
}

// New creates a client.
func New(token string) *Client {
	return &Client{Token: token, BaseURL: DefaultBaseURL, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Zone is a Cloudflare zone.
type Zone struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	AccountName string `json:"accountName,omitempty"`
}

// DNSRecord is a Cloudflare DNS record.
type DNSRecord struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Content  string   `json:"content"`
	Proxied  bool     `json:"proxied"`
	TTL      int      `json:"ttl"`
	Comment  string   `json:"comment,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Modified string   `json:"modified_on,omitempty"`
}

// APIError is a Cloudflare API failure.
type APIError struct {
	Status  int
	Errors  []string
	Message string
}

func (e *APIError) Error() string {
	if len(e.Errors) > 0 {
		return fmt.Sprintf("Cloudflare API error (HTTP %d): %s", e.Status, strings.Join(e.Errors, "; "))
	}
	if e.Message != "" {
		return fmt.Sprintf("Cloudflare API error (HTTP %d): %s", e.Status, e.Message)
	}
	return fmt.Sprintf("Cloudflare API error (HTTP %d)", e.Status)
}

type envelope struct {
	Success    bool            `json:"success"`
	Errors     []cfError       `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		TotalPages int `json:"total_pages"`
		Count      int `json:"count"`
	} `json:"result_info"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) (envelope, error) {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(strings.TrimRight(base, "/") + path)
	if err != nil {
		return envelope{}, err
	}
	if query != nil {
		u.RawQuery = query.Encode()
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return envelope{}, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return envelope{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "pbmanager-cloudflare/1")
	resp, err := c.client().Do(req)
	if err != nil {
		return envelope{}, fmt.Errorf("Cloudflare connection failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return env, &APIError{Status: resp.StatusCode, Message: "invalid response from Cloudflare"}
	}
	if resp.StatusCode >= 400 || !env.Success {
		apiErr := &APIError{Status: resp.StatusCode}
		for _, e := range env.Errors {
			apiErr.Errors = append(apiErr.Errors, fmt.Sprintf("%d: %s", e.Code, e.Message))
		}
		if len(apiErr.Errors) == 0 {
			apiErr.Message = http.StatusText(resp.StatusCode)
		}
		return env, apiErr
	}
	if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return env, fmt.Errorf("decode Cloudflare result: %w", err)
		}
	}
	return env, nil
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// VerifyToken validates the token and returns its status.
func (c *Client) VerifyToken(ctx context.Context) (string, error) {
	var out struct {
		Status string `json:"status"`
	}
	if _, err := c.do(ctx, http.MethodGet, "/user/tokens/verify", nil, nil, &out); err != nil {
		return "", err
	}
	if out.Status == "" {
		out.Status = "active"
	}
	return out.Status, nil
}

// ListZones returns all zones visible to the token (bounded pagination).
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	var out []Zone
	for page := 1; page <= 20; page++ {
		q := url.Values{}
		q.Set("page", fmt.Sprint(page))
		q.Set("per_page", "50")
		var zones []Zone
		env, err := c.do(ctx, http.MethodGet, "/zones", q, nil, &zones)
		if err != nil {
			return nil, err
		}
		out = append(out, zones...)
		if env.ResultInfo.TotalPages == 0 || page >= env.ResultInfo.TotalPages {
			break
		}
	}
	return out, nil
}

// FindZone selects the most specific zone suffix for a hostname.
func (c *Client) FindZone(ctx context.Context, hostname string) (*Zone, error) {
	zones, err := c.ListZones(ctx)
	if err != nil {
		return nil, err
	}
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	var best *Zone
	for i := range zones {
		name := strings.ToLower(zones[i].Name)
		if hostname == name || strings.HasSuffix(hostname, "."+name) {
			if best == nil || len(name) > len(best.Name) {
				best = &zones[i]
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no Cloudflare zone contains %s", hostname)
	}
	return best, nil
}

// ListRecords lists DNS records, optionally filtered by type and exact name.
func (c *Client) ListRecords(ctx context.Context, zoneID, recordType, name string) ([]DNSRecord, error) {
	var out []DNSRecord
	for page := 1; page <= 20; page++ {
		q := url.Values{}
		q.Set("page", fmt.Sprint(page))
		q.Set("per_page", "100")
		if recordType != "" {
			q.Set("type", recordType)
		}
		if name != "" {
			q.Set("name", name)
		}
		var records []DNSRecord
		env, err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/dns_records", q, nil, &records)
		if err != nil {
			return nil, err
		}
		out = append(out, records...)
		if env.ResultInfo.TotalPages == 0 || page >= env.ResultInfo.TotalPages {
			break
		}
	}
	return out, nil
}

// CreateRecord creates an A/AAAA/CNAME/TXT record.
func (c *Client) CreateRecord(ctx context.Context, zoneID string, rec DNSRecord) (DNSRecord, error) {
	payload := map[string]any{
		"type":    rec.Type,
		"name":    rec.Name,
		"content": rec.Content,
		"ttl":     rec.TTL,
		"proxied": rec.Proxied,
		"comment": rec.Comment,
	}
	var out DNSRecord
	_, err := c.do(ctx, http.MethodPost, "/zones/"+url.PathEscape(zoneID)+"/dns_records", nil, payload, &out)
	return out, err
}

// UpdateRecord updates a tracked record.
func (c *Client) UpdateRecord(ctx context.Context, zoneID, recordID string, rec DNSRecord) (DNSRecord, error) {
	payload := map[string]any{
		"type":    rec.Type,
		"name":    rec.Name,
		"content": rec.Content,
		"ttl":     rec.TTL,
		"proxied": rec.Proxied,
		"comment": rec.Comment,
	}
	var out DNSRecord
	_, err := c.do(ctx, http.MethodPut, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(recordID), nil, payload, &out)
	return out, err
}

// DeleteRecord deletes a tracked record.
func (c *Client) DeleteRecord(ctx context.Context, zoneID, recordID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(recordID), nil, nil, nil)
	return err
}

// GetRecord loads a record by ID.
func (c *Client) GetRecord(ctx context.Context, zoneID, recordID string) (DNSRecord, error) {
	var out DNSRecord
	_, err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(zoneID)+"/dns_records/"+url.PathEscape(recordID), nil, nil, &out)
	return out, err
}

// IsManagedRecord reports whether a record is explicitly owned by this manager.
func IsManagedRecord(rec DNSRecord, instanceID string) bool {
	if !strings.Contains(rec.Comment, "managed-by:pbmanager") {
		return false
	}
	if instanceID == "" {
		return true
	}
	return strings.Contains(rec.Comment, "instance:"+instanceID)
}

// Marker returns the ownership comment for a record.
func Marker(instanceID string) string {
	return "managed-by:pbmanager instance:" + instanceID
}

// ValidateTokenShape performs a cheap token sanity check before a network call.
func ValidateTokenShape(token string) error {
	token = strings.TrimSpace(token)
	if len(token) < 20 || len(token) > 512 {
		return errors.New("Cloudflare API token looks invalid")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return errors.New("Cloudflare API token must not contain whitespace")
	}
	return nil
}
