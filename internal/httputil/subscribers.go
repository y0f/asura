package httputil

import (
	"context"
	"net"
	"net/mail"
	"net/url"
	"strings"

	"github.com/y0f/asura/v2/internal/safenet"
)

type SubscriptionInput struct {
	Type       string `json:"type"`
	Email      string `json:"email"`
	WebhookURL string `json:"webhook_url"`
}

// Validate applies the same destination policy to public JSON and web forms.
// Delivery still checks resolved addresses at dial time to prevent DNS rebinding.
func (input *SubscriptionInput) Validate(ctx context.Context, allowPrivate bool) string {
	input.Email, input.WebhookURL = strings.TrimSpace(input.Email), strings.TrimSpace(input.WebhookURL)
	switch input.Type {
	case "email":
		input.WebhookURL = ""
		if input.Email == "" {
			return "email is required"
		}
		if len(input.Email) > 254 {
			return "email is too long"
		}
		if _, err := mail.ParseAddress(input.Email); err != nil {
			return "invalid email address"
		}
	case "webhook":
		input.Email = ""
		if input.WebhookURL == "" {
			return "webhook_url is required"
		}
		if len(input.WebhookURL) > 2048 {
			return "webhook_url is too long"
		}
		u, err := url.Parse(input.WebhookURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" {
			return "webhook_url must be a valid https URL"
		}
		if !allowPrivate && !subscriberHostAllowed(ctx, u.Hostname()) {
			return "webhook_url host is not allowed"
		}
	default:
		return "type must be 'email' or 'webhook'"
	}
	return ""
}

func subscriberHostAllowed(ctx context.Context, host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return !safenet.IsPrivateIP(ip)
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if safenet.IsPrivateIP(ip.IP) {
			return false
		}
	}
	return true
}
