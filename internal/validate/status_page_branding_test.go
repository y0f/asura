package validate

import (
	"strings"
	"testing"
)

func TestStatusPageImageURLAllowsOnlyExactSafeOrigins(t *testing.T) {
	for _, test := range []struct{ raw, value, origin string }{
		{"", "", ""}, {"/static/logo.svg", "/static/logo.svg", ""},
		{"https://Images.Example:443/a.png?q=x#y", "https://images.example:443/a.png?q=x#y", "https://images.example:443"},
		{"https://[2001:db8::1]:8443/a", "https://[2001:db8::1]:8443/a", "https://[2001:db8::1]:8443"},
		{"https://bücher.example/a", "https://xn--bcher-kva.example/a", "https://xn--bcher-kva.example"},
	} {
		value, origin, err := StatusPageImageURL(test.raw)
		if err != nil || value != test.value || origin != test.origin {
			t.Errorf("%q => %q %q %v", test.raw, value, origin, err)
		}
	}
	for _, raw := range []string{"//evil.example/a", "http://example/a", "javascript:alert(1)", "data:image/png;base64,abc", "https://user:password@example/a", "https://*.example/a", "https://example;script-src/a", "https://example/a\nheader", `/\evil.example/a`, "https://example:0/a", "https://example:65536/a", "https://example:wrong/a", "logo.svg"} {
		if value, origin, err := StatusPageImageURL(raw); err == nil || value != "" || origin != "" {
			t.Errorf("unsafe URL accepted: %q => %q %q %v", raw, value, origin, err)
		}
	}
}

func TestStatusPageHeaderStripsLegacyActiveContentAndUnsafeImages(t *testing.T) {
	header := SanitizeStatusPageHeader(`<div onclick="bad()"><img src="//evil.test/x" onerror="bad()"><img src="https://images.example/logo"><script src="https://evil.test/x"></script><a href="https://links.example/">Link</a></div>`)
	for _, forbidden := range []string{"onclick", "onerror", "<script", "evil.test"} {
		if strings.Contains(header, forbidden) {
			t.Fatalf("legacy header retained %q: %s", forbidden, header)
		}
	}
	origins := StatusPageHeaderImageOrigins(header)
	if len(origins) != 1 || origins[0] != "https://images.example" {
		t.Fatalf("image origins include links or unsafe content: %v", origins)
	}
}
