package views

import (
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
	"golang.org/x/net/html"
)

func TestImportResultKeepsEnrollmentURLsInDistinctCopyableFields(t *testing.T) {
	p := ImportResultParams{
		LayoutParams: LayoutParams{BasePath: "/asura", SuperAdmin: true},
		Stats:        storage.ConfigurationImportResult{Monitors: 2, Skipped: 3},
		Heartbeats: []ImportedHeartbeatEnrollment{
			{MonitorID: 7, MonitorName: "Backup <nightly>", PingURL: "https://example.test/asura/api/v1/heartbeat/first"},
			{MonitorID: 8, MonitorName: "Queue worker", PingURL: "https://example.test/asura/api/v1/heartbeat/second"},
		},
	}
	doc := render(t, ImportResultPage(p))
	fields := map[string]string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "nightly" {
				t.Fatal("monitor name was interpreted as markup")
			}
			if n.Data == "input" {
				id, _ := attr(n, "id")
				value, _ := attr(n, "value")
				if strings.Contains(value, "/heartbeat/") {
					if _, duplicate := fields[id]; duplicate {
						t.Fatalf("duplicate enrollment field %q", id)
					}
					fields[id] = value
					if _, ok := attr(n, "readonly"); !ok {
						t.Fatal("enrollment URL is not read-only")
					}
				}
			}
			if href, _ := attr(n, "href"); strings.Contains(href, "/heartbeat/") {
				t.Fatal("secret enrollment URL should not be a navigation link")
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if len(fields) != 2 || fields["import-heartbeat-7"] != p.Heartbeats[0].PingURL || fields["import-heartbeat-8"] != p.Heartbeats[1].PingURL {
		t.Fatalf("one-time fields lost their IDs or values: %v", fields)
	}
	if !strings.Contains(textOf(doc), "shown once") || !strings.Contains(textOf(doc), "Skipped existing entries") {
		t.Fatal("result must explain one-time enrollment and skipped entries")
	}
}

func TestImportWithoutNewHeartbeatsOmitsEnrollment(t *testing.T) {
	doc := render(t, ImportResultPage(ImportResultParams{}))
	if strings.Contains(textOf(doc), "Ping URL") {
		t.Fatal("skipped heartbeat imports must not invent a new credential")
	}
}
