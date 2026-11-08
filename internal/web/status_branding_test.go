package web

import (
	"strings"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestStatusBrandingOnlyWidensExactPublicImageOrigins(t *testing.T) {
	h := testWebHandler(t)
	original := &storage.StatusPage{LogoURL: "https://images.example/logo", FaviconURL: "https://icons.example:8443/favicon", CustomHeaderHTML: `<img src="https://images.example/other"><img src="//untrusted.example/a"><script>bad()</script>`, AnalyticsScript: `<script>alert(1)</script><script src="https://analytics.example/script.js"></script>`}
	safe, policy := h.publicStatusBranding(original)
	if safe == original || strings.Contains(safe.CustomHeaderHTML, "<script") || strings.Contains(safe.CustomHeaderHTML, "untrusted") || strings.Contains(safe.AnalyticsScript, "alert") {
		t.Fatalf("unsafe legacy branding: %+v", safe)
	}
	if !strings.Contains(policy, "img-src 'self' data: https://icons.example:8443 https://images.example;") || strings.Count(policy, "https://images.example") != 1 {
		t.Fatalf("wrong public image policy: %s", policy)
	}
	for _, forbidden := range []string{"https:;", "analytics.example", "untrusted.example", "script-src https"} {
		if strings.Contains(policy, forbidden) {
			t.Fatalf("policy widened beyond images: %s", policy)
		}
	}
	if !strings.Contains(policy, "script-src 'self' 'unsafe-inline' 'unsafe-eval';") || !strings.Contains(policy, "connect-src 'self';") {
		t.Fatal("script/connect baseline changed")
	}
	if !strings.Contains(original.CustomHeaderHTML, "<script>") {
		t.Fatal("runtime sanitizer rewrote stored configuration")
	}
}
