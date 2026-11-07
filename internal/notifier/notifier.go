package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/y0f/asura/internal/redact"
	"github.com/y0f/asura/internal/storage"
)

// Sender implementations must honor context cancellation. RegisterSender is
// concurrency-safe; a queued send retains the sender selected at admission.
type Sender interface {
	Type() string
	Send(context.Context, *storage.NotificationChannel, *Payload) error
}

type Payload struct {
	EventType       string                 `json:"event_type"`
	Incident        *storage.Incident      `json:"incident,omitempty"`
	Monitor         *storage.Monitor       `json:"monitor,omitempty"`
	Change          *storage.ContentChange `json:"change,omitempty"`
	EscalationStep  int                    `json:"escalation_step,omitempty"`
	EscalationTotal int                    `json:"escalation_total,omitempty"`
}

type Dispatcher struct {
	store          storage.Store
	senderMu       sync.RWMutex
	senders        map[string]Sender
	logger         *slog.Logger
	queue          *deliveryQueue
	attemptTimeout time.Duration
	baseBackoff    time.Duration
}

const (
	maxRetries            = 3
	defaultAttemptTimeout = 10 * time.Second
	defaultBaseBackoff    = 2 * time.Second
)

func NewDispatcher(store storage.Store, logger *slog.Logger, allowPrivateTargets bool) *Dispatcher {
	d := &Dispatcher{store: store, senders: make(map[string]Sender), logger: logger,
		queue: newDeliveryQueue(), attemptTimeout: defaultAttemptTimeout, baseBackoff: defaultBaseBackoff}
	d.RegisterSender(&WebhookSender{AllowPrivate: allowPrivateTargets})
	d.RegisterSender(&EmailSender{})
	d.RegisterSender(&TelegramSender{})
	d.RegisterSender(&DiscordSender{AllowPrivate: allowPrivateTargets})
	d.RegisterSender(&SlackSender{AllowPrivate: allowPrivateTargets})
	d.RegisterSender(&NtfySender{AllowPrivate: allowPrivateTargets})
	d.RegisterSender(&TeamsSender{AllowPrivate: allowPrivateTargets})
	d.RegisterSender(&PagerDutySender{})
	d.RegisterSender(&OpsgenieSender{})
	d.RegisterSender(&PushoverSender{})
	d.RegisterSender(&GoogleChatSender{AllowPrivate: allowPrivateTargets})
	d.RegisterSender(&MatrixSender{AllowPrivate: allowPrivateTargets})
	d.RegisterSender(&GotifySender{AllowPrivate: allowPrivateTargets})
	return d
}

func (d *Dispatcher) RegisterSender(s Sender) {
	d.senderMu.Lock()
	defer d.senderMu.Unlock()
	d.senders[s.Type()] = s
}

func (d *Dispatcher) sender(kind string) Sender {
	d.senderMu.RLock()
	defer d.senderMu.RUnlock()
	return d.senders[kind]
}

func (d *Dispatcher) Shutdown(ctx context.Context) error { return d.queue.shutdown(ctx) }
func (d *Dispatcher) Stats() DeliveryStats               { return d.queue.stats() }

// The caller context bounds lookup and admission. Accepted alerts use the
// dispatcher's lifetime so an HTTP response does not cancel their delivery.
func (d *Dispatcher) NotifyWithPayload(ctx context.Context, payload *Payload) error {
	return d.notify(ctx, payload, 0, nil)
}

func (d *Dispatcher) NotifyForMonitor(ctx context.Context, monitorID int64, payload *Payload) error {
	return d.notify(ctx, payload, monitorID, nil)
}

