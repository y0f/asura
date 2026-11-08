package httputil

import (
	"errors"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestStatusPageInputPreservesOmittedSecretsAndLegacyBranding(t *testing.T) {
	current := &storage.StatusPage{Title: "Page", Slug: "page", Enabled: true, PasswordHash: "current-password", AnalyticsScript: "legacy script", LogoURL: "http://legacy.example/logo", CustomHeaderHTML: "legacy header"}
	title := "Renamed"
	input := StatusPageInput{Title: &title}
	next, members, err := input.Apply(current, false)
	if err != nil || members != nil || next.Title != title || next.PasswordHash != current.PasswordHash || next.AnalyticsScript != current.AnalyticsScript || next.LogoURL != current.LogoURL || !next.Enabled || current.Title != "Page" {
		t.Fatalf("omitted values lost or current mutated: %+v %v", next, err)
	}
	sameScript := current.AnalyticsScript
	next, _, err = (StatusPageInput{AnalyticsScript: &sameScript}).Apply(current, false)
	if err != nil || next.AnalyticsScript != sameScript {
		t.Fatalf("unchanged script rewritten: %+v %v", next, err)
	}
	for _, value := range []string{"", `<script src="https://analytics.example/script.js"></script>`} {
		if _, _, err := (StatusPageInput{AnalyticsScript: &value}).Apply(current, false); !errors.Is(err, ErrStatusPageAnalytics) {
			t.Fatalf("ordinary editor changed script: %v", err)
		}
		if _, _, err := (StatusPageInput{AnalyticsScript: &value}).Apply(current, true); err != nil {
			t.Fatalf("super-admin script change rejected: %v", err)
		}
	}
	empty := ""
	next, _, err = (StatusPageInput{Password: &empty}).Apply(current, false)
	if err != nil || next.PasswordHash != "" || next.PasswordEnabled {
		t.Fatalf("explicit password clear failed: %+v %v", next, err)
	}
}
