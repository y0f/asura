package api

import (
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/redact"
	"github.com/y0f/asura/internal/storage"
)

// Mutation responses do not turn a write-only grant into a read grant.
func monitorResponse(r *http.Request, monitor *storage.Monitor) any {
	if !httputil.GetAPIKey(r.Context()).HasPermission("monitors.read") {
		return map[string]int64{"id": monitor.ID}
	}
	return redact.Monitor(monitor)
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
