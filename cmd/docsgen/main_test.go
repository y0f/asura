package main

import (
	"encoding/xml"
	"html"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticPagesAndStaleCheck(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "pages"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		fragment := "<h1>" + p.Title + "</h1>\r\n<h2>One &amp; two</h2>\r\n<h2>One &amp; two</h2>\r\n<h2>One two 2</h2>\r\n<a href=\"#api\">API</a>"
		if err := os.WriteFile(filepath.Join(root, "pages", p.Slug+".html"), []byte(fragment), 0644); err != nil {
			t.Fatal(err)
		}
	}
	const baseURL = "https://docs.example.test/team/asura/"
	if err := generate(root, baseURL, false); err != nil {
		t.Fatal(err)
	}
	if err := generate(root, baseURL, true); err != nil {
		t.Fatalf("fresh output failed check: %v", err)
	}
	base, _ := siteURL(baseURL)
	seenTitles, seenDescriptions := map[string]bool{}, map[string]bool{}
	for _, p := range pages {
		b, err := os.ReadFile(filepath.Join(root, p.File()))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for _, want := range []string{"<!DOCTYPE html>", "<title>" + html.EscapeString(p.SearchTitle()), `href="api.html"`, `id="one-two"`, `id="one-two-2"`, `id="one-two-2-2"`, `aria-current="page"`,
			`rel="canonical" href="` + canonicalURL(base, p) + `"`, `property="og:url" content="` + canonicalURL(base, p) + `"`,
			`name="description" content="` + html.EscapeString(p.Description) + `"`, `name="twitter:card" content="summary"`} {
			if !strings.Contains(s, want) {
				t.Errorf("%s missing %q", p.File(), want)
			}
		}
		if strings.Contains(s, "\r") || strings.Contains(s, "alpine") || strings.Contains(s, "htmx") {
			t.Errorf("%s is not standalone LF HTML", p.File())
		}
		if p.Description == "" || seenDescriptions[p.Description] || seenTitles[p.SearchTitle()] {
			t.Errorf("%s needs its own descriptive search metadata", p.File())
		}
		seenTitles[p.SearchTitle()], seenDescriptions[p.Description] = true, true
	}
	var sitemap struct {
		XMLName xml.Name
		URLs    []struct {
			Location string `xml:"loc"`
		} `xml:"url"`
	}
	data, err := os.ReadFile(filepath.Join(root, "sitemap.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(data, &sitemap); err != nil {
		t.Fatal(err)
	}
	if sitemap.XMLName.Space != "http://www.sitemaps.org/schemas/sitemap/0.9" || len(sitemap.URLs) != len(pages) {
		t.Fatalf("invalid or incomplete sitemap: %+v", sitemap)
	}
	for i, entry := range sitemap.URLs {
		if entry.Location != canonicalURL(base, pages[i]) {
			t.Errorf("sitemap location %q disagrees with canonical URL", entry.Location)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "sitemap.xml"), []byte("stale sitemap"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := generate(root, baseURL, true); err == nil || !strings.Contains(err.Error(), "sitemap.xml") {
		t.Fatalf("stale sitemap was not detected: %v", err)
	}
	if err := generate(root, baseURL, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "index.html")
	if err := os.WriteFile(path, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := generate(root, baseURL, true); err == nil {
		t.Fatal("stale output passed check")
	}
	if b, _ := os.ReadFile(path); string(b) != "stale" {
		t.Fatal("check mode changed output")
	}
}

func TestPublicBaseURL(t *testing.T) {
	for _, raw := range []string{"/asura/", "javascript:alert(1)", "https://user:secret@example.test/docs/", "https://example.test/docs/?x=1", "https://example.test/docs/#part"} {
		if _, err := siteURL(raw); err == nil {
			t.Errorf("invalid public base URL accepted: %q", raw)
		}
	}
	for _, tc := range []struct{ raw, want string }{
		{"https://docs.example.test", "https://docs.example.test/"},
		{"https://docs.example.test/asura", "https://docs.example.test/asura/"},
		{"https://docs.example.test/team%20docs/", "https://docs.example.test/team%20docs/"},
	} {
		base, err := siteURL(tc.raw)
		if err != nil || canonicalURL(base, pages[0]) != tc.want {
			t.Errorf("base %q: %v, %v", tc.raw, base, err)
		}
		if got := canonicalURL(base, pages[1]); got != tc.want+pages[1].File() {
			t.Errorf("nested page canonical=%q", got)
		}
	}
}
