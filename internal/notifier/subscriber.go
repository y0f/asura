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
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/redact"
	"github.com/y0f/asura/v3/internal/storage"
)

type SubscriberNotifier struct {
	store        storage.Store
	smtp         config.SMTPConfig
	extURL       string
	logger       *slog.Logger
	queue        *deliveryQueue
	allowPrivate bool
}

func NewSubscriberNotifier(store storage.Store, smtpCfg config.SMTPConfig, extURL string, logger *slog.Logger, allowPrivate bool) *SubscriberNotifier {
	return &SubscriberNotifier{
		store:        store,
		smtp:         smtpCfg,
		extURL:       strings.TrimRight(extURL, "/"),
		logger:       logger,
		queue:        newDeliveryQueue(),
		allowPrivate: allowPrivate,
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

func (n *SubscriberNotifier) Shutdown(ctx context.Context) error { return n.queue.shutdown(ctx) }
func (n *SubscriberNotifier) Stats() DeliveryStats               { return n.queue.stats() }

func (n *SubscriberNotifier) NotifyForMonitor(ctx context.Context, monitorID int64, eventType string, inc *storage.Incident, mon *storage.Monitor) error {
	ctx, finish, err := n.queue.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	pageIDs, err := n.store.GetStatusPageIDsForMonitor(ctx, monitorID)
	if err != nil {
		return fmt.Errorf("subscriber status pages: %w", err)
	}
	// Only the public display fields are needed by subscriber messages.
	if mon != nil {
		mon = &storage.Monitor{ID: mon.ID, Name: mon.Name}
	}
	if inc != nil {
		inc = &storage.Incident{ID: inc.ID, MonitorID: inc.MonitorID, MonitorName: inc.MonitorName,
			Status: inc.Status, Cause: redact.IncidentCause(), StartedAt: inc.StartedAt}
	}
	var firstErr error
	for _, pageID := range pageIDs {
		sp, err := n.store.GetStatusPage(ctx, pageID)
		if err != nil {
			return fmt.Errorf("subscriber status page: %w", err)
		}
		if sp == nil || !sp.Enabled {
			continue
		}
		sp = publicPage(sp)
		subject := subscriberSubject(eventType, mon, inc)
		var afterID int64
		for {
			subs, err := n.store.ListConfirmedSubscribersAfter(ctx, sp.ID, afterID, maxQueuedSends)
			if err != nil {
				return fmt.Errorf("list subscriber batch: %w", err)
			}
			for _, sub := range subs {
				sub := copyPointer(sub)
				err := n.queue.offer(ctx, deliveryJob{
					run: func(ctx context.Context) error {
						ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
						defer cancel()
						switch sub.Type {
						case "email":
							return n.sendSubscriberEmail(ctx, sub, sp, eventType, subject, inc, mon)
						case "webhook":
							return n.sendSubscriberWebhook(ctx, sub, sp, eventType, inc, mon)
						default:
							return fmt.Errorf("unknown subscriber type: %s", sub.Type)
						}
					},
					done: func(err error) { n.recordResult(sub, err) },
				})
				if err != nil {
					n.recordResult(sub, err)
					if firstErr == nil {
						firstErr = err
					}
				}
				afterID = sub.ID
			}
			if len(subs) < maxQueuedSends {
				break
			}
		}
	}
	return firstErr
}

func publicPage(sp *storage.StatusPage) *storage.StatusPage {
	return &storage.StatusPage{ID: sp.ID, Slug: sp.Slug, Title: sp.Title}
}

func (n *SubscriberNotifier) recordResult(sub *storage.StatusPageSubscriber, err error) {
	if err != nil {
		n.logger.Error("subscriber delivery failed", "page_id", sub.StatusPageID,
			"subscriber_id", sub.ID, "type", sub.Type, "error", redactErr(err))
	}
}

// QueueConfirmationEmail admits a confirmation into the owned delivery queue.
// Once admitted, it outlives the request and is joined during shutdown.
func (n *SubscriberNotifier) QueueConfirmationEmail(ctx context.Context, sub *storage.StatusPageSubscriber, sp *storage.StatusPage) error {
	ctx, finish, err := n.queue.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if n.smtp.Host == "" || n.smtp.From == "" {
		return fmt.Errorf("subscription SMTP not configured")
	}
	sub, sp = copyPointer(sub), publicPage(sp)
	err = n.queue.offer(ctx, deliveryJob{
		run: func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			return n.SendConfirmationEmail(ctx, sub, sp)
		},
		done: func(err error) { n.recordResult(sub, err) },
	})
	if err != nil {
		n.recordResult(sub, err)
	}
	return err
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
		return n.sendSMTPS(ctx, addr, host, []string{sub.Email}, msgBytes)
	case "none":
		return n.sendPlain(ctx, addr, host, []string{sub.Email}, msgBytes)
	default:
		return n.sendSTARTTLS(ctx, addr, host, []string{sub.Email}, msgBytes)
	}
}

func (n *SubscriberNotifier) sendSMTPS(ctx context.Context, addr, host string, rcpt []string, msg []byte) error {
	conn, err := dialSMTP(ctx, addr)
	if err != nil {
		return fmt.Errorf("smtps dial: %w", err)
	}
	tconn := tls.Client(conn, &tls.Config{ServerName: host})
	if err := tconn.HandshakeContext(ctx); err != nil {
		conn.Close()
		return fmt.Errorf("smtps handshake: %w", err)
	}
	client, err := smtp.NewClient(tconn, host)
	if err != nil {
		tconn.Close()
		return fmt.Errorf("smtps client: %w", err)
	}
	defer client.Close()
	return n.sendViaClient(client, rcpt, msg)
}

