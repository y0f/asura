package notifier

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestSanitizeHeader(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"hello", "hello"},
		{"hello\r\nworld", "helloworld"},
		{"inject\rvalue", "injectvalue"},
		{"inject\nvalue", "injectvalue"},
		{"Subject: test\r\nX-Evil: injected", "Subject: testX-Evil: injected"},
		{"clean header", "clean header"},
	}
	for _, tc := range tests {
		got := sanitizeHeader(tc.in)
		if got != tc.want {
			t.Errorf("sanitizeHeader(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildHTMLBody(t *testing.T) {
	tests := []struct {
		name      string
		subject   string
		payload   *Payload
		wantColor string
		wantLabel string
		wantText  string
	}{
		{
			name:      "incident created",
			subject:   "Alert",
			payload:   &Payload{EventType: "incident.created", Incident: &storage.Incident{MonitorName: "MyMonitor", Cause: "timeout"}},
			wantColor: "#f87171",
			wantLabel: "Alert",
			wantText:  "MyMonitor",
		},
		{
			name:      "incident resolved",
			subject:   "Resolved",
			payload:   &Payload{EventType: "incident.resolved", Incident: &storage.Incident{MonitorName: "MyMonitor", Cause: "ok"}},
			wantColor: "#34d399",
			wantLabel: "Resolved",
		},
		{
			name:      "incident acknowledged",
			subject:   "Acknowledged",
			payload:   &Payload{EventType: "incident.acknowledged", Incident: &storage.Incident{MonitorName: "MyMonitor", Cause: "ack"}},
			wantColor: "#fbbf24",
			wantLabel: "Acknowledged",
		},
		{
			name:      "test notification",
			subject:   "Test",
			payload:   &Payload{EventType: "test"},
			wantColor: "#818cf8",
			wantLabel: "Test",
			wantText:  "test notification",
		},
		{
			name:      "cert changed",
			subject:   "Cert",
			payload:   &Payload{EventType: "cert.changed", Monitor: &storage.Monitor{Name: "example.com"}},
			wantColor: "#fbbf24",
			wantLabel: "Certificate Changed",
			wantText:  "example.com",
		},
		{
			name:      "content changed",
			subject:   "Content",
			payload:   &Payload{EventType: "content.changed"},
			wantColor: "#60a5fa",
			wantLabel: "Content Changed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			html := buildHTMLBody(tc.subject, tc.payload)
			if !strings.Contains(html, tc.wantColor) {
				t.Errorf("expected color %s in HTML", tc.wantColor)
			}
			if !strings.Contains(html, tc.wantLabel) {
				t.Errorf("expected label %q in HTML", tc.wantLabel)
			}
			if tc.wantText != "" && !strings.Contains(html, tc.wantText) {
				t.Errorf("expected text %q in HTML", tc.wantText)
			}
			if !strings.Contains(html, "<!DOCTYPE html>") {
				t.Error("expected DOCTYPE in HTML")
			}
		})
	}
}

func TestBuildHTMLBodyXSSEscape(t *testing.T) {
	payload := &Payload{
		EventType: "incident.created",
		Incident: &storage.Incident{
			MonitorName: "<script>alert('xss')</script>",
			Cause:       "<img src=x onerror=alert(1)>",
		},
	}
	html := buildHTMLBody("Alert", payload)
	if strings.Contains(html, "<script>") {
		t.Error("XSS: unescaped <script> tag in HTML body")
	}
	if strings.Contains(html, "<img") {
		t.Error("XSS: unescaped <img> tag in HTML body")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Error("expected escaped &lt;script&gt; in HTML body")
	}
}

func TestBuildEmailMessage(t *testing.T) {
	s := EmailSettings{
		From: "from@example.com",
		To:   []string{"to@example.com"},
		CC:   []string{"cc@example.com"},
	}
	payload := &Payload{EventType: "incident.created", Incident: &storage.Incident{MonitorName: "m", Cause: "c"}}
	subject := "Test Subject"

	msg := string(buildEmailMessage(s, subject, payload))

	checks := []string{
		"From: from@example.com",
		"To: to@example.com",
		"Cc: cc@example.com",
		"Subject: Test Subject",
		"MIME-Version: 1.0",
		"Content-Type: text/html; charset=UTF-8",
		"<!DOCTYPE html>",
	}
	for _, want := range checks {
		if !strings.Contains(msg, want) {
			t.Errorf("expected %q in email message", want)
		}
	}
}

func TestBuildEmailMessageNoBCC(t *testing.T) {
	s := EmailSettings{
		From: "from@example.com",
		To:   []string{"to@example.com"},
		BCC:  []string{"bcc@example.com"},
	}
	payload := &Payload{EventType: "test"}
	msg := string(buildEmailMessage(s, "subj", payload))

	// BCC must not appear in headers
	if strings.Contains(msg, "Bcc:") || strings.Contains(msg, "bcc@example.com") {
		t.Error("BCC address must not appear in email headers")
	}
	// CC header should be absent when CC is empty
	if strings.Contains(msg, "Cc:") {
		t.Error("Cc header should not appear when CC is empty")
	}
}

func TestEmailSettingsPortDefaults(t *testing.T) {
	tests := []struct {
		tlsMode  string
		wantPort int
	}{
		{"smtps", 465},
		{"none", 25},
		{"starttls", 587},
		{"", 587},
	}
	for _, tc := range tests {
		t.Run(tc.tlsMode, func(t *testing.T) {
			s := EmailSettings{TLSMode: tc.tlsMode}
			port := s.Port
			if port == 0 {
				switch s.TLSMode {
				case "smtps":
					port = 465
				case "none":
					port = 25
				default:
					port = 587
				}
			}
			if port != tc.wantPort {
				t.Errorf("TLSMode=%q: got port %d, want %d", tc.tlsMode, port, tc.wantPort)
			}
		})
	}
}

func TestEmailSenderSendPlain(t *testing.T) {
	// Start a minimal SMTP server accepting one connection
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	_, port, _ := net.SplitHostPort(ln.Addr().String())

	doneCh := make(chan struct{ from, to, body string }, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			close(doneCh)
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		write := func(s string) { conn.Write([]byte(s + "\r\n")) }

		write("220 testsmtp ESMTP")
		var from, to, body string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				write("250 OK")
			case strings.HasPrefix(line, "MAIL FROM:"):
				from = line
				write("250 OK")
			case strings.HasPrefix(line, "RCPT TO:"):
				to = line
				write("250 OK")
			case line == "DATA":
				write("354 Start input")
				var sb strings.Builder
				for {
					l, _ := r.ReadString('\n')
					if strings.TrimSpace(l) == "." {
						break
					}
					sb.WriteString(l)
				}
				body = sb.String()
				write("250 OK")
			case line == "QUIT":
				write("221 Bye")
				doneCh <- struct{ from, to, body string }{from, to, body}
				return
			}
		}
		doneCh <- struct{ from, to, body string }{from, to, body}
	}()

	var portNum int
	fmt.Sscanf(port, "%d", &portNum)

	settings, _ := json.Marshal(EmailSettings{
		Host:    "127.0.0.1",
		Port:    portNum,
		From:    "alerts@example.com",
		To:      []string{"admin@example.com"},
		TLSMode: "none",
	})
	ch := &storage.NotificationChannel{
		ID:       1,
		Name:     "Test Email",
		Type:     "email",
		Settings: settings,
	}
	payload := &Payload{
		EventType: "test",
	}

	sender := &EmailSender{AllowPrivate: true}
	if err := sender.Send(context.Background(), ch, payload); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	result := <-doneCh
	if !strings.Contains(result.from, "alerts@example.com") {
		t.Errorf("MAIL FROM: got %q, want alerts@example.com", result.from)
	}
	if !strings.Contains(result.to, "admin@example.com") {
		t.Errorf("RCPT TO: got %q, want admin@example.com", result.to)
	}
	if !strings.Contains(result.body, "<!DOCTYPE html>") {
		t.Error("expected HTML body in email")
	}
}

func TestEmailSenderInvalidSettings(t *testing.T) {
	ch := &storage.NotificationChannel{
		Settings: []byte("not json"),
	}
	sender := &EmailSender{}
	err := sender.Send(nil, ch, &Payload{EventType: "test"})
	if err == nil {
		t.Fatal("expected error for invalid settings JSON")
	}
}

func TestEmailSenderMissingRequired(t *testing.T) {
	tests := []struct {
		name     string
		settings EmailSettings
	}{
		{"no host", EmailSettings{To: []string{"a@b.com"}}},
		{"no to", EmailSettings{Host: "smtp.example.com"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(tc.settings)
			ch := &storage.NotificationChannel{Settings: b}
			sender := &EmailSender{}
			err := sender.Send(nil, ch, &Payload{EventType: "test"})
			if err == nil {
				t.Fatal("expected error for missing required field")
			}
		})
	}
}
