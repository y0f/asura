package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// SSEEvent contains only refresh metadata. Keeping notification payloads out of
// this type prevents settings, credentials, response bodies and names entering
// the stream by accident.
type SSEEvent struct {
	Type       string `json:"-"`
	MonitorID  int64  `json:"monitor_id"`
	IncidentID int64  `json:"incident_id,omitempty"`
	Status     string `json:"status,omitempty"`
}

func (e SSEEvent) permission() string {
	switch e.Type {
	case "incident.created", "incident.acknowledged", "incident.resolved", "incident.reminder", "incident.escalated":
		return "incidents.read"
	case "content.changed", "cert.changed":
		return "monitors.read"
	case "sla.breach":
		return "sla.read"
	default:
		return ""
	}
}

type sseFrame struct {
	event SSEEvent
	data  []byte
}

// Frames are marshalled once, then authorized again when each client drains
// its queue. A revocation also applies to already-buffered events.
type sseClient struct {
	ch chan sseFrame
}

type EventBroker struct {
	mu                sync.RWMutex
	clients           map[*sseClient]struct{}
	closed            bool
	logger            *slog.Logger
	authorize         func(*http.Request, *SSEEvent) (bool, error)
	heartbeatInterval time.Duration
}

const (
	maxSSEClients    = 100
	clientBufferSize = 16
	sseHeartbeat     = 30 * time.Second
	// sseWriteWindow bounds each write on a stream. It is renewed after every
	// write and outlasts the heartbeat interval, so a healthy stream is never
	// cut by the server-wide WriteTimeout while a stuck client still is.
	sseWriteWindow = sseHeartbeat + 15*time.Second
)

func NewEventBroker(logger *slog.Logger) *EventBroker {
	return &EventBroker{
		clients:           make(map[*sseClient]struct{}),
		logger:            logger,
		heartbeatInterval: sseHeartbeat,
	}
}

func (b *EventBroker) Subscribe() *sseClient {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(b.clients) >= maxSSEClients {
		return nil
	}
	c := &sseClient{ch: make(chan sseFrame, clientBufferSize)}
	b.clients[c] = struct{}{}
	return c
}

func (b *EventBroker) Unsubscribe(c *sseClient) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.clients[c]; !ok {
		return
	}
	delete(b.clients, c)
	close(c.ch)
}

// Close disconnects all streaming clients and rejects new ones. Register it
// with http.Server.RegisterOnShutdown so graceful shutdown is not held up by
// open SSE connections.
func (b *EventBroker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for c := range b.clients {
		delete(b.clients, c)
		close(c.ch)
	}
}

func (b *EventBroker) Broadcast(event SSEEvent) {
	if event.MonitorID <= 0 || event.permission() == "" {
		return
	}
	if event.permission() == "incidents.read" && event.IncidentID <= 0 {
		return
	}
	if event.permission() != "incidents.read" {
		event.IncidentID, event.Status = 0, ""
	} else {
		switch event.Status {
		case "open", "acknowledged", "resolved":
		default:
			event.Status = ""
		}
	}
	data, err := json.Marshal(event)
	if err != nil {
		b.logger.Error("sse: marshal event", "type", event.Type, "error", err)
		return
	}
	frame := sseFrame{event: event, data: []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, data))}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for c := range b.clients {
		select {
		case c.ch <- frame:
		default:
		}
	}
}

func (b *EventBroker) ClientCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.clients)
}

func (b *EventBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if b.authorize == nil {
		http.Error(w, "stream authorization unavailable", http.StatusServiceUnavailable)
		return
	}
	allowed, err := b.authorize(r, nil)
	if err != nil {
		code := http.StatusServiceUnavailable
		if errors.Is(err, errStreamIdentity) {
			code = http.StatusUnauthorized
		}
		http.Error(w, "stream authorization failed", code)
		return
	}
	if !allowed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	client := b.Subscribe()
	if client == nil {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	defer b.Unsubscribe(client)

	// The server's WriteTimeout is meant for ordinary requests; without
	// extending it the stream is killed before the first heartbeat.
	rc := http.NewResponseController(w)
	extendDeadline := func() {
		_ = rc.SetWriteDeadline(time.Now().Add(sseWriteWindow))
	}
	extendDeadline()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	ticker := time.NewTicker(b.heartbeatInterval)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-client.ch:
			if !ok {
				return
			}
			allowed, err := b.authorize(r, &frame.event)
			if err != nil {
				return
			}
			if !allowed {
				continue
			}
			if _, err := w.Write(frame.data); err != nil {
				return
			}
			flusher.Flush()
			extendDeadline()
		case <-ticker.C:
			if allowed, err := b.authorize(r, nil); err != nil || !allowed {
				return
			}
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
			extendDeadline()
		}
	}
}
