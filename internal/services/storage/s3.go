// Package storage validates S3-compatible storage profiles using SigV4 and the
// ListObjectsV2 API. It never logs access keys or secrets.
package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/store"
)

// TestResult reports a successful connectivity and credential check.
type TestResult struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	Region    string `json:"region"`
	Provider  string `json:"provider"`
	LatencyMS int64  `json:"latencyMs"`
	Status    int    `json:"status"`
}

// Test performs a signed ListObjectsV2 request with max-keys=1.
func Test(ctx context.Context, p *store.StorageProfile, secret string) (TestResult, error) {
	start := time.Now()
	if p.Bucket == "" || p.Region == "" || p.AccessKey == "" || secret == "" {
		return TestResult{}, errors.New("bucket, region, access key, and secret key are required")
	}
	endpoint, err := normalizeEndpoint(p)
	if err != nil {
		return TestResult{}, err
	}
	u, err := buildObjectURL(endpoint, p.Bucket, p.ForcePathStyle)
	if err != nil {
		return TestResult{}, err
	}
	q := u.Query()
	q.Set("list-type", "2")
	q.Set("max-keys", "1")
	u.RawQuery = canonicalQuery(q)

	payloadHash := sha256.Sum256(nil)
	payloadHex := hex.EncodeToString(payloadHash[:])
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	headers := map[string]string{
		"host":                 u.Host,
		"x-amz-content-sha256": payloadHex,
		"x-amz-date":           amzDate,
	}
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + headers["host"] + "\n" +
		"x-amz-content-sha256:" + headers["x-amz-content-sha256"] + "\n" +
		"x-amz-date:" + headers["x-amz-date"] + "\n"
	canonicalRequest := strings.Join([]string{
		http.MethodGet,
		u.EscapedPath(),
		u.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHex,
	}, "\n")
	scope := date + "/" + p.Region + "/s3/aws4_request"
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(requestHash[:])
	signingKey := hmacSHA256([]byte("AWS4"+secret), date)
	signingKey = hmacSHA256(signingKey, p.Region)
	signingKey = hmacSHA256(signingKey, "s3")
	signingKey = hmacSHA256(signingKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return TestResult{}, err
	}
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHex)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+p.AccessKey+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
	req.Header.Set("User-Agent", "pbmanager-s3-test/1")

	client := &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errors.New("S3 redirects are not followed")
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return TestResult{}, fmt.Errorf("S3 connection failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return TestResult{
			Endpoint: u.Scheme + "://" + u.Host, Bucket: p.Bucket, Region: p.Region, Provider: p.Provider,
			LatencyMS: time.Since(start).Milliseconds(), Status: resp.StatusCode,
		}, nil
	}
	msg := parseS3Error(raw)
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return TestResult{}, fmt.Errorf("S3 request failed with HTTP %d: %s", resp.StatusCode, msg)
}

func normalizeEndpoint(p *store.StorageProfile) (*url.URL, error) {
	endpoint := strings.TrimSpace(p.Endpoint)
	if endpoint == "" && p.Provider == "aws" {
		endpoint = "https://s3." + p.Region + ".amazonaws.com"
	}
	if endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("endpoint must be a valid http(s) URL")
	}
	if u.Scheme == "http" && !isLoopback(u.Hostname()) {
		return nil, errors.New("plain HTTP endpoints are only allowed for loopback test services")
	}
	if u.User != nil {
		return nil, errors.New("endpoint must not contain credentials")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, errors.New("endpoint must not contain a path")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("endpoint must not contain a query or fragment")
	}
	return u, nil
}

func buildObjectURL(endpoint *url.URL, bucket string, pathStyle bool) (*url.URL, error) {
	if strings.ContainsAny(bucket, "/\\") || bucket == "" {
		return nil, errors.New("invalid bucket name")
	}
	u := *endpoint
	if pathStyle {
		u.Path = "/" + bucket
	} else {
		u.Path = "/"
		u.Host = bucket + "." + endpoint.Host
		if endpoint.Port() != "" {
			u.Host = bucket + "." + endpoint.Hostname() + ":" + endpoint.Port()
		}
	}
	u.RawQuery = ""
	return &u, nil
}

func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := append([]string{}, values[k]...)
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, awsEncode(k)+"="+awsEncode(v))
		}
	}
	return strings.Join(parts, "&")
}

func awsEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func parseS3Error(raw []byte) string {
	var payload struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if err := xml.Unmarshal(raw, &payload); err == nil {
		if payload.Code != "" {
			return strings.TrimSpace(payload.Code + ": " + payload.Message)
		}
	}
	if text := strings.TrimSpace(string(raw)); len(text) < 300 {
		return text
	}
	return ""
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
