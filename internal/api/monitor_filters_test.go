package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/storage"
)

func TestMonitorListFiltersIntersectScopeBeforeCountsAndPagination(t *testing.T) {
	h, store, groups, monitors := collectionFixture(t)
	ctx := context.Background()
	names := []string{"Alpha allowed", "Alpha hidden"}
	for i, m := range monitors {
		m.Name = names[i]
		m.Target = "https://user:hidden-password@example.test/?token=hidden-query"
		if err := store.UpdateMonitor(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	second := &storage.Monitor{Name: "Zulu allowed", Type: "manual", GroupID: &groups[0].ID, Enabled: false}
	if err := store.CreateMonitor(ctx, second); err != nil {
		t.Fatal(err)
	}
	tag := &storage.Tag{Name: "filter-tag", Color: "#005bbf"}
	if err := store.CreateTag(ctx, tag); err != nil {
		t.Fatal(err)
	}
	for _, m := range monitors {
		if err := store.SetMonitorTags(ctx, m.ID, []storage.MonitorTag{{TagID: tag.ID}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpsertMonitorStatus(ctx, &storage.MonitorStatus{MonitorID: monitors[0].ID, Status: "down", ResponseTime: 50}); err != nil {
		t.Fatal(err)
	}
	do := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/monitors?"+query, nil)
		r = r.WithContext(context.WithValue(ctx, httputil.CtxKeyAPIKey, incidentPermissionKey(t, "monitors.read", groups[0].ID)))
		w := httptest.NewRecorder()
		h.ListMonitors(w, r)
		return w
	}
	for _, tc := range []struct {
		query string
		total int64
		id    int64
	}{
		{"per_page=1", 2, monitors[0].ID}, {"per_page=1&page=2", 2, second.ID},
		{"type=http", 1, monitors[0].ID}, {"type=manual", 1, second.ID},
		{"q=Alpha", 1, monitors[0].ID}, {"status=down", 1, monitors[0].ID}, {"status=paused", 1, second.ID}, {"status=pending", 0, 0},
		{fmt.Sprintf("group=%d", groups[0].ID), 2, monitors[0].ID}, {fmt.Sprintf("group=%d", groups[1].ID), 0, 0},
		{fmt.Sprintf("tag=%d", tag.ID), 1, monitors[0].ID}, {fmt.Sprintf("tag=%d&type=manual", tag.ID), 0, 0},
		{"sort=name&per_page=1", 2, monitors[0].ID}, {"sort=response_time&per_page=1", 2, monitors[0].ID},
	} {
		w := do(tc.query)
		var result struct {
			Total int64              `json:"total"`
			Data  []*storage.Monitor `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Total != tc.total {
			t.Fatalf("filter%q: %d %s %v", tc.query, w.Code, w.Body, err)
		}
		if tc.id != 0 && (len(result.Data) == 0 || result.Data[0].ID != tc.id) {
			t.Fatalf("filter%q returned wrong row: %+v", tc.query, result.Data)
		}
		for _, m := range result.Data {
			if m.ID == monitors[1].ID {
				t.Fatalf("filter escaped current group scope: %q", tc.query)
			}
		}
	}
	for _, prefix := range []string{"https://user:hidden-p", "https://user:wrong-p", "https://user:hidden-password@example.test/?token=hidden", "https://user:hidden-password@example.test/?token=wrong"} {
		w := do("q=" + url.QueryEscape(prefix))
		var result struct {
			Total int64 `json:"total"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || result.Total != 0 {
			t.Fatalf("target-secret prefix influenced name search: %d %v", w.Code, err)
		}
	}
	for _, query := range []string{"group=0", "group=-1", "group=invalid", "tag=0", "tag=invalid", "type=unknown", "status=unknown", "sort=unknown"} {
		if w := do(query); w.Code != 400 {
			t.Fatalf("invalid filter%q silently ignored: %d", query, w.Code)
		}
	}
}
