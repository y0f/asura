package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type SSEEvent struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// sseClient receives fully formatted SSE frames. Events are marshalled once in
// Broadcast, not once per connected client.
type sseClient struct {
	ch chan []byte
}

type EventBroker struct {
	mu      sync.RWMutex
	clients map[*sseClient]struct{}
	closed  bool
	logger  *slog.Logger
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
		clients: make(map[*sseClient]struct{}),
		logger:  logger,
	}
}

func (b *EventBroker) Subscribe() *sseClient {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(b.clients) >= maxSSEClients {
		return nil
	}
	c := &sseClient{ch: make(chan []byte, clientBufferSize)}
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
	data, err := json.Marshal(event.Data)
	if err != nil {
		b.logger.Error("sse: marshal event", "type", event.Type, "error", err)
		return
	}
	frame := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event.Type, data))

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
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	ticker := time.NewTicker(sseHeartbeat)
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
			if _, err := w.Write(frame); err != nil {
				return
			}
			flusher.Flush()
			extendDeadline()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
			extendDeadline()
		}
	}
}
