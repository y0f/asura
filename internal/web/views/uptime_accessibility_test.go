package views

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"golang.org/x/net/html"
)

func TestDailyUptimeDataIncludesExactValuesAndMissingDays(t *testing.T) {
	days := []DailyBar{
		{Label: "2026-11-01", UptimePct: 99.25, HasData: true},
		{Label: "2026-11-02"},
	}
	doc := render(t, DailyUptimeData("API daily uptime", days))
	for _, value := range []string{"Daily uptime data", "API daily uptime", "2026-11-01", "99.25%", "2026-11-02", "No checks"} {
		if !strings.Contains(textOf(doc), value) {
			t.Errorf("daily data omits %q", value)
		}
	}
	if summary := uptimeSummary(MonitorWithUptime{DailyBars: []DailyBar{{}}}); summary != "No check data for the last 90 days." {
		t.Fatalf("missing data described as uptime: %s", summary)
	}
}

func elements(node *html.Node, name string) []*html.Node {
	var found []*html.Node
	if node.Type == html.ElementNode && node.Data == name {
		found = append(found, node)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		found = append(found, elements(child, name)...)
	}
	return found
}

func TestUptimeTooltipTextCannotBecomeMarkupOrJavaScript(t *testing.T) {
	label := `A "quoted" day & 'single' <script>bad()</script>`
	doc := render(t, templ.Raw(UptimeBarsSVG([]DailyBar{{Label: label, UptimePct: 50, HasData: true}})))
	var tooltip, handler string
	for _, node := range elements(doc, "rect") {
		tooltip, _ = attr(node, "data-tooltip")
		handler, _ = attr(node, "@mouseenter")
	}
	if tooltip != label+" — 50.00% uptime" {
		t.Fatalf("tooltip text did not round trip: %q", tooltip)
	}
	if strings.Contains(handler, label) || !strings.Contains(handler, "$el.dataset.tooltip") || len(elements(doc, "script")) != 0 {
		t.Fatal("tooltip text crossed its HTML/JavaScript boundary")
	}
}
