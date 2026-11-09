// Package smtp validates SMTP profiles using direct authenticated connections.
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/apexo/pbmanager/internal/store"
)

// TestResult describes a successful SMTP connection/auth check.
type TestResult struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	TLSMode   string `json:"tlsMode"`
	Auth      bool   `json:"authenticated"`
	LatencyMS int64  `json:"latencyMs"`
}

// Test connects, negotiates TLS as configured, authenticates if credentials are
// present, and issues NOOP. It does not send an email.
func Test(ctx context.Context, p *store.SMTPProfile, password string) (TestResult, error) {
	start := time.Now()
	if p.Host == "" || p.Port < 1 || p.Port > 65535 {
		return TestResult{}, errors.New("valid SMTP host and port are required")
	}
	if strings.ContainsAny(p.Host, " \t\r\n/") {
		return TestResult{}, errors.New("invalid SMTP host")
	}
	if p.TLSMode != "starttls" && p.TLSMode != "tls" && p.TLSMode != "none" {
		return TestResult{}, errors.New("TLS mode must be starttls, tls, or none")
	}
	if p.TLSMode == "none" && !isLoopback(p.Host) {
		return TestResult{}, errors.New("unencrypted SMTP is only permitted for a loopback test server")
	}
	if p.AuthMethod != "PLAIN" && p.AuthMethod != "LOGIN" {
		return TestResult{}, errors.New("auth method must be PLAIN or LOGIN")
	}
	if p.FromAddress != "" {
		if _, err := mail.ParseAddress(p.FromAddress); err != nil {
			return TestResult{}, errors.New("sender address is not a valid email address")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	var conn net.Conn
	var err error
	if p.TLSMode == "tls" {
		d := &net.Dialer{Timeout: 10 * time.Second}
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: p.Host, MinVersion: tls.VersionTLS12})
	} else {
		d := &net.Dialer{Timeout: 10 * time.Second}
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return TestResult{}, fmt.Errorf("SMTP connection failed: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	client, err := smtp.NewClient(conn, p.Host)
	if err != nil {
		return TestResult{}, fmt.Errorf("SMTP greeting failed: %w", err)
	}
	defer client.Close()
	if p.LocalName != "" {
		if err := client.Hello(p.LocalName); err != nil {
			return TestResult{}, fmt.Errorf("SMTP EHLO failed: %w", err)
		}
	}
	authenticated := false
	if p.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return TestResult{}, errors.New("SMTP server does not advertise STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: p.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return TestResult{}, fmt.Errorf("SMTP STARTTLS failed: %w", err)
		}
	}
	if p.Username != "" || password != "" {
		var auth smtp.Auth
		switch p.AuthMethod {
		case "PLAIN":
			auth = smtp.PlainAuth("", p.Username, password, p.Host)
		case "LOGIN":
			auth = &loginAuth{username: p.Username, password: password, host: p.Host}
		}
		if err := client.Auth(auth); err != nil {
			return TestResult{}, fmt.Errorf("SMTP authentication failed: %w", err)
		}
		authenticated = true
	}
	if err := client.Noop(); err != nil {
		return TestResult{}, fmt.Errorf("SMTP NOOP failed: %w", err)
	}
	if err := client.Quit(); err != nil {
		// Some servers close immediately after QUIT; this is non-fatal.
		_ = err
	}
	return TestResult{Host: p.Host, Port: p.Port, TLSMode: p.TLSMode, Auth: authenticated, LatencyMS: time.Since(start).Milliseconds()}, nil
}

type loginAuth struct {
	username, password, host string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLoopback(server.Name) {
		return "", nil, errors.New("refusing LOGIN authentication over an unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("unexpected SMTP server name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("unexpected SMTP LOGIN challenge")
	}
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
