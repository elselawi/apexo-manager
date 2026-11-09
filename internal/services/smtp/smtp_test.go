package smtp

import (
	"bufio"
	"context"
	"encoding/base64"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/apexo/pbmanager/internal/store"
)

type fakeSMTP struct {
	ln       net.Listener
	password string
	wg       sync.WaitGroup
}

func startFakeSMTP(t *testing.T, password string) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, password: password}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				defer conn.Close()
				handleFakeSMTP(conn, password)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		f.wg.Wait()
	})
	return f
}

func handleFakeSMTP(conn net.Conn, password string) {
	reader := bufio.NewReader(conn)
	write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	write("220 localhost ESMTP ready")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		upper := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			write("250-localhost")
			write("250-AUTH PLAIN LOGIN")
			write("250 STARTTLS")
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			fields := strings.Fields(cmd)
			if len(fields) == 3 {
				if decodeAndCheck(fields[2], password) {
					write("235 Authentication successful")
				} else {
					write("535 Authentication failed")
				}
			} else {
				write("334 ")
				data, _ := reader.ReadString('\n')
				if decodeAndCheck(strings.TrimSpace(data), password) {
					write("235 Authentication successful")
				} else {
					write("535 Authentication failed")
				}
			}
		case strings.HasPrefix(upper, "NOOP"):
			write("250 OK")
		case strings.HasPrefix(upper, "QUIT"):
			write("221 Bye")
			return
		default:
			write("250 OK")
		}
	}
}

func decodeAndCheck(encoded, password string) bool {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	parts := strings.Split(string(raw), "\x00")
	return len(parts) >= 3 && parts[len(parts)-1] == password
}

func TestSMTPConnectionAndAuth(t *testing.T) {
	fake := startFakeSMTP(t, "smtp-secret")
	_, port, _ := net.SplitHostPort(fake.ln.Addr().String())
	profile := &store.SMTPProfile{
		Host: "localhost", Port: parsePort(port), Username: "user", AuthMethod: "PLAIN", TLSMode: "none",
	}
	res, err := Test(context.Background(), profile, "smtp-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Auth {
		t.Fatal("expected authentication")
	}
	profile.Username = "user"
	res, err = Test(context.Background(), profile, "wrong")
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("expected auth failure, got %v", err)
	}
}

func TestSMTPRejectsPlaintextRemote(t *testing.T) {
	profile := &store.SMTPProfile{Host: "smtp.example.com", Port: 25, AuthMethod: "PLAIN", TLSMode: "none"}
	if _, err := Test(context.Background(), profile, "x"); err == nil {
		t.Fatal("plaintext remote accepted")
	}
}

func parsePort(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
