package views

import (
	"strings"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestPassiveMonitorDetailsOmitResponseMeasurements(t *testing.T) {
	for _, monitorType := range []string{"manual", "heartbeat", "http"} {
		t.Run(monitorType, func(t *testing.T) {
			p := MonitorDetailParams{Monitor: &storage.Monitor{ID: 7, Name: "Check", Type: monitorType}, Checks: &storage.PaginatedResult{Data: []*storage.CheckResult{{Status: "up"}}}}
			text := textOf(render(t, MonitorDetailPage(p)))
			if hasChart := strings.Contains(text, "Response time"); hasChart != (monitorType == "http") {
				t.Fatalf("%s response chart present = %v", monitorType, hasChart)
			}
			if monitorType != "http" && strings.Contains(text, "0 ms") {
				t.Fatal("passive status events must not be represented as measured response times")
			}
		})
	}
}

func TestMonitorUptimeWindowsKeepIndependentEvidence(t *testing.T) {
	p := MonitorDetailParams{
		Monitor:   &storage.Monitor{ID: 7, Type: "manual"},
		Uptime24h: 100, Uptime7d: 87.5, Uptime30d: 100,
		Uptime7dHasData: true,
	}
	text := textOf(render(t, MonitorDetailPage(p)))
	if strings.Count(text, "No check data") != 2 || !strings.Contains(text, "87.50%") || strings.Contains(text, "100.00%") {
		t.Fatal("empty windows must not appear healthy or hide another window's real measurements")
	}
	if durationPercentile(0, 0) != "—" || durationPercentile(0, 1) == "—" {
		t.Fatal("missing duration samples and a measured zero must remain distinguishable")
	}
}
