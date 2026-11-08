package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestMonitorSummaryAccess(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	access := MonitorAccess{MonitorIDs: []int64{monitors[4].ID}, GroupIDs: []int64{groups[0].ID}}
	now := time.Now().UTC()
	for _, monitor := range monitors {
		if err := store.UpsertMonitorStatus(ctx, &MonitorStatus{MonitorID: monitor.ID, Status: "up", LastCheckAt: &now, ResponseTime: monitor.ID}); err != nil {
			t.Fatal(err)
		}
	}
	up, down, degraded, paused, pending, err := store.CountMonitorsByStatus(ctx, access)
	if err != nil || up != 3 || down != 0 || degraded != 0 || paused != 0 || pending != 0 {
		t.Fatalf("summary crossed scope: %d/%d/%d/%d/%d, %v", up, down, degraded, paused, pending, err)
	}
	times, err := store.GetLatestResponseTimes(ctx, access)
	if err != nil || len(times) != 3 {
		t.Fatalf("response-time map crossed scope: %v, %v", times, err)
	}
	for _, monitor := range monitors {
		_, exists := times[monitor.ID]
		if exists != access.allows(monitor.ID, monitor.GroupID) {
			t.Errorf("unexpected response-time membership for monitor %d", monitor.ID)
		}
	}
	up, down, degraded, paused, pending, err = store.CountMonitorsByStatus(ctx, MonitorAccess{})
	if err != nil || up+down+degraded+paused+pending != 0 || pending != 0 {
		t.Fatalf("empty summary scope exposed data: %d/%d/%d/%d/%d, %v", up, down, degraded, paused, pending, err)
	}
}

func TestRelatedHistoryAndLogAccess(t *testing.T) {
	store, groups, monitors := accessFixture(t)
	ctx := context.Background()
	channel := &NotificationChannel{Name: "private channel", Type: "webhook", Settings: []byte("{}"), Enabled: true}
	if err := store.CreateNotificationChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	logs := make([]*RequestLog, 0, len(monitors)+1)
	for i := 0; i <= len(monitors); i++ {
		var monitorID *int64
		if i < len(monitors) {
			monitorID = &monitors[i].ID
		}
		if err := store.InsertNotificationHistory(ctx, &NotificationHistory{ChannelID: channel.ID, MonitorID: monitorID, EventType: "incident.created", Status: "sent"}); err != nil {
			t.Fatal(err)
		}
		logs = append(logs, &RequestLog{
			MonitorID: monitorID, Method: "GET", Path: fmt.Sprintf("/monitor-%d", i),
			ClientIP: fmt.Sprintf("192.0.2.%d", i+1), Referer: fmt.Sprintf("https://example.test/monitor-%d", i),
			StatusCode: 200, LatencyMs: int64(i + 1), CreatedAt: now,
		})
	}
	if err := store.InsertRequestLogBatch(ctx, logs); err != nil {
		t.Fatal(err)
	}
	access := MonitorAccess{MonitorIDs: []int64{monitors[4].ID}, GroupIDs: []int64{groups[0].ID}}
	for page := 1; page <= 3; page++ {
		result, err := store.ListNotificationHistory(ctx, NotifHistoryFilter{Access: []MonitorAccess{access}}, Pagination{Page: page, PerPage: 1})
		if err != nil || result.Total != 3 || result.TotalPages != 3 {
			t.Fatalf("history count crossed scope: %+v, %v", result, err)
		}
		rows := result.Data.([]*NotificationHistory)
		if len(rows) != 1 || rows[0].MonitorID == nil || !access.allows(*rows[0].MonitorID, rows[0].MonitorGroupID) {
			t.Fatalf("history row crossed scope: %+v", rows)
		}
	}
	f := RequestLogFilter{Access: []MonitorAccess{access}, From: now.Add(-time.Minute), To: now.Add(time.Minute)}
	result, err := store.ListRequestLogs(ctx, f, Pagination{Page: 1, PerPage: 1})
	if err != nil || result.Total != 3 || result.TotalPages != 3 {
		t.Fatalf("log count crossed scope: %+v, %v", result, err)
	}
	stats, err := store.GetRequestLogStats(ctx, f.From, f.To, access)
	if err != nil || stats.TotalRequests != 3 || stats.UniqueVisitors != 3 || stats.AvgLatencyMs != 3 || len(stats.TopPaths) != 3 || len(stats.TopReferers) != 3 {
		t.Fatalf("log aggregates crossed scope: %+v, %v", stats, err)
	}
	for _, path := range stats.TopPaths {
		if path.Path != "/monitor-0" && path.Path != "/monitor-2" && path.Path != "/monitor-4" {
			t.Errorf("hidden top path %q", path.Path)
		}
	}
	ips, err := store.ListTopClientIPs(ctx, f.From, f.To, 50, access)
	if err != nil || len(ips) != 3 {
		t.Fatalf("IP choices crossed scope: %v, %v", ips, err)
	}
	for _, ip := range ips {
		if ip != "192.0.2.1" && ip != "192.0.2.3" && ip != "192.0.2.5" {
			t.Errorf("hidden IP choice %q", ip)
		}
	}
	global, err := store.ListNotificationHistory(ctx, NotifHistoryFilter{Access: []MonitorAccess{{All: true}}}, Pagination{Page: 1, PerPage: 10})
	if err != nil || global.Total != 7 {
		t.Fatalf("global history omitted unassociated entries: %+v, %v", global, err)
	}
	denied, err := store.ListNotificationHistory(ctx, NotifHistoryFilter{Access: []MonitorAccess{{}}}, Pagination{Page: 1, PerPage: 10})
	if err != nil || denied.Total != 0 {
		t.Fatalf("empty history scope exposed entries: %+v, %v", denied, err)
	}
	// Group scope follows current membership for historical records too.
	if _, err := store.BulkSetMonitorGroup(ctx, []int64{monitors[0].ID}, &groups[1].ID); err != nil {
		t.Fatal(err)
	}
	moved, err := store.ListNotificationHistory(ctx, NotifHistoryFilter{Access: []MonitorAccess{access}}, Pagination{Page: 1, PerPage: 10})
	if err != nil || moved.Total != 2 {
		t.Fatalf("history retained stale group authority: %+v, %v", moved, err)
	}
}
