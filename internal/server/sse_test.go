package server

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestEventBrokerSubscribeUnsubscribe(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	broker := NewEventBroker(logger)

	c1 := broker.Subscribe()
	if c1 == nil {
		t.Fatal("expected non-nil client")
	}
	if broker.ClientCount() != 1 {
		t.Fatalf("expected 1 client, got %d", broker.ClientCount())
	}

	c2 := broker.Subscribe()
	if broker.ClientCount() != 2 {
		t.Fatalf("expected 2 clients, got %d", broker.ClientCount())
	}

	broker.Unsubscribe(c1)
	if broker.ClientCount() != 1 {
		t.Fatalf("expected 1 client after unsubscribe, got %d", broker.ClientCount())
	}

	broker.Unsubscribe(c2)
	if broker.ClientCount() != 0 {
		t.Fatalf("expected 0 clients, got %d", broker.ClientCount())
	}
}

func TestEventBrokerBroadcast(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	broker := NewEventBroker(logger)

	c1 := broker.Subscribe()
	c2 := broker.Subscribe()

	event := SSEEvent{Type: "incident.created", Data: map[string]string{"id": "1"}}
	broker.Broadcast(event)

	select {
	case got := <-c1.ch:
		if got.Type != "incident.created" {
			t.Fatalf("c1: expected incident.created, got %s", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("c1: timeout waiting for event")
	}

	select {
	case got := <-c2.ch:
		if got.Type != "incident.created" {
			t.Fatalf("c2: expected incident.created, got %s", got.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("c2: timeout waiting for event")
	}

	broker.Unsubscribe(c1)
	broker.Unsubscribe(c2)
}

func TestEventBrokerMaxClients(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	broker := NewEventBroker(logger)

	clients := make([]*sseClient, 0, maxSSEClients)
	for i := 0; i < maxSSEClients; i++ {
		c := broker.Subscribe()
		if c == nil {
			t.Fatalf("subscribe failed at client %d", i)
		}
		clients = append(clients, c)
	}

	if broker.ClientCount() != maxSSEClients {
		t.Fatalf("expected %d clients, got %d", maxSSEClients, broker.ClientCount())
	}

	extra := broker.Subscribe()
	if extra != nil {
		t.Fatal("expected nil when max clients reached")
	}

	broker.Unsubscribe(clients[0])
	c := broker.Subscribe()
	if c == nil {
		t.Fatal("expected subscribe to succeed after unsubscribe")
	}

	for _, cl := range clients[1:] {
		broker.Unsubscribe(cl)
	}
	broker.Unsubscribe(c)
}

func TestEventBrokerDropsSlowClient(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	broker := NewEventBroker(logger)

	c := broker.Subscribe()

	for i := 0; i < clientBufferSize+5; i++ {
		broker.Broadcast(SSEEvent{Type: "test", Data: i})
	}

	count := 0
	for {
		select {
		case <-c.ch:
			count++
		default:
			goto done
		}
	}
done:
	if count != clientBufferSize {
		t.Fatalf("expected %d buffered events, got %d", clientBufferSize, count)
	}

	broker.Unsubscribe(c)
}
