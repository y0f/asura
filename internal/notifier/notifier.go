package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/y0f/asura/internal/storage"
)

// Sender sends a notification via a specific channel type.
type Sender interface {
	Type() string
	Send(ctx context.Context, channel *storage.NotificationChannel, payload *Payload) error
}

// Payload contains the notification data.
type Payload struct {
	EventType       string                 `json:"event_type"`
	Incident        *storage.Incident      `json:"incident,omitempty"`
	Monitor         *storage.Monitor       `json:"monitor,omitempty"`
	Change          *storage.ContentChange `json:"change,omitempty"`
	EscalationStep  int                    `json:"escalation_step,omitempty"`
	EscalationTotal int                    `json:"escalation_total,omitempty"`
}

type Dispatcher struct {
	store   storage.Store
	senders map[string]Sender
	logger  *slog.Logger
	sem     chan struct{}
}

const maxConcurrentSends = 10

func NewDispatcher(store storage.Store, logger *slog.Logger, allowPrivateTargets bool) *Dispatcher {
	d := &Dispatcher{
		store:   store,
		senders: make(map[string]Sender),
		logger:  logger,
		sem:     make(chan struct{}, maxConcurrentSends),
	}
	d.RegisterSender(&WebhookSender{AllowPrivate: allowPrivateTargets})
	d.RegisterSender(&EmailSender{})
	d.RegisterSender(&TelegramSender{})
	d.RegisterSender(&DiscordSender{})
	d.RegisterSender(&SlackSender{})
	d.RegisterSender(&NtfySender{})
	d.RegisterSender(&TeamsSender{})
	d.RegisterSender(&PagerDutySender{})
	d.RegisterSender(&OpsgenieSender{})
	d.RegisterSender(&PushoverSender{})
	d.RegisterSender(&GoogleChatSender{})
	d.RegisterSender(&MatrixSender{})
	d.RegisterSender(&GotifySender{})
	return d
}

func (d *Dispatcher) RegisterSender(s Sender) {
	d.senders[s.Type()] = s
}

func (d *Dispatcher) NotifyWithPayload(payload *Payload) {
	channels, err := d.store.ListNotificationChannels(context.Background())
	if err != nil {
		d.logger.Error("list notification channels", "error", err)
		return
	}

	for _, ch := range channels {
		if !ch.Enabled || !matchesEvent(ch.Events, payload.EventType) {
			continue
		}

		sender, ok := d.senders[ch.Type]
		if !ok {
			d.logger.Warn("no sender for channel type", "type", ch.Type)
			continue
		}

		go d.sendWithRetry(sender, ch, payload)
	}
}

func (d *Dispatcher) NotifyForMonitor(monitorID int64, payload *Payload) {
	channels, err := d.store.ListNotificationChannels(context.Background())
	if err != nil {
		d.logger.Error("list notification channels", "error", err)
		return
	}

	assignedIDs, err := d.store.GetMonitorNotificationChannelIDs(context.Background(), monitorID)
	if err != nil {
		d.logger.Error("get monitor notification channels", "error", err)
		return
	}

	var allowed map[int64]bool
	if len(assignedIDs) > 0 {
		allowed = make(map[int64]bool, len(assignedIDs))
		for _, id := range assignedIDs {
			allowed[id] = true
		}
	}

	for _, ch := range channels {
		if !ch.Enabled || !matchesEvent(ch.Events, payload.EventType) {
			continue
		}
		if allowed != nil && !allowed[ch.ID] {
			continue
		}
		sender, ok := d.senders[ch.Type]
		if !ok {
			d.logger.Warn("no sender for channel type", "type", ch.Type)
			continue
		}
		go d.sendWithRetry(sender, ch, payload)
	}
}

// NotifyChannels sends a notification to specific channels by ID, bypassing event subscription filters.
func (d *Dispatcher) NotifyChannels(channelIDs []int64, payload *Payload) {
	if len(channelIDs) == 0 {
		return
	}
	channels, err := d.store.ListNotificationChannels(context.Background())
	if err != nil {
		d.logger.Error("list notification channels for escalation", "error", err)
		return
	}
	wanted := make(map[int64]bool, len(channelIDs))
	for _, id := range channelIDs {
		wanted[id] = true
	}
	for _, ch := range channels {
		if !wanted[ch.ID] || !ch.Enabled {
			continue
		}
		sender, ok := d.senders[ch.Type]
		if !ok {
			d.logger.Warn("no sender for channel type", "type", ch.Type)
			continue
		}
		go d.sendWithRetry(sender, ch, payload)
	}
}

