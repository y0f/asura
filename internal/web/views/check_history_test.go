package views

import (
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/storage"
	"golang.org/x/net/html"
)

func TestHistoryFactsFollowObservationKindNotCurrentMonitorType(t *testing.T) {
	expires := time.Now().Add(48 * time.Hour)
	checks := []*storage.CheckResult{
		{ObservationKind: storage.ObservationProbe, Status: "up", ResponseTime: 0, StatusCode: 204, CertExpiry: &expires},
		{ObservationKind: storage.ObservationUnknown, Status: "up", ResponseTime: 999, StatusCode: 599, CertExpiry: &expires},
		{ObservationKind: storage.ObservationManual, Status: "up", ResponseTime: 777, StatusCode: 598, CertExpiry: &expires},
		{ObservationKind: storage.ObservationHeartbeat, Status: "up", ResponseTime: 666, StatusCode: 597, CertExpiry: &expires},
	}
	for _, currentType := range []string{"http", "manual", "heartbeat"} {
		t.Run(currentType, func(t *testing.T) {
			p := MonitorDetailParams{Monitor: &storage.Monitor{ID: 1, Type: currentType}, Checks: &storage.PaginatedResult{Data: checks}}
			doc := render(t, MonitorDetailPage(p))
			var rows [][]string
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Data == "tr" && hasAncestor(n, "tbody") {
					var row []string
					for cell := n.FirstChild; cell != nil; cell = cell.NextSibling {
						if cell.Data == "td" {
							row = append(row, textOf(cell))
						}
					}
					rows = append(rows, row)
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					walk(child)
				}
			}
			walk(doc)
			if len(rows) != 4 {
				t.Fatalf("history has%d rows", len(rows))
			}
			for i, row := range rows {
				if len(row) != 6 {
					t.Fatalf("history columns=%v", row)
				}
				if row[1] != observationLabel(checks[i].ObservationKind) {
					t.Errorf("wrong observation source: %v", row)
				}
				if i == 0 {
					if row[2] != "0ms" || row[3] != "204" || !strings.HasSuffix(row[4], "d") {
						t.Errorf("genuine historical facts lost: %v", row)
					}
				} else if row[2] != "—" || row[3] != "—" || row[4] != "—" {
					t.Errorf("non-probe row presented measurements: %v", row)
				}
			}
		})
	}
}

func TestLegacyOnlyHistoryDoesNotAdvertiseLatency(t *testing.T) {
	p := MonitorDetailParams{Monitor: &storage.Monitor{Type: "http"}, Checks: &storage.PaginatedResult{Data: []*storage.CheckResult{{ObservationKind: storage.ObservationUnknown, ResponseTime: 0, StatusCode: 200}}}}
	if columns := p.historyColumns(); columns.Response || columns.Code || columns.Certificate {
		t.Fatal("unverified history enabled measured columns")
	}
	text := textOf(render(t, MonitorDetailPage(p)))
	if !strings.Contains(text, "Legacy observation") || strings.Contains(text, "0ms") {
		t.Fatal("legacy data presented as measured zero")
	}
}

func TestLatestProtocolFactsRequireProbeProvenance(t *testing.T) {
	expires := time.Now().Add(48 * time.Hour)
	for kind, heading := range map[string]string{"tls": "TLS certificate", "dns": "DNS records"} {
		for _, source := range []string{storage.ObservationUnknown, storage.ObservationManual, storage.ObservationProbe} {
			p := MonitorDetailParams{
				LayoutParams: LayoutParams{Perms: map[string]bool{"monitors.read": true, "diagnostics.read": true}},
				Monitor:      &storage.Monitor{ID: 1, Type: kind},
				LatestCheck:  &storage.CheckResult{ObservationKind: source, CertExpiry: &expires, DNSRecords: `["192.0.2.1"]`},
			}
			if shown := strings.Contains(textOf(render(t, MonitorDetailPage(p))), heading); shown != (source == storage.ObservationProbe) {
				t.Errorf("%s %s protocol facts shown=%v", kind, source, shown)
			}
		}
	}
}
