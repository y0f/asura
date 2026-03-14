package notifier

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/storage"
)

type SubscriberNotifier struct {
	store  storage.Store
	smtp   config.SMTPConfig
	extURL string
	logger *slog.Logger
	sem    chan struct{}
}

func NewSubscriberNotifier(store storage.Store, smtpCfg config.SMTPConfig, extURL string, logger *slog.Logger) *SubscriberNotifier {
	return &SubscriberNotifier{
		store:  store,
		smtp:   smtpCfg,
		extURL: strings.TrimRight(extURL, "/"),
		logger: logger,
		sem:    make(chan struct{}, 10),
	}
}

type SubscriberEvent struct {
	Event      string `json:"event"`
	StatusPage struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
		Slug  string `json:"slug"`
	} `json:"status_page"`
	Monitor  *subscriberMonitor  `json:"monitor,omitempty"`
	Incident *subscriberIncident `json:"incident,omitempty"`
	Time     string              `json:"timestamp"`
}

type subscriberMonitor struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type subscriberIncident struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Cause     string `json:"cause"`
	StartedAt string `json:"started_at"`
}

func (n *SubscriberNotifier) NotifyForMonitor(ctx context.Context, monitorID int64, eventType string, inc *storage.Incident, mon *storage.Monitor) {
	pageIDs, err := n.store.GetStatusPageIDsForMonitor(ctx, monitorID)
	if err != nil {
		n.logger.Error("subscriber: get status pages for monitor", "monitor_id", monitorID, "error", err)
		return
	}
	if len(pageIDs) == 0 {
		return
	}

	for _, pageID := range pageIDs {
		sp, err := n.store.GetStatusPage(ctx, pageID)
		if err != nil || sp == nil || !sp.Enabled {
			continue
		}
		go n.notifyPageSubscribers(ctx, sp, eventType, inc, mon)
	}
}

func (n *SubscriberNotifier) notifyPageSubscribers(ctx context.Context, sp *storage.StatusPage, eventType string, inc *storage.Incident, mon *storage.Monitor) {
	subs, err := n.store.ListConfirmedSubscribers(ctx, sp.ID)
	if err != nil {
		n.logger.Error("subscriber: list subscribers", "page_id", sp.ID, "error", err)
		return
	}
	if len(subs) == 0 {
		return
	}

	subject := subscriberSubject(eventType, mon, inc)

	for _, sub := range subs {
		sub := sub
		n.sem <- struct{}{}
		go func() {
			defer func() { <-n.sem }()

			sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			var err error
			switch sub.Type {
			case "email":
				err = n.sendSubscriberEmail(sendCtx, sub, sp, eventType, subject, inc, mon)
			case "webhook":
				err = n.sendSubscriberWebhook(sendCtx, sub, sp, eventType, inc, mon)
			}
			if err != nil {
				n.logger.Warn("subscriber: send failed",
					"type", sub.Type,
					"page_id", sp.ID,
					"subscriber_id", sub.ID,
					"error", err,
				)
			}
		}()
	}
}

func subscriberSubject(eventType string, mon *storage.Monitor, inc *storage.Incident) string {
	name := ""
	if mon != nil {
		name = mon.Name
	} else if inc != nil {
		name = inc.MonitorName
	}

	switch eventType {
	case "incident.created":
		return fmt.Sprintf("Incident opened: %s", name)
	case "incident.resolved":
		return fmt.Sprintf("Incident resolved: %s", name)
	default:
		return fmt.Sprintf("Status update: %s", name)
	}
}

func (n *SubscriberNotifier) sendSubscriberEmail(ctx context.Context, sub *storage.StatusPageSubscriber, sp *storage.StatusPage, eventType, subject string, inc *storage.Incident, mon *storage.Monitor) error {
	if n.smtp.Host == "" || n.smtp.From == "" {
		return fmt.Errorf("subscription SMTP not configured")
	}

	unsubURL := fmt.Sprintf("%s/%s/unsubscribe?token=%s", n.extURL, sp.Slug, sub.Token)
	body := buildSubscriberEmailBody(sp.Title, eventType, subject, inc, mon, unsubURL)

	port := n.smtp.Port
	if port == 0 {
		switch n.smtp.TLSMode {
		case "smtps":
			port = 465
		case "none":
			port = 25
		default:
			port = 587
		}
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("From: %s\r\n", sanitizeHeader(n.smtp.From)))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", sanitizeHeader(sub.Email)))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", sanitizeHeader(subject)))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	msg.WriteString(fmt.Sprintf("List-Unsubscribe: <%s>\r\n", unsubURL))
	msg.WriteString("\r\n")
	msg.WriteString(body)

	msgBytes := []byte(msg.String())
	addr := fmt.Sprintf("%s:%d", n.smtp.Host, port)
	host := n.smtp.Host

	switch n.smtp.TLSMode {
	case "smtps":
		return n.sendSMTPS(addr, host, []string{sub.Email}, msgBytes)
	case "none":
		return n.sendPlain(addr, host, []string{sub.Email}, msgBytes)
	default:
		return smtp.SendMail(addr, n.smtpAuth(host), n.smtp.From, []string{sub.Email}, msgBytes)
	}
}

func (n *SubscriberNotifier) smtpAuth(host string) smtp.Auth {
	if n.smtp.Username != "" {
		return smtp.PlainAuth("", n.smtp.Username, n.smtp.Password, host)
	}
	return nil
}

func (n *SubscriberNotifier) sendSMTPS(addr, host string, rcpt []string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return fmt.Errorf("smtps dial: %w", err)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtps client: %w", err)
	}
	defer client.Close()
	return n.sendViaClient(client, rcpt, msg)
}

