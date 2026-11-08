package notifier

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/storage"
)

func TestAutomaticDeliverySnapshotDoesNotExportDiagnostics(t *testing.T) {
	payload := &Payload{EventType: "content.changed",
		Incident: &storage.Incident{ID: 1, Cause: "private-check-error"},
		Monitor:  &storage.Monitor{ID: 2, Type: "http", Assertions: json.RawMessage(`{"private":"assertion"}`)},
		Change:   &storage.ContentChange{ID: 3, MonitorID: 2, Diff: "private-diff", OldHash: "private-hash", NewHash: "private-hash", OldBody: "private-body", NewBody: "private-body"},
	}
	projected := snapshotPayload(payload)
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private") || projected.Change.ID != 3 || projected.Change.MonitorID != 2 || projected.Incident.ID != 1 {
		t.Fatalf("automatic notification leaked diagnostics or lost references: %s", encoded)
	}
	if payload.Incident.Cause != "private-check-error" || payload.Change.Diff != "private-diff" || len(payload.Monitor.Assertions) == 0 {
		t.Fatal("safe delivery changed stored diagnostic data")
	}
}

type diagnosticSubscriberStore struct {
	subscriberBatchStore
	url string
}

func (s *diagnosticSubscriberStore) ListConfirmedSubscribersAfter(context.Context, int64, int64, int) ([]*storage.StatusPageSubscriber, error) {
	return []*storage.StatusPageSubscriber{{ID: 1, Type: "webhook", WebhookURL: s.url}}, nil
}

func TestSubscriberDeliveryOmitsPrivateIncidentCause(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	store := &diagnosticSubscriberStore{url: server.URL}
	n := NewSubscriberNotifier(store, config.SMTPConfig{}, "https://status.test", slog.New(slog.NewTextHandler(io.Discard, nil)), true)
	defer n.Shutdown(context.Background())
	inc := &storage.Incident{ID: 1, MonitorID: 2, Cause: "private-check-marker", Status: "open"}
	if err := n.NotifyForMonitor(context.Background(), 2, "incident.created", inc, &storage.Monitor{ID: 2, Name: "public name"}); err != nil {
		t.Fatal(err)
	}
	if err := n.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-received:
		if strings.Contains(body, "private-check-marker") || !strings.Contains(body, "public name") {
			t.Fatalf("public subscriber boundary: %s", body)
		}
	default:
		t.Fatal("subscriber webhook was not delivered")
	}
	if inc.Cause != "private-check-marker" {
		t.Fatal("delivery modified source incident")
	}
}
