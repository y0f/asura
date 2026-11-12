package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/y0f/asura/v3/internal/storage"
)

func TestGroupDeleteRequiresEmptyGroupAPI(t *testing.T) {
	env := newTestEnv(t, nil, false, nil)
	ctx := context.Background()
	g := &storage.MonitorGroup{Name: "occupied"}
	if err := env.store.CreateMonitorGroup(ctx, g); err != nil {
		t.Fatal(err)
	}
	m := &storage.Monitor{Name: "member", Type: "http", GroupID: &g.ID, Interval: 60, Timeout: 10}
	if err := env.store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/groups/%d", g.ID)
	w := env.do("DELETE", path, "", admin)
	if w.Code != http.StatusConflict {
		t.Fatalf("nonempty delete: %d %s", w.Code, w.Body.String())
	}
	stored, err := env.store.GetMonitor(ctx, m.ID)
	if err != nil || stored.GroupID == nil || *stored.GroupID != g.ID {
		t.Fatalf("nonempty delete moved monitor: %+v, %v", stored, err)
	}
	if err := env.store.DeleteMonitor(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	w = env.do("DELETE", path, "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("empty delete: %d %s", w.Code, w.Body.String())
	}
}