// NotifyChannels bypasses event filters for explicit escalation recipients.
func (d *Dispatcher) NotifyChannels(ctx context.Context, ids []int64, payload *Payload) error {
	if len(ids) == 0 {
		return nil
	}
	wanted := make(map[int64]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	return d.notify(ctx, payload, 0, wanted)
}

func (d *Dispatcher) notify(ctx context.Context, payload *Payload, monitorID int64, wanted map[int64]bool) error {
	ctx, finish, err := d.queue.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	channels, err := d.store.ListNotificationChannels(ctx)
	if err != nil {
		return fmt.Errorf("list notification channels: %w", err)
	}
	var assigned map[int64]bool
	if monitorID > 0 {
		ids, err := d.store.GetMonitorNotificationChannelIDs(ctx, monitorID)
		if err != nil {
			return fmt.Errorf("get monitor notification channels: %w", err)
		}
		if len(ids) > 0 {
			assigned = make(map[int64]bool, len(ids))
			for _, id := range ids {
				assigned[id] = true
			}
		}
	}
	var firstErr error
	var accountingCtx context.Context
	for _, ch := range channels {
		if !ch.Enabled || (wanted != nil && !wanted[ch.ID]) || (assigned != nil && !assigned[ch.ID]) ||
			(wanted == nil && !matchesEvent(ch.Events, payload.EventType)) {
			continue
		}
		ch, payload := snapshotChannel(ch), snapshotPayload(payload)
		sender := d.sender(ch.Type)
		var err error
		if sender == nil {
			err = fmt.Errorf("no sender for type: %s", ch.Type)
			d.queue.rejected.Add(1)
		} else {
			err = d.queue.offer(ctx, deliveryJob{
				run:  func(ctx context.Context) error { return d.sendWithRetry(ctx, sender, ch, payload) },
				done: func(err error) { d.recordResult(ch, payload, err) },
			})
		}
		if err != nil {
			// One budget for all rejected recipients, even after caller
			// cancellation. A blocked database cannot cost two seconds per
			// configured channel while shutdown waits for this admission.
			if accountingCtx == nil {
				batchCtx, cancel := context.WithTimeout(context.Background(), historyTimeout)
				defer cancel()
				accountingCtx = batchCtx
			}
			d.recordResultContext(accountingCtx, ch, payload, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// SendTest uses the same bounded queue and concurrency as ordinary alerts,
// but cancels when the request ends and makes one time-limited attempt.
func (d *Dispatcher) SendTest(ctx context.Context, ch *storage.NotificationChannel, inc *storage.Incident) error {
	ctx, finish, err := d.queue.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	sender := d.sender(ch.Type)
	if sender == nil {
		return fmt.Errorf("no sender for type: %s", ch.Type)
	}
	ch = snapshotChannel(ch)
	payload := snapshotPayload(&Payload{EventType: "test", Incident: inc})
	result := make(chan error, 1)
	err = d.queue.offer(ctx, deliveryJob{
		run: func(workerCtx context.Context) error {
			sendCtx, cancel := context.WithTimeout(ctx, d.attemptTimeout)
			stop := context.AfterFunc(workerCtx, cancel)
			defer func() { stop(); cancel() }()
			if err := sendCtx.Err(); err != nil {
				return err
			}
			return sender.Send(sendCtx, ch, payload)
		},
		done: func(err error) { d.recordResult(ch, payload, err); result <- err },
	})
	if err != nil {
		d.recordResult(ch, payload, err)
		return err
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Dispatcher) sendWithRetry(ctx context.Context, sender Sender, ch *storage.NotificationChannel, payload *Payload) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt > 0 {
			timer := time.NewTimer(d.baseBackoff * time.Duration(1<<(attempt-1)))
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		attemptCtx, cancel := context.WithTimeout(ctx, d.attemptTimeout)
		err := sender.Send(attemptCtx, ch, payload)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		d.logger.Warn("notification send attempt failed", "channel_id", ch.ID,
			"channel_type", ch.Type, "attempt", attempt+1, "error", redactErr(err))
	}
	return lastErr
}

func (d *Dispatcher) recordResult(ch *storage.NotificationChannel, payload *Payload, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), historyTimeout)
	defer cancel()
	d.recordResultContext(ctx, ch, payload, err)
}

func (d *Dispatcher) recordResultContext(ctx context.Context, ch *storage.NotificationChannel, payload *Payload, err error) {
	status, message := "sent", ""
	if err != nil {
		status, message = "failed", redactErr(err)
		d.logger.Error("notification delivery failed", "channel_id", ch.ID, "channel_type", ch.Type,
			"event", payload.EventType, "error", message)
	}
	h := &storage.NotificationHistory{ChannelID: ch.ID, EventType: payload.EventType, Status: status, Error: message}
	if payload.Incident != nil {
		h.IncidentID = &payload.Incident.ID
		if payload.Incident.MonitorID != 0 {
			h.MonitorID = &payload.Incident.MonitorID
		}
	} else if payload.Monitor != nil {
		h.MonitorID = &payload.Monitor.ID
	}
	if err := ctx.Err(); err != nil {
		d.logger.Error("notification history budget exhausted", "channel_id", ch.ID, "error", err)
		return
	}
	if err := d.store.InsertNotificationHistory(ctx, h); err != nil {
		d.logger.Error("record notification history", "channel_id", ch.ID, "error", err)
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

func marshalPayload(p *Payload) ([]byte, error) {
	if p != nil {
		copy := *p
		copy.Monitor = redact.Monitor(p.Monitor)
		p = &copy
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}
	return b, nil
}
