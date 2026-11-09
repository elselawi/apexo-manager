package storage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apexo/pbmanager/internal/store"
)

func verifySigV4(r *http.Request, secret string) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "AWS4-HMAC-SHA256 Credential=AKIA/"
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	rest := strings.TrimPrefix(auth, prefix)
	parts := strings.Split(rest, ", ")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "2026") {
		return false
	}
	scope := parts[0] // date/region/s3/aws4_request
	signedHeaders := strings.TrimPrefix(parts[1], "SignedHeaders=")
	signature := strings.TrimPrefix(parts[2], "Signature=")
	headerNames := strings.Split(signedHeaders, ";")
	var canonicalHeaders strings.Builder
	for _, name := range headerNames {
		value := r.Header.Get(name)
		if name == "host" {
			value = r.Host
		}
		canonicalHeaders.WriteString(name + ":" + strings.TrimSpace(value) + "\n")
	}
	payloadHash := r.Header.Get("x-amz-content-sha256")
	canonicalRequest := strings.Join([]string{
		r.Method, r.URL.EscapedPath(), r.URL.RawQuery, canonicalHeaders.String(), signedHeaders, payloadHash,
	}, "\n")
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	amzDate := r.Header.Get("x-amz-date")
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(requestHash[:])
	hmac256 := func(key []byte, data string) []byte {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(data))
		return m.Sum(nil)
	}
	date := strings.Split(scope, "/")[0]
	region := strings.Split(scope, "/")[1]
	key := hmac256([]byte("AWS4"+secret), date)
	key = hmac256(key, region)
	key = hmac256(key, "s3")
	key = hmac256(key, "aws4_request")
	expected := hex.EncodeToString(hmac256(key, stringToSign))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func TestSignedBucketProbe(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKIA/") {
			sawAuth = true
		}
		if !verifySigV4(r, "secret-key") {
			t.Errorf("invalid SigV4 signature; auth=%q path=%q query=%q", r.Header.Get("Authorization"), r.URL.Path, r.URL.RawQuery)
		}
		if r.URL.Path != "/my-bucket" || r.URL.Query().Get("list-type") != "2" {
			t.Errorf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("x-amz-content-sha256") == "" || r.Header.Get("x-amz-date") == "" {
			t.Errorf("missing signing headers")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><ListBucketResult></ListBucketResult>`))
	}))
	defer srv.Close()
	profile := &store.StorageProfile{
		Provider: "custom", Endpoint: srv.URL, Region: "us-east-1", Bucket: "my-bucket",
		AccessKey: "AKIA", SecretKey: "encrypted", ForcePathStyle: true,
	}
	res, err := Test(context.Background(), profile, "secret-key")
	if err != nil {
		t.Fatal(err)
	}
	if !sawAuth || res.Status != 200 || res.Bucket != "my-bucket" {
		t.Fatalf("unexpected result: %#v auth=%v", res, sawAuth)
	}
}

func TestBucketProbeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`))
	}))
	defer srv.Close()
	profile := &store.StorageProfile{
		Provider: "custom", Endpoint: srv.URL, Region: "auto", Bucket: "b",
		AccessKey: "AKIA", SecretKey: "encrypted", ForcePathStyle: true,
	}
	if _, err := Test(context.Background(), profile, "secret"); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("expected access denied, got %v", err)
	}
}

func TestRejectsInsecureRemoteEndpoint(t *testing.T) {
	profile := &store.StorageProfile{Provider: "custom", Endpoint: "http://example.com", Region: "r", Bucket: "b", AccessKey: "a"}
	if _, err := normalizeEndpoint(profile); err == nil {
		t.Fatal("insecure remote endpoint accepted")
	}
}
