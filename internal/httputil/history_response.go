package httputil

import (
	"context"

	"github.com/y0f/asura/v3/internal/storage"
)

// NotificationHistoryResponse keeps delivery status separate from permission to
// read its related resources. Provider errors can contain credential-bearing
// URLs, so ordinary history responses expose only the failure state.
func NotificationHistoryResponse(ctx context.Context, result *storage.PaginatedResult) *storage.PaginatedResult {
	if result == nil {
		return nil
	}
	rows, ok := result.Data.([]*storage.NotificationHistory)
	if !ok {
		return nil
	}
	key := GetAPIKey(ctx)
	response := *result
	entries := make([]*storage.NotificationHistory, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		entry := *row
		if !key.HasPermission("notifications.read") {
			entry.ChannelName = ""
			entry.ChannelType = ""
		}
		if entry.MonitorID == nil || !key.AllowsMonitor("monitors.read", *entry.MonitorID, entry.MonitorGroupID) {
			entry.MonitorName = ""
		}
		if entry.Error != "" {
			entry.Error = "Delivery failed"
		}
		entries = append(entries, &entry)
	}
	response.Data = entries
	return &response
}