func (d *Dispatcher) SendTest(ch *storage.NotificationChannel, inc *storage.Incident) error {
	sender, ok := d.senders[ch.Type]
	if !ok {
		return fmt.Errorf("no sender for type: %s", ch.Type)
	}
	return sender.Send(context.Background(), ch, &Payload{
		EventType: "test",
		Incident:  inc,
	})
}

const (
	maxRetries  = 3
	baseBackoff = 2 * time.Second
)

func (d *Dispatcher) sendWithRetry(sender Sender, ch *storage.NotificationChannel, payload *Payload) {
	defer func() {
		if r := recover(); r != nil {
			d.logger.Error("notification sender panicked",
				"channel_id", ch.ID,
				"channel_type", ch.Type,
				"panic", r,
			)
		}
	}()

	d.sem <- struct{}{}
	defer func() { <-d.sem }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(baseBackoff * time.Duration(1<<(attempt-1)))
		}
		if err := sender.Send(ctx, ch, payload); err != nil {
			lastErr = err
			d.logger.Warn("notification send attempt failed",
				"channel_id", ch.ID,
				"channel_type", ch.Type,
				"attempt", attempt+1,
				"max_attempts", maxRetries,
				"error", err,
			)
			continue
		}
		d.logger.Info("notification sent",
			"channel_id", ch.ID,
			"channel_type", ch.Type,
			"event", payload.EventType,
		)
		d.recordHistory(ch, payload, "sent", "")
		return
	}
	d.logger.Error("notification send failed after retries",
		"channel_id", ch.ID,
		"channel_type", ch.Type,
		"attempts", maxRetries,
		"error", lastErr,
	)
	errMsg := ""
	if lastErr != nil {
		errMsg = lastErr.Error()
	}
	d.recordHistory(ch, payload, "failed", errMsg)
}

func (d *Dispatcher) recordHistory(ch *storage.NotificationChannel, payload *Payload, status, errMsg string) {
	h := &storage.NotificationHistory{
		ChannelID: ch.ID,
		EventType: payload.EventType,
		Status:    status,
		Error:     errMsg,
	}
	if payload.Incident != nil {
		h.IncidentID = &payload.Incident.ID
		if payload.Incident.MonitorID != 0 {
			h.MonitorID = &payload.Incident.MonitorID
		}
	} else if payload.Monitor != nil {
		h.MonitorID = &payload.Monitor.ID
	}
	if err := d.store.InsertNotificationHistory(context.Background(), h); err != nil {
		d.logger.Warn("record notification history", "error", err)
	}
}

func matchesEvent(events []string, eventType string) bool {
	if len(events) == 0 {
		return true
	}
	for _, e := range events {
		if e == eventType {
			return true
		}
	}
	return false
}

func FormatMessage(p *Payload) string {
	switch p.EventType {
	case "incident.created":
		if p.Incident != nil {
			return fmt.Sprintf("[ALERT] Incident #%d opened for %s: %s",
				p.Incident.ID, p.Incident.MonitorName, p.Incident.Cause)
		}
	case "incident.reminder":
		if p.Incident != nil {
			return fmt.Sprintf("[REMINDER] Incident #%d still open for %s: %s",
				p.Incident.ID, p.Incident.MonitorName, p.Incident.Cause)
		}
	case "incident.acknowledged":
		if p.Incident != nil {
			return fmt.Sprintf("[ACK] Incident #%d for %s acknowledged by %s",
				p.Incident.ID, p.Incident.MonitorName, p.Incident.AcknowledgedBy)
		}
	case "incident.resolved":
		if p.Incident != nil {
			return fmt.Sprintf("[RESOLVED] Incident #%d for %s resolved by %s",
				p.Incident.ID, p.Incident.MonitorName, p.Incident.ResolvedBy)
		}
	case "incident.escalated":
		if p.Incident != nil {
			return fmt.Sprintf("[ESCALATION] Step %d/%d for incident #%d (%s): %s",
				p.EscalationStep, p.EscalationTotal, p.Incident.ID, p.Incident.MonitorName, p.Incident.Cause)
		}
	case "content.changed":
		if p.Change != nil {
			return fmt.Sprintf("[CHANGE] Content changed for monitor #%d", p.Change.MonitorID)
		}
	case "sla.breach":
		if p.Monitor != nil {
			return fmt.Sprintf("[SLA] SLA target at risk for %s (target: %.2f%%)", p.Monitor.Name, p.Monitor.SLATarget)
		}
	case "cert.changed":
		if p.Monitor != nil {
			return fmt.Sprintf("[CERT] Certificate fingerprint changed for %s", p.Monitor.Name)
		}
	case "test":
		return "[TEST] This is a test notification from Asura"
	}
	return fmt.Sprintf("[%s] Notification event", p.EventType)
}

func marshalPayload(p *Payload) []byte {
	b, _ := json.Marshal(p)
	return b
}
