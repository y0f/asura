package notifier

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestWebhookSender(t *testing.T) {
	var receivedBody []byte
	var receivedSig string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		receivedSig = r.Header.Get("X-Asura-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	secret := "test-secret"
	settings, _ := json.Marshal(WebhookSettings{
		URL:    server.URL,
		Secret: secret,
	})

	ch := &storage.NotificationChannel{
		ID:       1,
		Name:     "Test Webhook",
		Type:     "webhook",
		Settings: settings,
	}

	payload := &Payload{
		EventType: "test",
		Incident: &storage.Incident{
			ID:          1,
			MonitorName: "Test Monitor",
			Status:      "open",
			Cause:       "test cause",
		},
	}

	sender := &WebhookSender{AllowPrivate: true}
	err := sender.Send(context.Background(), ch, payload)
	if err != nil {
		t.Fatal(err)
	}

	// Verify body was received
	if len(receivedBody) == 0 {
		t.Fatal("no body received")
	}

	// Verify HMAC signature
	if receivedSig == "" {
		t.Fatal("no signature received")
	}
	if !strings.HasPrefix(receivedSig, "sha256=") {
		t.Fatal("signature should start with sha256=")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(receivedBody)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if receivedSig != expectedSig {
		t.Fatalf("signature mismatch: got %s, expected %s", receivedSig, expectedSig)
	}
}

func TestWebhookSenderFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	settings, _ := json.Marshal(WebhookSettings{URL: server.URL})
	ch := &storage.NotificationChannel{Settings: settings}
	payload := &Payload{EventType: "test"}

	sender := &WebhookSender{AllowPrivate: true}
	err := sender.Send(context.Background(), ch, payload)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}