func (n *SubscriberNotifier) sendPlain(ctx context.Context, addr, host string, rcpt []string, msg []byte) error {
	client, err := newSMTPClient(ctx, addr, host)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer client.Close()
	return n.sendViaClient(client, rcpt, msg)
}

func (n *SubscriberNotifier) sendSTARTTLS(ctx context.Context, addr, host string, rcpt []string, msg []byte) error {
	client, err := newSMTPClient(ctx, addr, host)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer client.Close()
	if err := startTLS(client, host, n.smtp.TLSMode == TLSModeSTARTTLSRequired); err != nil {
		return err
	}
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
	req.Header.Set("User-Agent", "Asura/2.0")

	resp, err := newHTTPClient(n.allowPrivate).Do(req)
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func buildSubscriberEmailBody(pageTitle, eventType, subject string, inc *storage.Incident, mon *storage.Monitor, unsubURL string) string {
	statusColor := "#b0b0b0"
	eventLabel := eventType
	detail := html.EscapeString(subject)

	switch eventType {
	case "incident.created":
		statusColor = "#69b3ff"
		eventLabel = "Incident Opened"
		if inc != nil {
			detail = html.EscapeString(inc.MonitorName) + ": " + html.EscapeString(inc.Cause)
		}
	case "incident.resolved":
		statusColor = "#b0b0b0"
		eventLabel = "Incident Resolved"
		if inc != nil {
			detail = html.EscapeString(inc.MonitorName) + " is back to normal"
		}
	}

	return `<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#111111;font-family:system-ui,-apple-system,sans-serif">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#111111;padding:24px 16px;box-sizing:border-box">
<tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="width:100%;max-width:480px;table-layout:fixed;background:#1a1a1a;border:1px solid #3d3d3d;border-radius:8px;overflow:hidden">
<tr><td style="background:` + statusColor + `;height:4px"></td></tr>
<tr><td style="padding:24px 20px">
<p style="margin:0 0 4px;font-size:12px;text-transform:uppercase;letter-spacing:.08em;color:#b0b0b0">` + html.EscapeString(eventLabel) + `</p>
<h1 style="margin:0;font-size:18px;font-weight:600;color:#ffffff;line-height:1.4;overflow-wrap:anywhere">` + html.EscapeString(pageTitle) + `</h1>
</td></tr>
<tr><td style="padding:0 20px 24px">
<p style="margin:0;font-size:14px;color:#dedede;line-height:1.6;overflow-wrap:anywhere">` + detail + `</p>
</td></tr>
<tr><td style="padding:16px 20px;border-top:1px solid #3d3d3d">
<p style="margin:0;font-size:12px;color:#b0b0b0;line-height:1.6">
<a href="` + html.EscapeString(unsubURL) + `" style="display:inline-block;padding:6px 0;color:#b0b0b0;text-decoration:underline">Unsubscribe</a>
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

	body := buildSubscriptionConfirmationBody(sp.Title, confirmURL)

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
		return n.sendSMTPS(ctx, addr, host, []string{sub.Email}, msgBytes)
	case "none":
		return n.sendPlain(ctx, addr, host, []string{sub.Email}, msgBytes)
	default:
		return n.sendSTARTTLS(ctx, addr, host, []string{sub.Email}, msgBytes)
	}
}

func buildSubscriptionConfirmationBody(pageTitle, confirmURL string) string {
	return `<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:#111111;font-family:system-ui,-apple-system,sans-serif">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#111111;padding:24px 16px;box-sizing:border-box">
<tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="width:100%;max-width:480px;table-layout:fixed;background:#1a1a1a;border:1px solid #3d3d3d;border-radius:8px;overflow:hidden">
<tr><td style="background:#005bbf;height:4px"></td></tr>
<tr><td style="padding:24px 20px">
<p style="margin:0 0 4px;font-size:12px;text-transform:uppercase;letter-spacing:.08em;color:#b0b0b0">Confirm Subscription</p>
<h1 style="margin:0;font-size:18px;font-weight:600;color:#ffffff;line-height:1.4;overflow-wrap:anywhere">` + html.EscapeString(pageTitle) + `</h1>
</td></tr>
<tr><td style="padding:0 20px 24px">
<p style="margin:0 0 16px;font-size:14px;color:#dedede;line-height:1.6;overflow-wrap:anywhere">Click the button below to confirm your subscription to status updates.</p>
<a href="` + html.EscapeString(confirmURL) + `" style="display:inline-block;max-width:100%;box-sizing:border-box;padding:12px 16px;line-height:1.5;background:#005bbf;color:#fff;font-size:14px;font-weight:600;text-decoration:none;border-radius:6px">Confirm Subscription</a>
</td></tr>
<tr><td style="padding:16px 20px;border-top:1px solid #3d3d3d">
<p style="margin:0;font-size:12px;color:#b0b0b0;line-height:1.6">If you didn't request this, ignore this email.</p>
</td></tr>
</table>
</td></tr>
</table>
</body>
</html>`
}
