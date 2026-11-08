package views

import (
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestHeartbeatCreationShowsOneTimeURLWithScopedDestination(t *testing.T) {
	p := HeartbeatCreatedParams{
		LayoutParams: LayoutParams{BasePath: "/asura", Perms: map[string]bool{"incidents.read": true}},
		Monitor:      &storage.Monitor{ID: 7, Name: "Nightly backup"},
		PingURL:      "https://example.test/asura/api/v1/heartbeat/one-time-token",
	}
	doc := render(t, HeartbeatCreatedPage(p))
	if !strings.Contains(textOf(doc), "shown once") || hasHref(doc, "/asura/monitors/7") || !hasHref(doc, "/asura/incidents") {
		t.Fatal("creation response misses one-time guidance or links to an unreadable monitor")
	}
	p.Perms["monitors.read"] = true
	if !hasHref(render(t, HeartbeatCreatedPage(p)), "/asura/monitors/7") {
		t.Fatal("readable monitor destination is missing")
	}
}
