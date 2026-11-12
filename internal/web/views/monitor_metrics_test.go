package views

import (
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/y0f/asura/v3/internal/storage"
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

func TestResponsePercentilesRequireRetainedDurationSamples(t *testing.T) {
	for _, samples := range []int64{0, 1} {
		p := MonitorDetailParams{
			Monitor:  &storage.Monitor{ID: 7, Type: "http"},
			UpChecks: 40, ResponseSamples: samples,
		}
		doc := render(t, MonitorDetailPage(p))
		var summary string
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if classes, _ := attr(n, "class"); strings.Contains(classes, "percentile-summary") {
				summary = textOf(n)
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
		walk(doc)
		if summary == "" {
			t.Fatal("percentile summary missing")
		}
		if samples == 0 && (strings.Contains(summary, "0ms") || strings.Count(summary, "—") != 3) {
			t.Fatalf("retained uptime counts fabricated duration measurements: %s", summary)
		}
		if samples == 1 && strings.Count(summary, "0ms") != 3 {
			t.Fatalf("measured zero duration was hidden: %s", summary)
		}
	}
}

func TestMonitorUptimeWindowsKeepIndependentEvidence(t *testing.T) {
	p := MonitorDetailParams{
		Monitor:   &storage.Monitor{ID: 7, Type: "http"},
		Uptime24h: 100, Uptime7d: 87.5, Uptime30d: 100,
		Uptime7dHasData: true,
	}
	text := textOf(render(t, MonitorDetailPage(p)))
	if strings.Count(text, "No check data") != 2 || !strings.Contains(text, "87.50%") || strings.Contains(text, "100.00%") {
		t.Fatal("empty windows must not appear healthy or hide another window's real measurements")
	}
	if durationPercentile(0, 0) != "—" || durationPercentile(0, 1) != "0ms" {
		t.Fatal("missing duration samples and a measured zero must remain distinguishable")
	}
}
