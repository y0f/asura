// docsgen renders the documentation fragments as complete static HTML pages.
package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed shell.html
var shell string

type page struct{ Slug, Title string }

var pages = []page{
	{"home", "Overview"}, {"getting-started", "Getting started"},
	{"configuration", "Configuration"}, {"monitors", "Monitors"},
	{"notifications", "Notifications"}, {"escalation-policies", "Escalation policies"},
	{"sla", "SLA reports"}, {"status-pages", "Status pages"}, {"api", "API reference"},
	{"backup", "Backup and restore"}, {"deployment", "Deployment"}, {"architecture", "Architecture"},
}

func (p page) File() string {
	if p.Slug == "home" {
		return "index.html"
	}
	return p.Slug + ".html"
}

var (
	heading = regexp.MustCompile(`(?s)<h([1-4])>(.*?)</h[1-4]>`)
	tag     = regexp.MustCompile(`<[^>]*>`)
	nonWord = regexp.MustCompile(`[^a-z0-9]+`)
)

// Fragments are trusted repository content. Add stable anchors before embedding
// them, and translate the former hash-based page links to ordinary file links.
func content(source string) string {
	for _, p := range pages {
		source = strings.ReplaceAll(source, `href="#`+p.Slug+`"`, `href="`+p.File()+`"`)
	}
	used := make(map[string]bool)
	return heading.ReplaceAllStringFunc(source, func(match string) string {
		parts := heading.FindStringSubmatch(match)
		id := strings.Trim(nonWord.ReplaceAllString(strings.ToLower(html.UnescapeString(tag.ReplaceAllString(parts[2], ""))), "-"), "-")
		if id == "" {
			id = "section"
		}
		base := id
		for suffix := 2; used[id]; suffix++ {
			id = fmt.Sprintf("%s-%d", base, suffix)
		}
		used[id] = true
		return `<h` + parts[1] + ` id="` + id + `"><a class="heading-link" href="#` + id + `">` + parts[2] + `</a></h` + parts[1] + `>`
	})
}

func generate(root string, check bool) error {
	t, err := template.New("docs").Parse(strings.ReplaceAll(shell, "\r\n", "\n"))
	if err != nil {
		return err
	}
	var stale []string
	for _, p := range pages {
		fragment, err := os.ReadFile(filepath.Join(root, "pages", p.Slug+".html"))
		if err != nil {
			return err
		}
		source := strings.ReplaceAll(string(fragment), "\r\n", "\n")
		if strings.Count(source, "<h1>") != 1 {
			return fmt.Errorf("%s needs exactly one h1", p.Slug)
		}
		var out bytes.Buffer
		err = t.Execute(&out, struct {
			Page    page
			Pages   []page
			Content template.HTML
		}{p, pages, template.HTML(content(source))})
		if err != nil {
			return err
		}
		path := filepath.Join(root, p.File())
		if check {
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, out.Bytes()) {
				stale = append(stale, path)
			}
		} else if err := os.WriteFile(path, out.Bytes(), 0644); err != nil {
			return err
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("stale documentation: %s; run go run ./cmd/docsgen", strings.Join(stale, ", "))
	}
	return nil
}

func main() {
	check := flag.Bool("check", false, "check committed pages without changing files")
	flag.Parse()
	if err := generate("docs", *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
