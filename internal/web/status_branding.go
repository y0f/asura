package web

import (
	"slices"
	"strings"

	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/validate"
)

// publicStatusBranding sanitizes even legacy stored HTML without rewriting
// configuration. Only this response receives the page's exact image origins;
// analytics scripts cannot expand script-src or connect-src.
func (h *Handler) publicStatusBranding(page *storage.StatusPage) (*storage.StatusPage, string) {
	safe := *page
	safe.CustomHeaderHTML = validate.SanitizeStatusPageHeader(page.CustomHeaderHTML)
	safe.AnalyticsScript = validate.SanitizeStatusPageAnalytics(page.AnalyticsScript)
	var origins []string
	var origin string
	safe.LogoURL, origin, _ = validate.StatusPageImageURL(page.LogoURL)
	if origin != "" {
		origins = append(origins, origin)
	}
	safe.FaviconURL, origin, _ = validate.StatusPageImageURL(page.FaviconURL)
	if origin != "" {
		origins = append(origins, origin)
	}
	origins = append(origins, validate.StatusPageHeaderImageOrigins(safe.CustomHeaderHTML)...)
	slices.Sort(origins)
	origins = slices.Compact(origins)
	images := "img-src 'self' data:"
	if len(origins) != 0 {
		images += " " + strings.Join(origins, " ")
	}
	policy := strings.Replace(componentCSP, "img-src 'self' data:;", images+";", 1) + h.cspFrameDirective
	return &safe, policy
}
