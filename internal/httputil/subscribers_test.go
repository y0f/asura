package httputil

import (
	"context"
	"strings"
	"testing"
)

func TestSubscriptionInputUsesSharedDestinationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                string
		input               SubscriptionInput
		allowPrivate, valid bool
	}{
		{"email", SubscriptionInput{Type: "email", Email: " fixture@example.test ", WebhookURL: "discard-unused-private-field"}, false, true},
		{"missing email", SubscriptionInput{Type: "email"}, false, false},
		{"oversized email", SubscriptionInput{Type: "email", Email: strings.Repeat("a", 255) + "@example.test"}, false, false},
		{"invalid email", SubscriptionInput{Type: "email", Email: "invalid"}, false, false},
		{"public https", SubscriptionInput{Type: "webhook", WebhookURL: "https://1.1.1.1/hook", Email: "unused@example.test"}, false, true},
		{"private blocked", SubscriptionInput{Type: "webhook", WebhookURL: "https://127.0.0.1/hook"}, false, false},
		{"private permitted", SubscriptionInput{Type: "webhook", WebhookURL: "https://127.0.0.1/hook"}, true, true},
		{"plaintext", SubscriptionInput{Type: "webhook", WebhookURL: "http://1.1.1.1/hook"}, true, false},
		{"oversized webhook", SubscriptionInput{Type: "webhook", WebhookURL: "https://1.1.1.1/" + strings.Repeat("x", 2048)}, false, false},
		{"unknown type", SubscriptionInput{Type: "other"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.input.Validate(context.Background(), tc.allowPrivate)
			if (msg == "") != tc.valid {
				t.Fatalf("valid=%v message=%q", tc.valid, msg)
			}
			if tc.valid && (tc.input.Type == "email" && tc.input.WebhookURL != "" || tc.input.Type == "webhook" && tc.input.Email != "") {
				t.Fatal("unused destination field retained")
			}
		})
	}
}
