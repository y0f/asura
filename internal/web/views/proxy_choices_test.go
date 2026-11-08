package views

import (
	"testing"

	"golang.org/x/net/html"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestProxyChoicesRetainInvalidSelectionForCorrection(t *testing.T) {
	for _, tc := range []struct {
		name, kind, protocol, server string
		enabled, disabled            bool
	}{
		{"HTTP enabled", "http", "http", "", true, false},
		{"HTTP disabled remains editable", "http", "http", "", false, false},
		{"unsupported type", "ssh", "socks5", "", true, true},
		{"DNS without server", "dns", "socks5", "", true, true},
		{"DNS blank server", "dns", "socks5", "  ", true, true},
		{"DNS explicit server", "dns", "socks5", "1.1.1.1", true, false},
		{"DNS HTTP proxy", "dns", "http", "1.1.1.1", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := int64(7)
			p := MonitorFormParams{
				LayoutParams: LayoutParams{Perms: map[string]bool{"proxies.read": true, "monitors.assign": true}},
				Monitor:      &storage.Monitor{ID: 1, Type: tc.kind, ProxyID: &id},
				DNS:          storage.DNSSettings{Server: tc.server},
				Proxies:      []*storage.Proxy{{ID: id, Name: "Saved proxy", Protocol: tc.protocol, Enabled: tc.enabled}},
			}
			doc := render(t, MonitorFormPage(p))
			found := false
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Data == "select" {
					if value, _ := attr(n, "id"); value == "proxy_id" {
						for option := n.FirstChild; option != nil; option = option.NextSibling {
							if value, _ := attr(option, "value"); value != "7" {
								continue
							}
							found = true
							_, selected := attr(option, "selected")
							_, disabled := attr(option, "disabled")
							if !selected || disabled != tc.disabled {
								t.Fatalf("saved option selected=%v disabled=%v; want selected=true disabled=%v", selected, disabled, tc.disabled)
							}
						}
					}
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					walk(child)
				}
			}
			walk(doc)
			if !found {
				t.Fatal("saved proxy was silently removed from the selector")
			}
		})
	}
}
