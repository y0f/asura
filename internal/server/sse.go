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

type sseClient struct {
	ch chan SSEEvent
}

type EventBroker struct {
	mu      sync.RWMutex
	clients map[*sseClient]struct{}
	logger  *slog.Logger
}

const (
	maxSSEClients    = 100
	clientBufferSize = 16
	sseHeartbeat     = 30 * time.Second
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
	if len(b.clients) >= maxSSEClients {
		return nil
	}
	c := &sseClient{ch: make(chan SSEEvent, clientBufferSize)}
	b.clients[c] = struct{}{}
	return c
}

func (b *EventBroker) Unsubscribe(c *sseClient) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.clients, c)
	close(c.ch)
}

func (b *EventBroker) Broadcast(event SSEEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for c := range b.clients {
		select {
		case c.ch <- event:
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
		case event, ok := <-client.ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
