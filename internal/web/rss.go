package web

import (
	"database/sql"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
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

	now := time.Now().UTC()
	snapshot, err := h.store.GetPublicStatusPageSnapshot(ctx, sp.ID, storage.PublicStatusPageAccess{
		ExpectedPasswordHash: sp.PasswordHash, Audience: storage.PublicStatusPageWeb,
	}, now, false)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("web: RSS snapshot", "error", err)
		http.Error(w, "Failed to load status page", http.StatusInternalServerError)
		return
	}
	sp, incidents := snapshot.Page, snapshot.Incidents

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
