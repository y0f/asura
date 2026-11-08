package api

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/y0f/asura/internal/httputil"
	"golang.org/x/text/width"
)

const (
	colorOK       = "#555555"
	colorWarning  = "#005fcc"
	colorCritical = "#005fcc"
	colorNeutral  = "#666666"
)

func (h *Handler) BadgeStatus(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeBadgeSVG(w, "status", "error", colorNeutral)
		return
	}

	ctx := r.Context()
	m, err := h.store.GetMonitor(ctx, id)
	if err != nil {
		writeBadgeSVG(w, "status", "not found", colorNeutral)
		return
	}

	visible, err := h.store.IsMonitorOnStatusPage(ctx, m.ID)
	if err != nil || !visible {
		writeBadgeSVG(w, "status", "not found", colorNeutral)
		return
	}

	label := r.URL.Query().Get("label")
	if label == "" {
		label = m.Name
	}
	status := m.Status
	color := statusColor(status)
	writeBadgeSVG(w, label, status, color)
}

func (h *Handler) BadgeUptime(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeBadgeSVG(w, "uptime", "error", colorNeutral)
		return
	}

	ctx := r.Context()
	m, err := h.store.GetMonitor(ctx, id)
	if err != nil {
		writeBadgeSVG(w, "uptime", "not found", colorNeutral)
		return
	}

	visible, err := h.store.IsMonitorOnStatusPage(ctx, m.ID)
	if err != nil || !visible {
		writeBadgeSVG(w, "uptime", "not found", colorNeutral)
		return
	}

	now := time.Now().UTC()
	from := now.Add(-30 * 24 * time.Hour)
	pct, err := h.store.GetUptimePercent(ctx, id, from, now)
	if err != nil {
		writeBadgeSVG(w, "uptime", "error", colorNeutral)
		return
	}

	label := r.URL.Query().Get("label")
	if label == "" {
		label = "uptime"
	}
	value := fmt.Sprintf("%.2f%%", pct)
	color := colorOK
	if pct < 99 {
		color = colorWarning
	}
	if pct < 95 {
		color = colorCritical
	}
	writeBadgeSVG(w, label, value, color)
}

func (h *Handler) BadgeResponseTime(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeBadgeSVG(w, "response", "error", colorNeutral)
		return
	}

	ctx := r.Context()
	m, err := h.store.GetMonitor(ctx, id)
	if err != nil {
		writeBadgeSVG(w, "response", "not found", colorNeutral)
		return
	}

	visible, err := h.store.IsMonitorOnStatusPage(ctx, m.ID)
	if err != nil || !visible {
		writeBadgeSVG(w, "response", "not found", colorNeutral)
		return
	}

	now := time.Now().UTC()
	from := now.Add(-24 * time.Hour)
	p50, _, _, err := h.store.GetResponseTimePercentiles(ctx, id, from, now)
	if err != nil {
		writeBadgeSVG(w, "response", "error", colorNeutral)
		return
	}

	label := r.URL.Query().Get("label")
	if label == "" {
		label = "response time"
	}
	value := fmt.Sprintf("%.0fms", p50)
	color := colorOK
	if p50 > 500 {
		color = colorWarning
	}
	if p50 > 2000 {
		color = colorCritical
	}
	writeBadgeSVG(w, label, value, color)
}

func (h *Handler) BadgeCert(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeBadgeSVG(w, "cert", "error", colorNeutral)
		return
	}

	ctx := r.Context()
	m, err := h.store.GetMonitor(ctx, id)
	if err != nil {
		writeBadgeSVG(w, "cert", "not found", colorNeutral)
		return
	}

	visible, err := h.store.IsMonitorOnStatusPage(ctx, m.ID)
	if err != nil || !visible {
		writeBadgeSVG(w, "cert", "not found", colorNeutral)
		return
	}

	cr, err := h.store.GetLatestCheckResult(ctx, id)
	if err != nil || cr == nil || cr.CertExpiry == nil {
		writeBadgeSVG(w, "cert", "n/a", colorNeutral)
		return
	}

	label := r.URL.Query().Get("label")
	if label == "" {
		label = "cert expiry"
	}
	days := int(time.Until(*cr.CertExpiry).Hours() / 24)
	value := fmt.Sprintf("%dd", days)
	color := colorOK
	if days < 30 {
		color = colorWarning
	}
	if days < 7 {
		color = colorCritical
	}
	writeBadgeSVG(w, label, value, color)
}

func statusColor(status string) string {
	switch status {
	case "up":
		return colorOK
	case "degraded":
		return colorWarning
	case "down":
		return colorCritical
	default:
		return colorNeutral
	}
}

// A conservative monospace estimate avoids counting encoded bytes, entities,
// and combining marks as visible cells. Fallback font metrics can still vary.
func badgeTextCells(text string) int {
	cells, previous := 0, 0
	for _, r := range text {
		if r == '\ufe0f' { // Emoji presentation can widen an otherwise narrow symbol.
			if previous == 1 {
				cells++
				previous = 2
			}
			continue
		}
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		previous = 1
		if kind := width.LookupRune(r).Kind(); kind == width.EastAsianWide || kind == width.EastAsianFullwidth {
			previous = 2
		}
		cells += previous
	}
	return cells
}

func badgeXMLText(text string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' || (r >= 0x20 && r <= 0xd7ff) || (r >= 0xe000 && r <= 0xfffd) || (r >= 0x10000 && r <= unicode.MaxRune) {
			return r
		}
		return unicode.ReplacementChar
	}, text)
}

// Badge labels are measured before HTML escaping. The SVG has no font fetches,
// scripting, duplicated shadow text, or shared clip-path IDs.
func writeBadgeSVG(w http.ResponseWriter, label, value, color string) {
	label, value = badgeXMLText(label), badgeXMLText(value)
	labelWidth := badgeTextCells(label)*7 + 12
	valueWidth := badgeTextCells(value)*7 + 12
	totalWidth := labelWidth + valueWidth
	label = html.EscapeString(label)
	value = html.EscapeString(value)

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "max-age=300")

	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" viewBox="0 0 %d 20" role="img" aria-label="%s: %s">
  <title>%s: %s</title>
  <rect width="%d" height="20" rx="3" fill="#303030"/>
  <path d="M%d 0h%d a3 3 0 0 1 3 3v14a3 3 0 0 1-3 3H%dZ" fill="%s"/>
  <g fill="#ffffff" text-anchor="middle" font-family="ui-monospace,monospace" font-size="11" aria-hidden="true" style="filter:grayscale(1)">
    <text x="%d" y="14">%s</text>
    <text x="%d" y="14">%s</text>
  </g>
</svg>`, totalWidth, totalWidth, label, value, label, value,
		totalWidth, labelWidth, valueWidth-3, labelWidth, color,
		labelWidth/2, label, labelWidth+valueWidth/2, value)
	io.WriteString(w, svg)
}