func (n *SubscriberNotifier) sendPlain(addr, host string, rcpt []string, msg []byte) error {
	client, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer client.Close()
	return n.sendViaClient(client, rcpt, msg)
}

func (n *SubscriberNotifier) sendViaClient(client *smtp.Client, rcpt []string, msg []byte) error {
	if n.smtp.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", n.smtp.Username, n.smtp.Password, n.smtp.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(n.smtp.From); err != nil {
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

func (n *SubscriberNotifier) sendSubscriberWebhook(ctx context.Context, sub *storage.StatusPageSubscriber, sp *storage.StatusPage, eventType string, inc *storage.Incident, mon *storage.Monitor) error {
	event := SubscriberEvent{
		Event: eventType,
		Time:  time.Now().UTC().Format(time.RFC3339),
	}
	event.StatusPage.ID = sp.ID
	event.StatusPage.Title = sp.Title
	event.StatusPage.Slug = sp.Slug

	if mon != nil {
		event.Monitor = &subscriberMonitor{ID: mon.ID, Name: mon.Name}
	} else if inc != nil {
		event.Monitor = &subscriberMonitor{ID: inc.MonitorID, Name: inc.MonitorName}
	}
	if inc != nil {
		event.Incident = &subscriberIncident{
			ID:        inc.ID,
			Status:    inc.Status,
			Cause:     inc.Cause,
			StartedAt: inc.StartedAt.Format(time.RFC3339),
		}
	}

	body, _ := json.Marshal(event)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Asura/1.0")

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func buildSubscriberEmailBody(pageTitle, eventType, subject string, inc *storage.Incident, mon *storage.Monitor, unsubURL string) string {
	statusColor := "#6b7280"
	eventLabel := eventType
	detail := subject

	switch eventType {
	case "incident.created":
		statusColor = "#f87171"
		eventLabel = "Incident Opened"
		if inc != nil {
			detail = html.EscapeString(inc.MonitorName) + ": " + html.EscapeString(inc.Cause)
		}
	case "incident.resolved":
		statusColor = "#34d399"
		eventLabel = "Incident Resolved"
		if inc != nil {
			detail = html.EscapeString(inc.MonitorName) + " is back to normal"
		}
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
<p style="margin:0;font-size:18px;font-weight:600;color:#f9fafb">` + html.EscapeString(pageTitle) + `</p>
</td></tr>
<tr><td style="padding:0 28px 24px">
<p style="margin:0;font-size:14px;color:#d1d5db;line-height:1.6">` + detail + `</p>
</td></tr>
<tr><td style="padding:16px 28px;border-top:1px solid #374151">
<p style="margin:0;font-size:11px;color:#6b7280">
<a href="` + html.EscapeString(unsubURL) + `" style="color:#6b7280;text-decoration:underline">Unsubscribe</a>
</p>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`
}

func (n *SubscriberNotifier) SendConfirmationEmail(ctx context.Context, sub *storage.StatusPageSubscriber, sp *storage.StatusPage) error {
	if n.smtp.Host == "" || n.smtp.From == "" {
		return fmt.Errorf("subscription SMTP not configured")
	}

	confirmURL := fmt.Sprintf("%s/%s/confirm?token=%s", n.extURL, sp.Slug, sub.Token)
	subject := fmt.Sprintf("Confirm your subscription to %s", sp.Title)

	body := `<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#111827;font-family:system-ui,-apple-system,sans-serif">
<table width="100%" cellpadding="0" cellspacing="0" style="background:#111827;padding:32px 0">
<tr><td align="center">
<table width="480" cellpadding="0" cellspacing="0" style="background:#1f2937;border:1px solid #374151;border-radius:8px;overflow:hidden">
<tr><td style="background:#818cf8;height:4px"></td></tr>
<tr><td style="padding:24px 28px">
<p style="margin:0 0 4px;font-size:11px;text-transform:uppercase;letter-spacing:.08em;color:#9ca3af">Confirm Subscription</p>
<p style="margin:0;font-size:18px;font-weight:600;color:#f9fafb">` + html.EscapeString(sp.Title) + `</p>
</td></tr>
<tr><td style="padding:0 28px 24px">
<p style="margin:0 0 16px;font-size:14px;color:#d1d5db;line-height:1.6">Click the button below to confirm your subscription to status updates.</p>
<a href="` + html.EscapeString(confirmURL) + `" style="display:inline-block;padding:10px 24px;background:#818cf8;color:#fff;font-size:14px;font-weight:600;text-decoration:none;border-radius:6px">Confirm Subscription</a>
</td></tr>
<tr><td style="padding:16px 28px;border-top:1px solid #374151">
<p style="margin:0;font-size:11px;color:#6b7280">If you didn't request this, ignore this email.</p>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`

	port := n.smtp.Port
	if port == 0 {
		switch n.smtp.TLSMode {
		case "smtps":
			port = 465
		case "none":
			port = 25
		default:
			port = 587
		}
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("From: %s\r\n", sanitizeHeader(n.smtp.From)))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", sanitizeHeader(sub.Email)))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", sanitizeHeader(subject)))
	msg.WriteString("MIME-Version: 1.0\r\n")
	msg.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	msg.WriteString("\r\n")
	msg.WriteString(body)

	msgBytes := []byte(msg.String())
	addr := fmt.Sprintf("%s:%d", n.smtp.Host, port)
	host := n.smtp.Host

	switch n.smtp.TLSMode {
	case "smtps":
		return n.sendSMTPS(addr, host, []string{sub.Email}, msgBytes)
	case "none":
		return n.sendPlain(addr, host, []string{sub.Email}, msgBytes)
	default:
		return smtp.SendMail(addr, n.smtpAuth(host), n.smtp.From, []string{sub.Email}, msgBytes)
	}
}
