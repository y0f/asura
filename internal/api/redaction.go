package api

import (
	"net/http"

	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/redact"
	"github.com/y0f/asura/v3/internal/storage"
)

// Mutation responses do not turn a write-only grant into a read grant.
func monitorResponse(r *http.Request, monitor *storage.Monitor) any {
	if !httputil.GetAPIKey(r.Context()).AllowsMonitor("monitors.read", monitor.ID, monitor.GroupID) {
		return map[string]int64{"id": monitor.ID}
	}
	return monitorReadResponse(r, monitor)
}

func monitorReadResponse(r *http.Request, monitor *storage.Monitor) *storage.Monitor {
	response := httputil.MonitorReadResponse(r.Context(), monitor)
	if response != nil && !httputil.GetAPIKey(r.Context()).HasPermission("tags.read") {
		response.MonitorTags = make([]storage.MonitorTag, len(monitor.MonitorTags))
		for i, tag := range monitor.MonitorTags {
			response.MonitorTags[i].TagID = tag.TagID
		}
	}
	return response
}

func notificationResponse(r *http.Request, channel *storage.NotificationChannel) any {
	if !httputil.GetAPIKey(r.Context()).HasPermission("notifications.read") {
		return map[string]int64{"id": channel.ID}
	}
	return redact.Notification(channel)
}

func secretClearSet(keys []string) map[string]bool {
	clear := make(map[string]bool, len(keys))
	for _, key := range keys {
		clear[key] = true
	}
	return clear
}
