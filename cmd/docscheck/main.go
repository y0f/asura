// docscheck verifies the public Jekyll artifact without fetching external URLs.
package main

import (
	"encoding/xml"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"gopkg.in/yaml.v3"
)

type navigationEntry struct {
	Title string `yaml:"title"`
	URL   string `yaml:"url"`
}

type siteConfig struct {
	URL     string `yaml:"url"`
	BaseURL string `yaml:"baseurl"`
}

type document struct {
	file, title, description, canonical, socialURL string
	ids                                            map[string]bool
	links                                          []string
}

func readDocument(file string) (*document, error) {
	input, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	root, err := html.Parse(input)
	if err != nil {
		return nil, err
	}
	doc := &document{file: file, ids: make(map[string]bool)}
	counts := make(map[string]int)
	var visit func(*html.Node) error
	visit = func(node *html.Node) error {
		if node.Type == html.ElementNode {
			attrs := make(map[string]string, len(node.Attr))
			for _, attr := range node.Attr {
				attrs[attr.Key] = attr.Val
			}
			if id := attrs["id"]; id != "" {
				if doc.ids[id] {
					return fmt.Errorf("duplicate heading/element ID %q", id)
				}
				doc.ids[id] = true
			}
			if href := attrs["href"]; href != "" {
				doc.links = append(doc.links, href)
			}
			if src := attrs["src"]; src != "" {
				doc.links = append(doc.links, src)
			}
			switch node.Data {
			case "title":
				counts["title"]++
				if node.FirstChild != nil {
					doc.title = strings.TrimSpace(node.FirstChild.Data)
				}
			case "h1":
				counts["h1"]++
			case "link":
				if attrs["rel"] == "canonical" {
					counts["canonical"]++
					doc.canonical = attrs["href"]
				}
			case "meta":
				if attrs["name"] == "description" {
					counts["description"]++
					doc.description = strings.TrimSpace(attrs["content"])
				}
				if attrs["property"] == "og:url" {
					counts["og:url"]++
					doc.socialURL = attrs["content"]
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return nil, err
	}
	for _, field := range []string{"title", "h1", "canonical", "description", "og:url"} {
		if counts[field] != 1 {
			return nil, fmt.Errorf("expected one %s, found %d", field, counts[field])
		}
	}
	if doc.title == "" || doc.description == "" || !doc.ids["content"] {
		return nil, fmt.Errorf("missing title, description or accessible content target")
	}
	return doc, nil
}

func publicBase(config siteConfig) (*url.URL, error) {
	u, err := url.Parse(config.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("site url must be an HTTPS origin without credentials")
	}
	base := config.BaseURL
	if base != "" && (!strings.HasPrefix(base, "/") || strings.ContainsAny(base, "?#\\") || path.Clean(base) != base || strings.Contains(base, "%")) {
		return nil, fmt.Errorf("site baseurl must be a clean absolute path or empty")
	}
	u.Path = strings.TrimRight(base, "/") + "/"
	return u, nil
}

func artifactPath(base *url.URL, target *url.URL) (string, error) {
	if !strings.HasPrefix(target.Path, base.Path) {
		return "", fmt.Errorf("local URL escapes documentation base path: %s", target.Path)
	}
	relative := strings.TrimPrefix(target.Path, base.Path)
	if relative == "" || strings.HasSuffix(relative, "/") {
		relative += "index.html"
	}
	if !fs.ValidPath(relative) || strings.Contains(relative, "\\") {
		return "", fmt.Errorf("invalid documentation path: %s", target.Path)
	}
	return relative, nil
}

func checkSite(root string, base *url.URL, navigation []navigationEntry) error {
	if len(navigation) == 0 {
		return fmt.Errorf("public navigation is empty")
	}
	documents := make(map[string]*document, len(navigation))
	canonicalURLs := make(map[string]bool, len(navigation))
	titles, descriptions := make(map[string]bool), make(map[string]bool)
	for _, entry := range navigation {
		if entry.Title == "" || !strings.HasPrefix(entry.URL, "/") || strings.HasPrefix(entry.URL, "//") || strings.ContainsAny(entry.URL, "?#\\%") || path.Clean(entry.URL) != entry.URL {
			return fmt.Errorf("invalid public navigation entry %q", entry.URL)
		}
		pageURL := base.ResolveReference(&url.URL{Path: strings.TrimPrefix(entry.URL, "/")})
		file, err := artifactPath(base, pageURL)
		if err != nil {
			return err
		}
		if !strings.HasSuffix(file, ".html") || documents[file] != nil {
			return fmt.Errorf("invalid or repeated public page %q", file)
		}
		doc, err := readDocument(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		canonical := pageURL.String()
		if doc.canonical != canonical || doc.socialURL != canonical {
			return fmt.Errorf("%s: canonical/social URL must equal %s", file, canonical)
		}
		if titles[doc.title] || descriptions[doc.description] {
			return fmt.Errorf("%s: title and description must be unique", file)
		}
		titles[doc.title], descriptions[doc.description] = true, true
		canonicalURLs[canonical] = true
		documents[file] = doc
	}
	if err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular file in public artifact: %s", file)
		}
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if documents[relative] == nil && relative != "sitemap.xml" && relative != "robots.txt" && !strings.HasPrefix(relative, "assets/") {
			return fmt.Errorf("unexpected file in public artifact: %s", relative)
		}
		if strings.HasPrefix(relative, "assets/") && (strings.HasSuffix(relative, ".html") || strings.HasSuffix(relative, ".md")) {
			return fmt.Errorf("unexpected document in public assets: %s", relative)
		}
		return nil
	}); err != nil {
		return err
	}
	for file, doc := range documents {
		pageURL, _ := url.Parse(doc.canonical)
		for _, link := range doc.links {
			reference, err := url.Parse(link)
			if err != nil {
				return fmt.Errorf("%s: invalid URL %q", file, link)
			}
			target := pageURL.ResolveReference(reference)
			if target.Scheme != base.Scheme || target.Host != base.Host {
				continue
			}
			// Fully qualified links may point to another project on the same
			// host. Relative links must remain inside this deployment path.
			if reference.IsAbs() && !strings.HasPrefix(target.Path, base.Path) {
				continue
			}
			targetFile, err := artifactPath(base, target)
			if err != nil {
				return fmt.Errorf("%s: %w", file, err)
			}
			info, err := os.Stat(filepath.Join(root, filepath.FromSlash(targetFile)))
			if err != nil || !info.Mode().IsRegular() {
				return fmt.Errorf("%s: missing local link target %q", file, link)
			}
			if targetDoc := documents[targetFile]; targetDoc != nil && target.Fragment != "" && !targetDoc.ids[target.Fragment] {
				return fmt.Errorf("%s: missing anchor in %q", file, link)
			}
		}
	}
	return checkSitemap(filepath.Join(root, "sitemap.xml"), canonicalURLs)
}

func checkSitemap(file string, expected map[string]bool) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	var sitemap struct {
		XMLName xml.Name `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 urlset"`
		URLs    []struct {
			Location string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(data, &sitemap); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, item := range sitemap.URLs {
		if !expected[item.Location] || seen[item.Location] {
			return fmt.Errorf("unexpected or duplicate sitemap URL %q", item.Location)
		}
		seen[item.Location] = true
	}
	var missing []string
	for location := range expected {
		if !seen[location] {
			missing = append(missing, location)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("sitemap is missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func main() {
	root := flag.String("dir", ".build/docs", "compiled Jekyll site directory")
	configFile := flag.String("config", "docs/_config.yml", "Jekyll site configuration")
	navigationFile := flag.String("navigation", "docs/_data/navigation.yml", "public page catalog")
	flag.Parse()
	err := run(*root, *configFile, *navigationFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Documentation links, anchors, metadata and public artifact passed")
}

func run(root, configFile, navigationFile string) error {
	configData, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}
	var config siteConfig
	if err := yaml.Unmarshal(configData, &config); err != nil {
		return err
	}
	base, err := publicBase(config)
	if err != nil {
		return err
	}
	navigationData, err := os.ReadFile(navigationFile)
	if err != nil {
		return err
	}
	var navigation []navigationEntry
	if err := yaml.Unmarshal(navigationData, &navigation); err != nil {
		return err
	}
	return checkSite(root, base, navigation)
}
