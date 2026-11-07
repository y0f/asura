package main

import (
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
	if err := generate(root, false); err != nil {
		t.Fatal(err)
	}
	if err := generate(root, true); err != nil {
		t.Fatalf("fresh output failed check: %v", err)
	}
	for _, p := range pages {
		b, err := os.ReadFile(filepath.Join(root, p.File()))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		for _, want := range []string{"<!DOCTYPE html>", "<title>" + p.Title, `href="api.html"`, `id="one-two"`, `id="one-two-2"`, `id="one-two-2-2"`, `aria-current="page"`} {
			if !strings.Contains(s, want) {
				t.Errorf("%s missing %q", p.File(), want)
			}
		}
		if strings.Contains(s, "\r") || strings.Contains(s, "alpine") || strings.Contains(s, "htmx") {
			t.Errorf("%s is not standalone LF HTML", p.File())
		}
	}
	path := filepath.Join(root, "index.html")
	if err := os.WriteFile(path, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := generate(root, true); err == nil {
		t.Fatal("stale output passed check")
	}
	if b, _ := os.ReadFile(path); string(b) != "stale" {
		t.Fatal("check mode changed output")
	}
}
