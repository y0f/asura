package httputil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

var (
	ErrStatusPageAnalytics = errors.New("only a super administrator may change analytics scripts")
	ErrInvalidStatusPage   = errors.New("invalid status page")
)

// StatusPageInput distinguishes omitted fields from explicit replacements.
// Apply runs against the current transaction's page, preserving omitted
// membership, credentials and metadata without a stale pre-read.
type StatusPageInput struct {
	Title            *string                      `json:"title"`
	Slug             *string                      `json:"slug"`
	Description      *string                      `json:"description"`
	CustomCSS        *string                      `json:"custom_css"`
	ShowIncidents    *bool                        `json:"show_incidents"`
	Enabled          *bool                        `json:"enabled"`
	APIEnabled       *bool                        `json:"api_enabled"`
	SortOrder        *int                         `json:"sort_order"`
	LogoURL          *string                      `json:"logo_url"`
	FaviconURL       *string                      `json:"favicon_url"`
	CustomHeaderHTML *string                      `json:"custom_header_html"`
	AnalyticsScript  *string                      `json:"analytics_script"`
	Password         *string                      `json:"password"`
	Monitors         *[]storage.StatusPageMonitor `json:"monitors"`
}

func (in StatusPageInput) Apply(current *storage.StatusPage, superAdmin bool) (*storage.StatusPage, *[]storage.StatusPageMonitor, error) {
	next := storage.StatusPage{ShowIncidents: true}
	if current != nil {
		next = *current
	}
	if in.AnalyticsScript != nil && *in.AnalyticsScript != next.AnalyticsScript && !superAdmin {
		return nil, nil, ErrStatusPageAnalytics
	}
	if in.Title != nil {
		next.Title = *in.Title
	}
	if in.Slug != nil {
		next.Slug = *in.Slug
	}
	if in.Description != nil {
		next.Description = *in.Description
	}
	if in.CustomCSS != nil {
		next.CustomCSS = *in.CustomCSS
	}
	if in.ShowIncidents != nil {
		next.ShowIncidents = *in.ShowIncidents
	}
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	if in.APIEnabled != nil {
		next.APIEnabled = *in.APIEnabled
	}
	if in.SortOrder != nil {
		next.SortOrder = *in.SortOrder
	}
	if in.LogoURL != nil {
		next.LogoURL = *in.LogoURL
	}
	if in.FaviconURL != nil {
		next.FaviconURL = *in.FaviconURL
	}
	if in.CustomHeaderHTML != nil {
		next.CustomHeaderHTML = *in.CustomHeaderHTML
	}
	if in.AnalyticsScript != nil {
		next.AnalyticsScript = *in.AnalyticsScript
	}
	if in.Password != nil {
		next.PasswordHash = ""
		if *in.Password != "" {
			hash := sha256.Sum256([]byte(*in.Password))
			next.PasswordHash = hex.EncodeToString(hash[:])
		}
	}
	// Preserve omitted legacy branding byte-for-byte. Runtime rendering has its
	// own sanitizer; an unrelated edit must not silently rewrite stored scripts.
	css, header, analytics := next.CustomCSS, next.CustomHeaderHTML, next.AnalyticsScript
	logo, favicon := next.LogoURL, next.FaviconURL
	keepAnalytics := in.AnalyticsScript == nil || (current != nil && *in.AnalyticsScript == current.AnalyticsScript)
	if in.CustomCSS == nil {
		next.CustomCSS = ""
	}
	if in.CustomHeaderHTML == nil {
		next.CustomHeaderHTML = ""
	}
	if keepAnalytics {
		next.AnalyticsScript = ""
	}
	if in.LogoURL == nil {
		next.LogoURL = ""
	}
	if in.FaviconURL == nil {
		next.FaviconURL = ""
	}
	if err := validate.ValidateStatusPage(&next); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidStatusPage, err)
	}
	if in.CustomCSS == nil {
		next.CustomCSS = css
	}
	if in.CustomHeaderHTML == nil {
		next.CustomHeaderHTML = header
	}
	if keepAnalytics {
		next.AnalyticsScript = analytics
	}
	if in.LogoURL == nil {
		next.LogoURL = logo
	}
	if in.FaviconURL == nil {
		next.FaviconURL = favicon
	}
	next.PasswordEnabled = next.PasswordHash != ""
	return &next, in.Monitors, nil
}
