package checker

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/internal/model"
)

func TestSMTPSTARTTLSRetainsResponseBudget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture CA uses the Unix SSL_CERT_FILE trust override")
	}
	certServer := httptest.NewTLSServer(nil)
	certificate := certServer.TLS.Certificates[0]
	certServer.Close()
	caFile := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, oversized := range []bool{false, true} {
		name, want := "valid TLS dialogue", "up"
		if oversized {
			name, want = "oversized TLS EHLO", "down"
		}
		t.Run(name, func(t *testing.T) {
			target := protocolServer(t, func(conn net.Conn) {
				reader := bufio.NewReader(conn)
				_, _ = io.WriteString(conn, "220 fixture.example\r\n")
				if line, err := reader.ReadString('\n'); err != nil || !strings.HasPrefix(line, "EHLO ") {
					return
				}
				_, _ = io.WriteString(conn, "250-fixture.example\r\n250 STARTTLS\r\n")
				if line, err := reader.ReadString('\n'); err != nil || line != "STARTTLS\r\n" {
					return
				}
				_, _ = io.WriteString(conn, "220 ready for TLS\r\n")
				secure := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}})
				defer secure.Close()
				if err := secure.Handshake(); err != nil {
					return
				}
				reader = bufio.NewReader(secure)
				if line, err := reader.ReadString('\n'); err != nil || !strings.HasPrefix(line, "EHLO ") {
					return
				}
				reply := "250 fixture.example\r\n"
				if oversized {
					reply = "250 " + strings.Repeat("x", 1<<20) + "\r\n"
				}
				if _, err := io.WriteString(secure, reply); err != nil {
					return
				}
				if line, err := reader.ReadString('\n'); err == nil && line == "QUIT\r\n" {
					_, _ = io.WriteString(secure, "221 goodbye\r\n")
				}
			})
			// A fresh process prevents system-root caching or a test-only TLS
			// configuration hook from weakening the real checker trust path.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSMTPSTARTTLSBudgetChild$")
			cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+caFile, "ASURA_TEST_SMTP_TARGET="+target, "ASURA_TEST_SMTP_EXPECT="+want)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("trusted STARTTLS fixture failed: %v %s", err, out)
			}
		})
	}
}

func TestSMTPSTARTTLSBudgetChild(t *testing.T) {
	target := os.Getenv("ASURA_TEST_SMTP_TARGET")
	if target == "" {
		t.Skip("subprocess fixture")
	}
	got, err := (&SMTPChecker{AllowPrivate: true}).Check(context.Background(), &model.Monitor{
		Target: target, Timeout: 3, Settings: json.RawMessage(`{"starttls":true}`),
	})
	want := os.Getenv("ASURA_TEST_SMTP_EXPECT")
	if err != nil || got == nil || got.Status != want || want == "down" && !strings.Contains(got.Message, "exceeds 1 MiB") {
		t.Fatalf("STARTTLS result=%+v err=%v want=%s", got, err, want)
	}
}
