package httputil

import (
	"context"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestHistoryResponseDoesNotGrantRelatedReadsOrExposeProviderCredentials(t *testing.T) {
	if response := NotificationHistoryResponse(context.Background(), &storage.PaginatedResult{Data: map[string]string{"private": "unexpected raw data"}}); response != nil {
		t.Fatal("unexpected history result type bypassed redaction")
	}
	monitorID, groupID := int64(1), int64(2)
	row := &storage.NotificationHistory{
		ChannelID: 5, ChannelName: "private channel", ChannelType: "webhook",
		MonitorID: &monitorID, MonitorName: "private monitor", MonitorGroupID: &groupID,
		Status: "failed", Error: "Post https://secret-token.example.test/hook?key=secret: timeout",
	}
	result := &storage.PaginatedResult{Data: []*storage.NotificationHistory{row}, Total: 1}
	for _, tc := range []struct {
		name        string
		permissions []string
		channel     bool
		monitor     bool
	}{
		{"history only", []string{"notification_history.read"}, false, false},
		{"channel read", []string{"notification_history.read", "notifications.read"}, true, false},
		{"monitor read", []string{"notification_history.read", "monitors.read"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), CtxKeyAPIKey, &config.APIKeyConfig{Permissions: tc.permissions})
			response := NotificationHistoryResponse(ctx, result)
			got := response.Data.([]*storage.NotificationHistory)[0]
			if (got.ChannelName != "") != tc.channel || (got.ChannelType != "") != tc.channel || (got.MonitorName != "") != tc.monitor {
				t.Fatalf("related read boundary crossed: %+v", got)
			}
			if got.ChannelID != 5 || got.MonitorID == nil || *got.MonitorID != monitorID || got.Error != "Delivery failed" {
				t.Fatalf("IDs/status lost or provider secret leaked: %+v", got)
			}
		})
	}
	if row.ChannelName != "private channel" || row.MonitorName != "private monitor" || row.Error == "Delivery failed" {
		t.Fatal("read response mutated source records")
	}
}

func TestMonitorAccessTranslationFailsClosed(t *testing.T) {
	ctx := context.Background()
	for _, actions := range [][]string{nil, {"monitors.read"}, {"monitors.typo"}, {"groups.read"}} {
		filters := MonitorAccess(ctx, actions...)
		if len(filters) == 0 || filters[0].All || len(filters[0].MonitorIDs) != 0 || len(filters[0].GroupIDs) != 0 {
			t.Fatalf("missing principal or action became unrestricted: %v", filters)
		}
	}
}
