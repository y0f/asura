package views

import (
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestPublicOverviewCountsCurrentStatesWithoutDuplicatingGroups(t *testing.T) {
	monitors := []MonitorWithUptime{
		{Monitor: &storage.Monitor{Name: "Healthy", Status: "up"}},
		{Monitor: &storage.Monitor{Name: "Slow", Status: "degraded"}},
		{Monitor: &storage.Monitor{Name: "Unavailable", Status: "down"}},
		{Monitor: &storage.Monitor{Name: "Waiting", Status: "pending"}},
	}
	p := PublicStatusPageParams{Monitors: monitors, HasGroups: true, Groups: []MonitorGroup{
		{Name: "Platform", Monitors: monitors[:2]},
		{Name: "Other services", Monitors: monitors[2:]},
	}}
	if got := publicServiceSummary(p); got != "1 operational · 1 degraded · 1 outage · 1 awaiting data" {
		t.Fatalf("current-state summary = %q", got)
	}
	if got := publicServiceSummary(PublicStatusPageParams{}); strings.Contains(got, "operational") {
		t.Fatalf("empty page claimed healthy services: %q", got)
	}
}

func TestPublicStatusUsesLabelsAndEscapesServiceIdentity(t *testing.T) {
	row := MonitorWithUptime{Monitor: &storage.Monitor{Name: `<script>private()</script>`, Type: "manual", Status: "degraded"}}
	doc := render(t, statusMonitorRow(row))
	if !strings.Contains(textOf(doc), row.Monitor.Name) {
		t.Fatal("service identity was not escaped")
	}
	if !strings.Contains(textOf(doc), "Degraded") || !strings.Contains(textOf(doc), "Reported status") {
		t.Fatal("status meaning depends on colour instead of text")
	}
}
