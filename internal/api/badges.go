package api

import (
	"database/sql"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"golang.org/x/text/width"
)

const (
	colorOK       = "#555555"
	colorWarning  = "#005fcc"
	colorCritical = "#005fcc"
	colorNeutral  = "#666666"
)

func (h *Handler) badgeMonitor(w http.ResponseWriter, r *http.Request, label string) (*storage.Monitor, bool) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeBadgeResponse(w, http.StatusBadRequest, label, "error", colorNeutral)
		return nil, false
	}
	m, err := h.store.GetPublicBadgeMonitor(r.Context(), id)
	if err != nil {
		writeBadgeFailure(w, label, err)
		return nil, false
	}
	return m, true
}

func (h *Handler) badgeData(w http.ResponseWriter, r *http.Request, metric storage.PublicBadgeMetric) (*storage.PublicBadgeData, bool) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeBadgeResponse(w, http.StatusBadRequest, string(metric), "error", colorNeutral)
		return nil, false
	}
	data, err := h.store.GetPublicBadgeData(r.Context(), id, metric, time.Now().UTC())
	if err != nil {
		writeBadgeFailure(w, string(metric), err)
		return nil, false
	}
	return data, true
}

func writeBadgeFailure(w http.ResponseWriter, label string, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeBadgeResponse(w, http.StatusNotFound, label, "not found", colorNeutral)
	} else {
		writeBadgeResponse(w, http.StatusInternalServerError, label, "error", colorNeutral)
	}
}

func badgeLabel(r *http.Request, fallback string) string {
	if label := r.URL.Query().Get("label"); label != "" {
		return label
	}
	return fallback
}

func (h *Handler) BadgeStatus(w http.ResponseWriter, r *http.Request) {
	m, ok := h.badgeMonitor(w, r, "status")
	if !ok {
		return
	}
	status := m.Status
	if !m.Enabled {
		status = "paused"
	}
	writeBadgeSVG(w, badgeLabel(r, m.Name), status, statusColor(status))
}

func (h *Handler) BadgeUptime(w http.ResponseWriter, r *http.Request) {
	data, ok := h.badgeData(w, r, storage.PublicBadgeUptime)
	if !ok {
		return
	}
	label := badgeLabel(r, "uptime")
	if data.TotalChecks == 0 {
		writeBadgeSVG(w, label, "no checks", colorNeutral)
		return
	}
	value := fmt.Sprintf("%.2f%%", data.UptimePercent)
	color := colorOK
	if data.UptimePercent < 99 {
		color = colorWarning
	}
	if data.UptimePercent < 95 {
		color = colorCritical
	}
	writeBadgeSVG(w, label, value, color)
}

func (h *Handler) BadgeResponseTime(w http.ResponseWriter, r *http.Request) {
	data, ok := h.badgeData(w, r, storage.PublicBadgeResponse)
	if !ok {
		return
	}
	label := badgeLabel(r, "response time")
	if data.Monitor.Type == "heartbeat" || data.Monitor.Type == "manual" {
		writeBadgeSVG(w, label, "n/a", colorNeutral)
		return
	}
	summary := data.Response
	if summary.Samples == 0 {
		writeBadgeSVG(w, label, "no samples", colorNeutral)
		return
	}
	value := fmt.Sprintf("%.0fms", summary.P50)
	color := colorOK
	if summary.P50 > 500 {
		color = colorWarning
	}
	if summary.P50 > 2000 {
		color = colorCritical
	}
	writeBadgeSVG(w, label, value, color)
}

func (h *Handler) BadgeCert(w http.ResponseWriter, r *http.Request) {
	data, ok := h.badgeData(w, r, storage.PublicBadgeCert)
	if !ok {
		return
	}
	label := badgeLabel(r, "cert expiry")
	if data.CertExpiry == nil {
		writeBadgeSVG(w, label, "n/a", colorNeutral)
		return
	}
	remaining := time.Until(*data.CertExpiry)
	if remaining <= 0 {
		writeBadgeSVG(w, label, "expired", colorCritical)
		return
	}
	days := int(remaining.Hours() / 24)
	value := fmt.Sprintf("%dd", days)
	if days == 0 {
		value = "<1d"
	}
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
	writeBadgeResponse(w, http.StatusOK, label, value, color)
}

func writeBadgeResponse(w http.ResponseWriter, status int, label, value, color string) {
	label, value = badgeXMLText(label), badgeXMLText(value)
	labelWidth := badgeTextCells(label)*7 + 12
	valueWidth := badgeTextCells(value)*7 + 12
	totalWidth := labelWidth + valueWidth
	label = html.EscapeString(label)
	value = html.EscapeString(value)

	w.Header().Set("Content-Type", "image/svg+xml")
	if status == http.StatusOK {
		w.Header().Set("Cache-Control", "max-age=300")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)

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
