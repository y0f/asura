package notifier

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestWebhookPayloadDoesNotIncludeMonitorCredentials(t *testing.T) {
	payload := &Payload{EventType: "incident.created", Monitor: &storage.Monitor{ID: 1, Name: "endpoint", Settings: json.RawMessage(`{"bearer_token":"private-token","headers":{"Any-Name":"private-header"}}`)}}
	body, err := marshalPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private-") || !strings.Contains(string(body), "endpoint") {
		t.Fatalf("unexpected notification payload: %s", body)
	}
	if !strings.Contains(string(payload.Monitor.Settings), "private-token") {
		t.Fatal("notification redaction changed the source monitor")
	}
}
