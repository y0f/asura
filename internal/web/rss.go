package web

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
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
	if err != nil || sp == nil || !sp.Enabled {
		http.NotFound(w, r)
		return
	}

	monitors, _, err := h.store.ListStatusPageMonitorsWithStatus(ctx, sp.ID)
	if err != nil {
		monitors = []*storage.Monitor{}
	}

	now := time.Now().UTC()
	incidents := httputil.PublicIncidentsForPage(ctx, h.store, sp, monitors, now)

	extURL := h.cfg.ResolvedExternalURL()
	pageURL := fmt.Sprintf("%s/%s", extURL, sp.Slug)

	var items []rssItem
	for _, inc := range incidents {
		title := fmt.Sprintf("Incident: %s", inc.MonitorName)
		desc := inc.Cause
		if inc.Status == "resolved" {
			title = fmt.Sprintf("Resolved: %s", inc.MonitorName)
			if inc.ResolvedAt != nil {
				desc = fmt.Sprintf("Resolved after %s. Cause: %s",
					inc.ResolvedAt.Sub(inc.StartedAt).Truncate(time.Second), inc.Cause)
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
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(feed)
}
