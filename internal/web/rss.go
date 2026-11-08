package web

import (
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/httputil"
)

type rssChannel struct {
	XMLName       xml.Name  `xml:"channel"`
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	LastBuildDate string    `xml:"lastBuildDate"`
	TTL           int       `xml:"ttl"`
	Items         []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
	GUID        string `xml:"guid"`
}

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

func (h *Handler) StatusPageRSS(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()

	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.logger.Error("web: RSS status page", "error", err)
		http.Error(w, "Failed to load status page", http.StatusInternalServerError)
		return
	}
	if err != nil || sp == nil || !sp.Enabled {
		http.NotFound(w, r)
		return
	}

	if !h.requireStatusPageAuth(w, r, sp) {
		return
	}

	monitors, _, err := h.store.ListStatusPageMonitorsWithStatus(ctx, sp.ID)
	if err != nil {
		h.logger.Error("web: RSS monitors", "error", err)
		http.Error(w, "Failed to load status page monitors", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()
	incidents, err := httputil.PublicIncidentsForPage(ctx, h.store, sp, monitors, now)
	if err != nil {
		h.logger.Error("web: RSS incidents", "error", err)
		http.Error(w, "Failed to load incidents", http.StatusInternalServerError)
		return
	}

	extURL := h.cfg.ResolvedExternalURL()
	pageURL := fmt.Sprintf("%s/%s", extURL, sp.Slug)

	var items []rssItem
	for _, inc := range incidents {
		title := fmt.Sprintf("Incident: %s", inc.MonitorName)
		desc := fmt.Sprintf("%s is experiencing issues", inc.MonitorName)
		if inc.Status == "resolved" {
			title = fmt.Sprintf("Resolved: %s", inc.MonitorName)
			if inc.ResolvedAt != nil {
				desc = fmt.Sprintf("%s recovered after %s",
					inc.MonitorName, inc.ResolvedAt.Sub(inc.StartedAt).Truncate(time.Second))
			}
		}
		items = append(items, rssItem{
			Title:       title,
			Link:        pageURL,
			Description: desc,
			PubDate:     inc.StartedAt.Format(time.RFC1123Z),
			GUID:        fmt.Sprintf("%s/incident/%d", pageURL, inc.ID),
		})
	}

	feed := rssFeed{
		Version: "2.0",
		Channel: rssChannel{
			Title:         sp.Title,
			Link:          pageURL,
			Description:   sp.Description,
			LastBuildDate: now.Format(time.RFC1123Z),
			TTL:           5,
			Items:         items,
		},
	}

	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	if sp.PasswordHash == "" {
		w.Header().Set("Cache-Control", "public, max-age=300")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(feed)
}
