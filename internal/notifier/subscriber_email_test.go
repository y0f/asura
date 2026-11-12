package notifier

import (
	"strings"
	"testing"

	"github.com/y0f/asura/v3/internal/storage"
	"golang.org/x/net/html"
)

func TestSubscriberEmailTextCannotInjectMarkup(t *testing.T) {
	payload := `<a href="https://other.example">Unexpected action</a><img src=x onerror=alert(1)>`
	url := `https://status.example/unsubscribe?token=example&source="email"`
	for _, event := range []string{"incident.created", "incident.resolved", "future.event"} {
		for _, incident := range []*storage.Incident{nil, {MonitorName: payload, Cause: payload}} {
			body := buildSubscriberEmailBody(payload, event, payload, incident, nil, url)
			assertEmailAction(t, body, url)
		}
	}
	assertEmailAction(t, buildSubscriptionConfirmationBody(payload, url), url)
}

func assertEmailAction(t *testing.T, body, expectedURL string) {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var links, headings int
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "img", "script", "svg":
				t.Errorf("untrusted text created a %s element", n.Data)
			case "h1":
				headings++
			case "a":
				links++
				for _, attr := range n.Attr {
					if attr.Key == "href" && attr.Val != expectedURL {
						t.Errorf("unexpected action URL %q", attr.Val)
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	if links != 1 || headings != 1 {
		t.Fatalf("email has %d actions and %d headings, want one each", links, headings)
	}
}
