// Package netutil contains network probes used for health, DNS, TLS, and
// public-address discovery.
package netutil

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// DNSResult is a DNS resolution result.
type DNSResult struct {
	Hostname  string   `json:"hostname"`
	Addresses []string `json:"addresses"`
	Resolver  string   `json:"resolver"`
	CheckedAt string   `json:"checkedAt"`
}

// Lookup resolves a hostname through the system resolver with a fixed timeout.
func Lookup(ctx context.Context, hostname string) (DNSResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, hostname)
	if err != nil {
		return DNSResult{Hostname: hostname, Resolver: "system", CheckedAt: time.Now().UTC().Format(time.RFC3339)}, err
	}
	return DNSResult{Hostname: hostname, Addresses: addrs, Resolver: "system", CheckedAt: time.Now().UTC().Format(time.RFC3339)}, nil
}

// CheckPublicDNS asks Cloudflare's public resolver directly. It is used for
// propagation checks and does not trust local caches.
func CheckPublicDNS(ctx context.Context, hostname string) (DNSResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", "1.1.1.1:53")
		},
	}
	addrs, err := resolver.LookupHost(ctx, hostname)
	if err != nil {
		return DNSResult{Hostname: hostname, Resolver: "1.1.1.1", CheckedAt: time.Now().UTC().Format(time.RFC3339)}, err
	}
	return DNSResult{Hostname: hostname, Addresses: addrs, Resolver: "1.1.1.1", CheckedAt: time.Now().UTC().Format(time.RFC3339)}, nil
}

// TLSInfo describes an observed leaf certificate.
type TLSInfo struct {
	Hostname  string `json:"hostname"`
	Subject   string `json:"subject"`
	Issuer    string `json:"issuer"`
	NotBefore string `json:"notBefore"`
	NotAfter  string `json:"notAfter"`
	DaysLeft  int    `json:"daysLeft"`
	Error     string `json:"error,omitempty"`
}

// CheckTLS connects to hostname:443 with SNI and validates the certificate.
func CheckTLS(ctx context.Context, hostname string) TLSInfo {
	info := TLSInfo{Hostname: hostname}
	if hostname == "" || strings.ContainsAny(hostname, `/\ `) {
		info.Error = "invalid hostname"
		return info
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 8 * time.Second},
		Config:    &tls.Config{ServerName: hostname, MinVersion: tls.VersionTLS12},
	}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(hostname, "443"))
	if err != nil {
		info.Error = err.Error()
		return info
	}
	defer conn.Close()
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		info.Error = "unexpected connection type"
		return info
	}
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		info.Error = "no peer certificate"
		return info
	}
	cert := state.PeerCertificates[0]
	info.Subject = cert.Subject.CommonName
	info.Issuer = cert.Issuer.CommonName
	info.NotBefore = cert.NotBefore.UTC().Format(time.RFC3339)
	info.NotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
	info.DaysLeft = int(time.Until(cert.NotAfter).Hours() / 24)
	return info
}

// PortListening checks whether a loopback TCP port accepts connections.
func PortListening(ctx context.Context, port int) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// CheckHTTP performs a bounded GET against a URL.
func CheckHTTP(ctx context.Context, url string) (int, []byte, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return errors.New("unsupported redirect scheme")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "pbmanager-health/1")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, body, nil
}

// DetectPublicIPv4 asks an external echo service, then falls back to the local
// address selected for a default route. The result is advisory and can always
// be overridden in server settings.
func DetectPublicIPv4(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 6 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.ipify.org?format=json", nil)
	req.Header.Set("User-Agent", "pbmanager/1")
	if resp, err := client.Do(req); err == nil {
		defer resp.Body.Close()
		var payload struct {
			IP string `json:"ip"`
		}
		if resp.StatusCode == 200 && json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&payload) == nil {
			if ip := net.ParseIP(payload.IP); ip != nil && ip.To4() != nil {
				return ip.String(), nil
			}
		}
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", "1.1.1.1:53")
	if err != nil {
		return "", fmt.Errorf("could not determine public address: %w", err)
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil || addr.IP.IsLoopback() {
		return "", errors.New("no usable local address found")
	}
	return addr.IP.String(), nil
}

// PortFree reports whether a TCP port is currently free on loopback.
func PortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// SplitHostPortDefault parses host:port and applies a default port.
func SplitHostPortDefault(addr string, defaultPort string) (string, string, error) {
	if !strings.Contains(addr, ":") {
		return addr, defaultPort, nil
	}
	host, port, err := net.SplitHostPort(addr)
	return host, port, err
}
