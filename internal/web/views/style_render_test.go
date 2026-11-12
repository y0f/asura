package views

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/y0f/asura/v3/internal/storage"
	"golang.org/x/net/html"
)

func TestPublicCustomStyleBoundary(t *testing.T) {
	for _, css := range []string{
		`h1 { font-family: "A & B"; letter-spacing: 1px; }`,
		`@media (width < 600px) { h1 { letter-spacing: 7px; } }`,
		`</style><script>alert(1)</script><style>`,
		`</StYlE><img src=x onerror=alert(1)><style>`,
		`/* </style> --><script>alert(1)</script> */ h1 { color: blue; }`,
	} {
		t.Run(css, func(t *testing.T) {
			doc := render(t, templ.Raw(publicCustomStyle(css)))
			styles := 0
			var styleText string
			var inspect func(*html.Node)
			inspect = func(n *html.Node) {
				if n.Type == html.ElementNode {
					switch n.Data {
					case "script", "img":
						t.Errorf("custom CSS created a %s element", n.Data)
					case "style":
						styles++
						styleText = textOf(n)
					}
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					inspect(child)
				}
			}
			inspect(doc)
			if styles != 1 {
				t.Fatalf("rendered %d style elements, want one", styles)
			}
			if !strings.Contains(css, "</") && styleText != css {
				t.Errorf("valid quoted CSS changed: %q", styleText)
			}
		})
	}
}

func TestPublicPageRendersCustomStyle(t *testing.T) {
	css := `@media (width < 600px) { h1 { letter-spacing: 1px; font-family: "A & B"; } }`
	doc := render(t, PublicStatusPage(PublicStatusPageParams{
		Title: "Service status", Config: &storage.StatusPage{CustomCSS: css},
		Overall: "operational",
	}))
	var rendered []string
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "style" {
			rendered = append(rendered, textOf(n))
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	if len(rendered) != 1 || rendered[0] != css {
		t.Fatalf("public page did not render its configured CSS: %q", rendered)
	}
}
