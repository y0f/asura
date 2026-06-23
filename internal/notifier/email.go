package notifier

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

const smtpDialTimeout = 15 * time.Second

// dialSMTP opens a TCP connection to a mail server with a bounded timeout and,
// unless allowPrivate is set, refuses to connect to private/reserved IPs so a
// configured SMTP host cannot be used to probe internal services.
func dialSMTP(ctx context.Context, addr string, allowPrivate bool) (net.Conn, error) {
	d := &net.Dialer{
		Timeout: smtpDialTimeout,
		Control: safenet.MaybeDialControl(allowPrivate),
	}
	return d.DialContext(ctx, "tcp", addr)
}

// newSMTPClient dials addr and wraps it in an SMTP client, closing the
// connection if the client greeting fails.
func newSMTPClient(ctx context.Context, addr, host string, allowPrivate bool) (*smtp.Client, error) {
	conn, err := dialSMTP(ctx, addr, allowPrivate)
	if err != nil {
		return nil, err
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return client, nil
}

type EmailSettings struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
	CC       []string `json:"cc,omitempty"`
	BCC      []string `json:"bcc,omitempty"`
	TLSMode  string   `json:"tls_mode,omitempty"` // none, starttls (default), smtps
}

type EmailSender struct {
	AllowPrivate bool
}

func (s *EmailSender) Type() string { return "email" }

func (s *EmailSender) Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error {
	var settings EmailSettings
	if err := json.Unmarshal(channel.Settings, &settings); err != nil {
		return fmt.Errorf("invalid email settings: %w", err)
	}

	if settings.Host == "" || len(settings.To) == 0 {
		return fmt.Errorf("email host and recipients are required")
	}

	port := settings.Port
	if port == 0 {
		switch settings.TLSMode {
		case "smtps":
			port = 465
		case "none":
			port = 25
		default:
			port = 587
		}
	}

	subject := sanitizeHeader(FormatMessage(payload))
	allRcpt := make([]string, 0, len(settings.To)+len(settings.CC)+len(settings.BCC))
	allRcpt = append(allRcpt, settings.To...)
	allRcpt = append(allRcpt, settings.CC...)
	allRcpt = append(allRcpt, settings.BCC...)

	msgBytes := buildEmailMessage(settings, subject, payload)
	addr := fmt.Sprintf("%s:%d", settings.Host, port)
	host := settings.Host

	switch settings.TLSMode {
	case "smtps":
		return sendSMTPS(ctx, addr, host, settings, allRcpt, msgBytes, s.AllowPrivate)
	case "none":
		return sendPlain(ctx, addr, host, settings, allRcpt, msgBytes, s.AllowPrivate)
	default:
		return sendSTARTTLS(ctx, addr, host, settings, allRcpt, msgBytes, s.AllowPrivate)
	}
}

func sendSMTPS(ctx context.Context, addr, host string, s EmailSettings, rcpt []string, msg []byte, allowPrivate bool) error {
	conn, err := dialSMTP(ctx, addr, allowPrivate)
	if err != nil {
		return fmt.Errorf("smtps dial failed: %w", err)
	}
	tconn := tls.Client(conn, &tls.Config{ServerName: host})
	if err := tconn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return fmt.Errorf("smtps handshake failed: %w", err)
	}
	client, err := smtp.NewClient(tconn, host)
	if err != nil {
		tconn.Close()
		return fmt.Errorf("smtps client: %w", err)
	}
	defer client.Close()
	return sendViaClient(client, s, rcpt, msg)
}

func sendPlain(ctx context.Context, addr, host string, s EmailSettings, rcpt []string, msg []byte, allowPrivate bool) error {
	client, err := newSMTPClient(ctx, addr, host, allowPrivate)
	if err != nil {
		return fmt.Errorf("smtp dial failed: %w", err)
	}
	defer client.Close()
	return sendViaClient(client, s, rcpt, msg)
}

func sendSTARTTLS(ctx context.Context, addr, host string, s EmailSettings, rcpt []string, msg []byte, allowPrivate bool) error {
	client, err := newSMTPClient(ctx, addr, host, allowPrivate)
	if err != nil {
		return fmt.Errorf("smtp dial failed: %w", err)
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("starttls failed: %w", err)
		}
	}
	return sendViaClient(client, s, rcpt, msg)
}

func sendViaClient(client *smtp.Client, s EmailSettings, rcpt []string, msg []byte) error {
	if s.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(s.From); err != nil {
		return err
	}
	for _, r := range rcpt {
		if err := client.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	return w.Close()
}

func buildEmailMessage(s EmailSettings, subject string, payload *Payload) []byte {
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("From: %s\r\n", sanitizeHeader(s.From)))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", sanitizeHeader(strings.Join(s.To, ", "))))
	if len(s.CC) > 0 {
		msg.WriteString(fmt.Sprintf("Cc: %s\r\n", sanitizeHeader(strings.Join(s.CC, ", "))))
	}
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(buildHTMLBody(subject, payload))
	return []byte(msg.String())
}

func buildHTMLBody(subject string, payload *Payload) string {
	statusColor := "#6b7280"
	eventLabel := payload.EventType
	detail := subject

	if payload.Incident != nil {
		switch payload.EventType {
		case "incident.created":
			statusColor = "#f87171"
			eventLabel = "Alert"
		case "incident.resolved":
			statusColor = "#34d399"
			eventLabel = "Resolved"
		case "incident.acknowledged":
			statusColor = "#fbbf24"
			eventLabel = "Acknowledged"
		case "incident.reminder":
			statusColor = "#f87171"
			eventLabel = "Reminder"
		}
		detail = html.EscapeString(payload.Incident.MonitorName) + ": " + html.EscapeString(payload.Incident.Cause)
	} else if payload.EventType == "cert.changed" && payload.Monitor != nil {
		statusColor = "#fbbf24"
		eventLabel = "Certificate Changed"
		detail = "Certificate fingerprint changed for " + html.EscapeString(payload.Monitor.Name)
	} else if payload.EventType == "content.changed" {
		statusColor = "#60a5fa"
		eventLabel = "Content Changed"
	} else if payload.EventType == "test" {
		statusColor = "#818cf8"
		eventLabel = "Test"
		detail = "This is a test notification from Asura"
	}

	return `<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#111827;font-family:system-ui,-apple-system,sans-serif">
<table width="100%" cellpadding="0" cellspacing="0" style="background:#111827;padding:32px 0">
<tr><td align="center">
<table width="480" cellpadding="0" cellspacing="0" style="background:#1f2937;border:1px solid #374151;border-radius:8px;overflow:hidden">
<tr><td style="background:` + statusColor + `;height:4px"></td></tr>
<tr><td style="padding:24px 28px">
<p style="margin:0 0 4px;font-size:11px;text-transform:uppercase;letter-spacing:.08em;color:#9ca3af">` + html.EscapeString(eventLabel) + `</p>
<p style="margin:0;font-size:18px;font-weight:600;color:#f9fafb">Asura Alert</p>
</td></tr>
<tr><td style="padding:0 28px 24px">
<p style="margin:0;font-size:14px;color:#d1d5db;line-height:1.6">` + detail + `</p>
</td></tr>
<tr><td style="padding:16px 28px;border-top:1px solid #374151">
<p style="margin:0;font-size:11px;color:#6b7280">Sent by <a href="#" style="color:#6b7280">Asura</a></p>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`
}

// sanitizeHeader strips CR and LF to prevent email header injection.
func sanitizeHeader(s string) string {
	r := strings.NewReplacer("\r", "", "\n", "")
	return r.Replace(s)
}
