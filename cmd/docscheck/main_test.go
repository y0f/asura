package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func siteFixture(t *testing.T) (string, []navigationEntry) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	navigation := []navigationEntry{{"Home", "/"}, {"Guide", "/guide.html"}}
	for i, page := range navigation {
		file, canonical := "index.html", "https://docs.example.test/asura/"
		if i == 1 {
			file, canonical = "guide.html", canonical+"guide.html"
		}
		body := fmt.Sprintf(`<!doctype html><html lang="en"><head><title>%s</title><meta name="description" content="Description for %s"><link rel="canonical" href="%s"><meta property="og:url" content="%s"><link rel="stylesheet" href="/asura/assets/site.css"></head><body><a href="#content">Skip to content</a><main id="content"><h1>%s</h1><h2 id="setup">Setup</h2><a href="/asura/guide.html#setup">Guide</a></main></body></html>`, page.Title, page.Title, canonical, canonical, page.Title)
		writeFixture(t, root, file, body)
	}
	writeFixture(t, root, "assets/site.css", "body{color:#222}")
	writeFixture(t, root, "sitemap.xml", `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>https://docs.example.test/asura/</loc></url><url><loc>https://docs.example.test/asura/guide.html</loc></url></urlset>`)
	return root, navigation
}

func writeFixture(t *testing.T, root, file, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(file)), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func replaceFixture(t *testing.T, root, file, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("fixture does not contain %q", old)
	}
	writeFixture(t, root, file, strings.ReplaceAll(string(data), old, replacement))
}

func TestDocumentationArtifactChecksRealLinkAndPublicationFailures(t *testing.T) {
	base, err := publicBase(siteConfig{URL: "https://docs.example.test", BaseURL: "/asura"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		change     func(*testing.T, string)
	}{
		{"valid", "", func(*testing.T, string) {}},
		{"same host external project", "", func(t *testing.T, root string) {
			replaceFixture(t, root, "index.html", `href="/asura/guide.html#setup"`, `href="https://docs.example.test/another-project/"`)
		}},
		{"missing asset", "missing local link", func(t *testing.T, root string) {
			replaceFixture(t, root, "index.html", "assets/site.css", "assets/missing.css")
		}},
		{"broken cross-page anchor", "missing anchor", func(t *testing.T, root string) {
			replaceFixture(t, root, "index.html", "guide.html#setup", "guide.html#missing")
		}},
		{"broken base path", "escapes documentation", func(t *testing.T, root string) {
			replaceFixture(t, root, "index.html", `href="/asura/guide.html#setup"`, `href="/guide.html#setup"`)
		}},
		{"leaked audit", "unexpected file", func(t *testing.T, root string) {
			writeFixture(t, root, "internal-audit.md", "Not public")
		}},
		{"document hidden in assets", "unexpected document", func(t *testing.T, root string) {
			writeFixture(t, root, "assets/audit.md", "Not public")
		}},
		{"duplicate IDs", "duplicate heading", func(t *testing.T, root string) {
			replaceFixture(t, root, "index.html", `<h2 id="setup">`, `<h2 id="content">`)
		}},
		{"wrong canonical", "canonical/social", func(t *testing.T, root string) {
			replaceFixture(t, root, "guide.html", `https://docs.example.test/asura/guide.html`, `https://wrong.example.test/guide.html`)
		}},
		{"missing description", "expected one description", func(t *testing.T, root string) {
			replaceFixture(t, root, "index.html", `name="description"`, `name="unused"`)
		}},
		{"duplicate title", "must be unique", func(t *testing.T, root string) {
			replaceFixture(t, root, "guide.html", `<title>Guide</title>`, `<title>Home</title>`)
		}},
		{"wrong sitemap URL", "unexpected or duplicate sitemap", func(t *testing.T, root string) {
			replaceFixture(t, root, "sitemap.xml", `https://docs.example.test/asura/guide.html`, `https://docs.example.test/private.html`)
		}},
		{"missing sitemap URL", "sitemap is missing", func(t *testing.T, root string) {
			replaceFixture(t, root, "sitemap.xml", `<url><loc>https://docs.example.test/asura/guide.html</loc></url>`, ``)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, navigation := siteFixture(t)
			tc.change(t, root)
			err := checkSite(root, base, navigation)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDocumentationAtOriginRoot(t *testing.T) {
	root, navigation := siteFixture(t)
	for _, file := range []string{"index.html", "guide.html", "sitemap.xml"} {
		replaceFixture(t, root, file, "/asura/", "/")
	}
	base, err := publicBase(siteConfig{URL: "https://docs.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkSite(root, base, navigation); err != nil {
		t.Fatal(err)
	}
}

func TestDocumentationConfigRejectsUnsafeOriginsAndPaths(t *testing.T) {
	for _, config := range []siteConfig{
		{URL: "https://user:password@example.test"},
		{URL: "http://example.test"},
		{URL: "https://example.test/another-base"},
		{URL: "https://example.test", BaseURL: "/asura/../private"},
		{URL: "https://example.test", BaseURL: "//other.test"},
		{URL: "https://example.test", BaseURL: "/asura?query"},
	} {
		if _, err := publicBase(config); err == nil {
			t.Errorf("unsafe site configuration accepted: %+v", config)
		}
	}
}
