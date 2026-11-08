package views

import (
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"

	"github.com/y0f/asura/internal/storage"
)

func TestPassiveDetailRejectsLegacySampledPresentation(t *testing.T) {
	for _, kind := range []string{"manual", "heartbeat"} {
		t.Run(kind, func(t *testing.T) {
			at := time.Date(2026, 11, 8, 1, 2, 3, 0, time.UTC)
			p := MonitorDetailParams{
				LayoutParams: LayoutParams{Perms: map[string]bool{"monitors.read": true, "sla.read": true}},
				Monitor:      &storage.Monitor{ID: 7, Type: kind, Name: "Reported service", Status: "up", Interval: 60, LastCheckAt: &at, SLATarget: 99.9},
				Uptime24h:    100, Uptime7d: 100, Uptime30d: 100,
				Uptime24hHasData: true, Uptime7dHasData: true, Uptime30dHasData: true,
				SLA:       &SLAData{Target: 99.9, TotalChecks: 10, UptimeMonth: 100},
				Heatmap:   []HeatmapDay{{Date: "2026-11-08", UptimePct: 100, HasData: true}},
				Heartbeat: &HeartbeatData{LastPingAt: &at, GraceSecs: 30},
			}
			doc := render(t, MonitorDetailPage(p))
			text := textOf(doc)
			var heading string
			var findHeading func(*html.Node)
			findHeading = func(n *html.Node) {
				if id, _ := attr(n, "id"); id == "checks-title" {
					heading = strings.TrimSpace(textOf(n))
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					findHeading(child)
				}
			}
			findHeading(doc)
			if heading != "Recorded state history" || strings.Contains(text, "p.Monitor.") {
				t.Fatalf("invalid rendered passive heading %q", heading)
			}
			for _, sampled := range []string{"Uptime 24h", "Checks 24h", "Response time", "SLA target", "Uptime, last 365 days", "Failures before alert"} {
				if strings.Contains(text, sampled) {
					t.Errorf("passive monitor showed %q", sampled)
				}
			}
			if !strings.Contains(text, "Reported state") || !strings.Contains(text, "not calculated") {
				t.Fatal("passive state and unavailable-metrics explanation missing")
			}
			if kind == "heartbeat" && (!strings.Contains(text, "Last ping") || !strings.Contains(text, "30 seconds")) {
				t.Fatal("safe heartbeat timing facts missing")
			}
		})
	}
}

func TestPassivePublicRowOmitsLegacyUptime(t *testing.T) {
	for _, kind := range []string{"manual", "heartbeat"} {
		row := MonitorWithUptime{Monitor: &storage.Monitor{Name: "Reported service", Type: kind, Status: "up"}, UptimeLabel: "99.99%", DailyBars: []DailyBar{{Date: "2026-11-08", UptimePct: 99.99, HasData: true}}}
		text := textOf(render(t, statusMonitorRow(row)))
		if strings.Contains(text, "99.99%") || strings.Contains(text, "90 days ago") || !strings.Contains(text, "uptime is not calculated") {
			t.Fatalf("%s public row misrepresents passive samples: %s", kind, text)
		}
	}
}

func TestDashboardDistinguishesMeasuredZeroAndPassiveHistory(t *testing.T) {
	for _, kind := range []string{"http", "manual", "heartbeat"} {
		p := DashboardParams{Monitors: []*storage.Monitor{{ID: 1, Name: "Example", Type: kind}}, ResponseTimes: map[int64]int64{1: 0}}
		text := textOf(render(t, DashboardPage(p)))
		if got := strings.Contains(text, "0ms"); got != (kind == "http") {
			t.Errorf("%s zero response visibility = %v", kind, got)
		}
	}
}

func TestSLAFormPreservesArbitraryValidTargets(t *testing.T) {
	for _, target := range []float64{0, 99.123, 100} {
		p := MonitorFormParams{Monitor: &storage.Monitor{Type: "http", ID: 1, SLATarget: target}}
		doc := render(t, MonitorFormPage(p))
		found := false
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if id, _ := attr(n, "id"); id == "sla_target" {
				found = true
				for key, want := range map[string]string{"type": "number", "value": p.slaTargetValue(), "min": "0", "max": "100", "step": "any"} {
					if got, _ := attr(n, key); got != want {
						t.Errorf("target%v %s=%q; want%q", target, key, got, want)
					}
				}
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				walk(child)
			}
		}
		walk(doc)
		if !found {
			t.Fatal("SLA input missing")
		}
	}
}

func TestInactiveSLAClearIsExplicitAndLimitedToSavedTargets(t *testing.T) {
	for _, tc := range []struct {
		kind   string
		id     int64
		target float64
		want   bool
	}{
		{"manual", 1, 99.123, true}, {"heartbeat", 2, 100, true},
		{"manual", 1, 0, false}, {"heartbeat", 0, 0, false}, {"http", 3, 99.9, false},
	} {
		p := MonitorFormParams{Monitor: &storage.Monitor{ID: tc.id, Type: tc.kind, SLATarget: tc.target}}
		if got := namedControl(render(t, MonitorFormPage(p)), "Clear inactive target"); got != tc.want {
			t.Errorf("%s id%d target%v clear control=%v; want%v", tc.kind, tc.id, tc.target, got, tc.want)
		}
	}
}
