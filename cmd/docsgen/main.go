// docsgen renders the documentation fragments as complete static HTML pages.
package main

import (
	"bytes"
	_ "embed"
	"encoding/xml"
	"flag"
	"fmt"
	"html"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed shell.html
var shell string

type page struct{ Slug, Title, Description string }

const defaultBaseURL = "https://y0f.github.io/asura/"

var pages = []page{
	{"home", "Overview", "Open-source uptime monitoring with alerts, incident tracking, SLA reports and public status pages. Self-host Asura as a single Go binary."},
	{"getting-started", "Getting started", "Install Asura, create your first uptime monitor and configure alerts. Start with Docker Compose or build the Go server from source."},
	{"upgrading", "Upgrading to v2", "Upgrade Asura with a consistent backup, review v2 permissions and update probe agents. Includes verification steps and database rollback guidance."},
	{"configuration", "Configuration", "Configure Asura's server, SQLite storage, API keys, sessions and monitoring settings. Includes environment variables and deployment options."},
	{"access-control", "Access control", "Manage Asura API keys, reusable roles and fine-grained permissions. Scope access to monitors and groups, preserve credentials and migrate legacy grants."},
	{"monitors", "Monitors", "Monitor websites, APIs and network services with Asura. Configure HTTP, DNS, TLS, heartbeat and other checks, assertions and remote probes."},
	{"notifications", "Notifications", "Send Asura uptime alerts through email, webhooks and messaging services. Configure notification channels, event filters and delivery settings."},
	{"escalation-policies", "Escalation policies", "Escalate unresolved Asura incidents with timed notification steps. Configure policy delays, repeats and monitor assignments."},
	{"sla", "SLA reports", "Review monthly uptime and error budgets in Asura. Learn how SLA targets, check samples, reports and budget alerts are calculated."},
	{"status-pages", "Status pages", "Publish service availability with Asura status pages. Configure monitor groups, branding, password protection and subscriber notifications."},
	{"api", "API reference", "Automate Asura through its REST API. Reference authentication, permissions, monitor management, incidents, status pages and reporting endpoints."},
	{"backup", "Backup and restore", "Back up and restore Asura safely. Preserve the database, configuration and encryption key, and understand configuration export limits."},
	{"deployment", "Deployment", "Deploy Asura with Docker or systemd. Configure HTTPS, reverse proxies, trusted headers, subpaths and persistent storage."},
	{"architecture", "Architecture", "Explore Asura's Go server, scheduler, probe agents, SQLite storage and embedded web interface. Understand its runtime and data flow."},
}

func (p page) SearchTitle() string {
	if p.Slug == "home" {
		return "Asura | Self-hosted uptime monitoring and status pages"
	}
	return p.Title + " | Asura documentation"
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

func canonicalURL(base *url.URL, p page) string {
	if p.Slug == "home" {
		return base.String()
	}
	return base.ResolveReference(&url.URL{Path: p.File()}).String()
}

func siteURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("base URL must be an absolute HTTP(S) URL without credentials, query or fragment")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u, nil
}

func generate(root, baseURL string, check bool) error {
	base, err := siteURL(baseURL)
	if err != nil {
		return err
	}
	t, err := template.New("docs").Parse(strings.ReplaceAll(shell, "\r\n", "\n"))
	if err != nil {
		return err
	}
	var stale []string
	write := func(name string, output []byte) error {
		path := filepath.Join(root, name)
		if check {
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, output) {
				stale = append(stale, path)
			}
			return nil
		}
		return os.WriteFile(path, output, 0644)
	}
	type sitemapURL struct {
		Location string `xml:"loc"`
	}
	sitemap := struct {
		XMLName xml.Name     `xml:"urlset"`
		XMLNS   string       `xml:"xmlns,attr"`
		URLs    []sitemapURL `xml:"url"`
	}{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9"}
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
			Page      page
			Pages     []page
			Canonical string
			Content   template.HTML
		}{p, pages, canonicalURL(base, p), template.HTML(content(source))})
		if err != nil {
			return err
		}
		if err := write(p.File(), out.Bytes()); err != nil {
			return err
		}
		sitemap.URLs = append(sitemap.URLs, sitemapURL{canonicalURL(base, p)})
	}
	sitemapXML, err := xml.MarshalIndent(sitemap, "", "  ")
	if err != nil {
		return err
	}
	if err := write("sitemap.xml", append([]byte(xml.Header), append(sitemapXML, '\n')...)); err != nil {
		return err
	}
	if len(stale) > 0 {
		return fmt.Errorf("stale documentation: %s; run go run ./cmd/docsgen", strings.Join(stale, ", "))
	}
	return nil
}

func main() {
	check := flag.Bool("check", false, "check committed pages without changing files")
	baseURL := flag.String("base-url", defaultBaseURL, "public documentation URL for canonical links and sitemap")
	flag.Parse()
	if err := generate("docs", *baseURL, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
