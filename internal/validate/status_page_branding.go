package validate

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/idna"
)

// StatusPageImageURL returns a safe image URL and its exact HTTPS origin for
// the public page's CSP. Relative images must start at this origin's root.
// No user information, wildcard host, protocol-relative URL or CSP delimiter
// can enter the policy, even through a legacy value that bypassed validation.
func StatusPageImageURL(raw string) (value, origin string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	if strings.ContainsAny(raw, "\\") || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", "", fmt.Errorf("image URL contains an invalid character")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid image URL")
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") && u.Scheme == "" && u.Host == "" {
		return raw, "", nil
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return "", "", fmt.Errorf("image URL must be root-relative or HTTPS without credentials")
	}
	host := u.Hostname()
	if net.ParseIP(host) == nil {
		host, err = idna.Lookup.ToASCII(host)
		if err != nil || host == "" {
			return "", "", fmt.Errorf("invalid image host")
		}
		for _, c := range host {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.') {
				return "", "", fmt.Errorf("invalid image host")
			}
		}
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	host = strings.ToLower(host)
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", "", fmt.Errorf("invalid image port")
		}
		host += ":" + strconv.Itoa(n)
	}
	u.Host = host
	return u.String(), "https://" + host, nil
}

func SanitizeStatusPageHeader(input string) string    { return sanitizeHTML(input) }
func SanitizeStatusPageAnalytics(input string) string { return sanitizeAnalyticsScript(input) }

// StatusPageHeaderImageOrigins reads only sanitized image sources. Links and
// text cannot expand the page's resource policy.
func StatusPageHeaderImageOrigins(header string) []string {
	tokenizer := html.NewTokenizer(strings.NewReader(header))
	var origins []string
	for {
		typeToken := tokenizer.Next()
		if typeToken == html.ErrorToken {
			return origins
		}
		if typeToken != html.StartTagToken && typeToken != html.SelfClosingTagToken {
			continue
		}
		token := tokenizer.Token()
		if token.DataAtom != atom.Img {
			continue
		}
		for _, attr := range token.Attr {
			if attr.Key != "src" {
				continue
			}
			if _, origin, err := StatusPageImageURL(attr.Val); err == nil && origin != "" {
				origins = append(origins, origin)
			}
		}
	}
}
