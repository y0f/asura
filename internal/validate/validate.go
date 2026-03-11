package validate

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/storage"
)

var ValidMonitorTypes = map[string]bool{
	"http": true, "tcp": true, "dns": true,
	"icmp": true, "tls": true, "websocket": true, "command": true,
	"heartbeat": true, "docker": true, "domain": true,
	"grpc": true, "mqtt": true,
}

var ValidIncidentStatuses = map[string]bool{
	incident.StatusOpen: true, incident.StatusAcknowledged: true, incident.StatusResolved: true,
}

var _validNotificationTypes = map[string]bool{
	"webhook": true, "email": true, "telegram": true,
	"discord": true, "slack": true, "ntfy": true,
	"teams": true, "pagerduty": true, "opsgenie": true, "pushover": true,
	"googlechat": true, "matrix": true, "gotify": true,
}

var _validNotificationEvents = map[string]bool{
	"incident.created":      true,
	"incident.acknowledged": true,
	"incident.resolved":     true,
	"incident.reminder":     true,
	"content.changed":       true,
	"cert.changed":          true,
}

func ValidateMonitor(m *storage.Monitor) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(m.Name) > 255 {
		return fmt.Errorf("name must be at most 255 characters")
	}
	if len(m.Description) > 5000 {
		return fmt.Errorf("description must be at most 5000 characters")
	}
	if !ValidMonitorTypes[m.Type] {
		return fmt.Errorf("type must be one of: http, tcp, dns, icmp, tls, websocket, command, heartbeat, docker, domain, grpc, mqtt")
	}
	if m.Type == "heartbeat" {
		return nil
	}
	if strings.TrimSpace(m.Target) == "" {
		return fmt.Errorf("target is required")
	}
	if len(m.Target) > 2048 {
		return fmt.Errorf("target must be at most 2048 characters")
	}
	return validateMonitorLimits(m)
}

func validateMonitorLimits(m *storage.Monitor) error {
	if m.Interval < 5 {
		return fmt.Errorf("interval must be at least 5 seconds")
	}
	if m.Interval > 86400 {
		return fmt.Errorf("interval must be at most 86400 seconds")
	}
	if m.Timeout < 1 {
		return fmt.Errorf("timeout must be at least 1 second")
	}
	if m.Timeout > 300 {
		return fmt.Errorf("timeout must be at most 300 seconds")
	}
	if m.FailureThreshold < 1 {
		return fmt.Errorf("failure_threshold must be at least 1")
	}
	if m.SuccessThreshold < 1 {
		return fmt.Errorf("success_threshold must be at least 1")
	}
	if m.ResendInterval < 0 {
		return fmt.Errorf("resend_interval must be non-negative")
	}
	if m.ResendInterval > 86400 {
		return fmt.Errorf("resend_interval must be at most 86400 seconds")
	}
	return validateMonitorJSON(m)
}

func validateMonitorJSON(m *storage.Monitor) error {
	if len(m.Settings) > 0 && string(m.Settings) != "{}" {
		var s map[string]any
		if err := json.Unmarshal(m.Settings, &s); err != nil {
			return fmt.Errorf("settings must be a valid JSON object")
		}
	}
	if len(m.Assertions) > 0 && string(m.Assertions) != "[]" {
		var a []any
		if err := json.Unmarshal(m.Assertions, &a); err != nil {
			return fmt.Errorf("assertions must be a valid JSON array")
		}
	}
	if m.Type == "docker" {
		return validateDockerSettings(m)
	}
	if m.Type == "http" {
		return validateHTTPSettings(m)
	}
	return nil
}

func validateHTTPSettings(m *storage.Monitor) error {
	if len(m.Settings) == 0 {
		return nil
	}
	var s storage.HTTPSettings
	if err := json.Unmarshal(m.Settings, &s); err != nil {
		return fmt.Errorf("invalid http settings: %w", err)
	}

	if s.AuthMethod == "oauth2" {
		if s.OAuth2TokenURL == "" {
			return fmt.Errorf("oauth2 token URL is required")
		}
		u, err := url.Parse(s.OAuth2TokenURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return fmt.Errorf("oauth2 token URL must be a valid HTTP(S) URL")
		}
		if s.OAuth2ClientID == "" {
			return fmt.Errorf("oauth2 client ID is required")
		}
		if len(s.OAuth2ClientID) > 1024 {
			return fmt.Errorf("oauth2 client ID must be at most 1024 characters")
		}
		if s.OAuth2ClientSecret == "" {
			return fmt.Errorf("oauth2 client secret is required")
		}
		if len(s.OAuth2ClientSecret) > 4096 {
			return fmt.Errorf("oauth2 client secret must be at most 4096 characters")
		}
		if len(s.OAuth2Scopes) > 1024 {
			return fmt.Errorf("oauth2 scopes must be at most 1024 characters")
		}
		if len(s.OAuth2Audience) > 1024 {
			return fmt.Errorf("oauth2 audience must be at most 1024 characters")
		}
	}

	if s.MTLSEnabled {
		if s.MTLSClientCert == "" {
			return fmt.Errorf("mTLS client certificate is required")
		}
		if s.MTLSClientKey == "" {
			return fmt.Errorf("mTLS client key is required")
		}
		if _, err := tls.X509KeyPair([]byte(s.MTLSClientCert), []byte(s.MTLSClientKey)); err != nil {
			return fmt.Errorf("mTLS cert/key pair invalid: %w", err)
		}
		if s.MTLSCACert != "" {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM([]byte(s.MTLSCACert)) {
				return fmt.Errorf("mTLS CA certificate is not valid PEM")
			}
		}
	}
	return nil
}

func validateDockerSettings(m *storage.Monitor) error {
	var ds storage.DockerSettings
	if len(m.Settings) > 0 {
		if err := json.Unmarshal(m.Settings, &ds); err != nil {
			return fmt.Errorf("invalid docker settings: %w", err)
		}
	}
	name := ds.ContainerName
	if name == "" {
		name = m.Target
	}
	if strings.ContainsAny(name, "/\\..") {
		return fmt.Errorf("container name contains invalid characters")
	}
	if ds.SocketPath != "" {
		if strings.Contains(ds.SocketPath, "..") {
			return fmt.Errorf("socket path must not contain path traversal")
		}
		if !strings.HasPrefix(ds.SocketPath, "/") {
			return fmt.Errorf("socket path must be an absolute path")
		}
	}
	return nil
}

func ValidateNotificationChannel(ch *storage.NotificationChannel) error {
	if strings.TrimSpace(ch.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(ch.Name) > 255 {
		return fmt.Errorf("name must be at most 255 characters")
	}
	if !_validNotificationTypes[ch.Type] {
		return fmt.Errorf("type must be one of: webhook, email, telegram, discord, slack, ntfy, teams, pagerduty, opsgenie, pushover, googlechat, matrix, gotify")
	}
	if len(ch.Settings) == 0 {
		return fmt.Errorf("settings is required")
	}
	for _, ev := range ch.Events {
		if !_validNotificationEvents[ev] {
			return fmt.Errorf("invalid event: %s", ev)
		}
	}
	return nil
}

var _hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func ValidateTag(t *storage.Tag) error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(t.Name) > 50 {
		return fmt.Errorf("name must be at most 50 characters")
	}
	if t.Color == "" {
		t.Color = "#808080"
	}
	if !_hexColorPattern.MatchString(t.Color) {
		return fmt.Errorf("color must be a valid hex color (e.g. #ff0000)")
	}
	return nil
}

func ValidateMonitorTags(tags []storage.MonitorTag) error {
	if len(tags) > 20 {
		return fmt.Errorf("at most 20 tags allowed")
	}
	for _, t := range tags {
		if len(t.Value) > 50 {
			return fmt.Errorf("tag value must be at most 50 characters")
		}
	}
	return nil
}

func ValidateMonitorGroup(g *storage.MonitorGroup) error {
	if strings.TrimSpace(g.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(g.Name) > 255 {
		return fmt.Errorf("name must be at most 255 characters")
	}
	return nil
}

func ValidateStatusPage(sp *storage.StatusPage) error {
	if strings.TrimSpace(sp.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if len(sp.Title) > 200 {
		return fmt.Errorf("title must be at most 200 characters")
	}
	if strings.TrimSpace(sp.Slug) == "" {
		return fmt.Errorf("slug is required")
	}
	sp.Slug = ValidateSlug(sp.Slug)
	if len(sp.Description) > 1000 {
		return fmt.Errorf("description must be at most 1000 characters")
	}
	sp.CustomCSS = sanitizeCSS(sp.CustomCSS)
	if len(sp.LogoURL) > 2048 {
		return fmt.Errorf("logo_url must be at most 2048 characters")
	}
	if len(sp.FaviconURL) > 2048 {
		return fmt.Errorf("favicon_url must be at most 2048 characters")
	}
	if len(sp.CustomHeaderHTML) > 5000 {
		return fmt.Errorf("custom_header_html must be at most 5000 characters")
	}
	sp.CustomHeaderHTML = sanitizeHTML(sp.CustomHeaderHTML)
	if len(sp.AnalyticsScript) > 5000 {
		return fmt.Errorf("analytics_script must be at most 5000 characters")
	}
	sp.AnalyticsScript = sanitizeAnalyticsScript(sp.AnalyticsScript)
	return nil
}

func ValidateProxy(p *storage.Proxy) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if len(p.Name) > 255 {
		return fmt.Errorf("name must be at most 255 characters")
	}
	if p.Protocol != "http" && p.Protocol != "socks5" {
		return fmt.Errorf("protocol must be http or socks5")
	}
	if strings.TrimSpace(p.Host) == "" {
		return fmt.Errorf("host is required")
	}
	if len(p.Host) > 255 {
		return fmt.Errorf("host must be at most 255 characters")
	}
	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

func ValidateMaintenanceWindow(mw *storage.MaintenanceWindow) error {
	if strings.TrimSpace(mw.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if mw.StartTime.IsZero() {
		return fmt.Errorf("start_time is required")
	}
	if mw.EndTime.IsZero() {
		return fmt.Errorf("end_time is required")
	}
	if !mw.EndTime.After(mw.StartTime) {
		return fmt.Errorf("end_time must be after start_time")
	}
	if mw.Recurring != "" && mw.Recurring != "daily" && mw.Recurring != "weekly" && mw.Recurring != "monthly" {
		return fmt.Errorf("recurring must be one of: daily, weekly, monthly")
	}
	return nil
}

var _slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$|^[a-z0-9]$`)

var _reservedSlugs = map[string]bool{
	"login": true, "logout": true, "monitors": true, "incidents": true,
	"notifications": true, "maintenance": true, "logs": true,
	"status-settings": true, "status-pages": true, "static": true, "api": true,
	"groups": true,
}

func ValidateSlug(slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return "status"
	}
	if len(slug) > 50 {
		slug = slug[:50]
	}
	if !_slugPattern.MatchString(slug) {
		return "status"
	}
	if _reservedSlugs[slug] {
		return "status"
	}
	return slug
}

var _safeCSSProperties = map[string]bool{
	"color": true, "background": true, "background-color": true,
	"border": true, "border-color": true, "border-style": true,
	"border-width": true, "border-radius": true,
	"border-top": true, "border-right": true, "border-bottom": true, "border-left": true,
	"outline": true, "outline-color": true, "outline-style": true, "outline-width": true,
	"margin": true, "margin-top": true, "margin-right": true, "margin-bottom": true, "margin-left": true,
	"padding": true, "padding-top": true, "padding-right": true, "padding-bottom": true, "padding-left": true,
	"width": true, "height": true, "max-width": true, "max-height": true, "min-width": true, "min-height": true,
	"font-size": true, "font-weight": true, "font-family": true, "font-style": true,
	"text-align": true, "text-decoration": true, "text-transform": true,
	"line-height": true, "letter-spacing": true, "word-spacing": true,
	"display": true, "flex": true, "flex-direction": true, "flex-wrap": true,
	"justify-content": true, "align-items": true, "align-self": true, "gap": true,
	"grid-template-columns": true, "grid-template-rows": true, "grid-gap": true,
	"opacity": true, "visibility": true, "overflow": true, "overflow-x": true, "overflow-y": true,
	"position": true, "top": true, "right": true, "bottom": true, "left": true, "z-index": true,
	"box-shadow": true, "text-shadow": true,
	"transition": true, "transform": true,
	"cursor": true, "white-space": true, "word-break": true, "word-wrap": true,
	"list-style": true, "list-style-type": true,
	"vertical-align": true, "text-overflow": true,
	"content": true, "box-sizing": true, "float": true, "clear": true,
}

var _dangerousValuePattern = regexp.MustCompile(`(?i)(javascript|vbscript|expression\s*\(|behavior\s*:|@import|@charset|-moz-binding|url\s*\(|data\s*:)`)

var _cssCommentPattern = regexp.MustCompile(`/\*[\s\S]*?\*/`)

func sanitizeCSS(css string) string {
	if len(css) > 10000 {
		css = css[:10000]
	}

	css = strings.ReplaceAll(css, "<", "")
	css = strings.ReplaceAll(css, ">", "")
	css = strings.ReplaceAll(css, "\\", "")
	css = _cssCommentPattern.ReplaceAllString(css, "")

	var result strings.Builder
	rules := splitCSSRules(css)

	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}

		if strings.HasPrefix(rule, "@") {
			continue
		}

		braceIdx := strings.Index(rule, "{")
		if braceIdx == -1 {
			continue
		}

		selector := strings.TrimSpace(rule[:braceIdx])
		body := strings.TrimSpace(rule[braceIdx+1:])
		body = strings.TrimSuffix(body, "}")
		body = strings.TrimSpace(body)

		if selector == "" || body == "" {
			continue
		}

		sanitized := sanitizeCSSDeclarations(body)
		if sanitized == "" {
			continue
		}

		if result.Len() > 0 {
			result.WriteString("\n")
		}
		result.WriteString(selector)
		result.WriteString(" { ")
		result.WriteString(sanitized)
		result.WriteString(" }")
	}

	return result.String()
}

func splitCSSRules(css string) []string {
	var rules []string
	depth := 0
	start := 0
	for i, ch := range css {
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				rules = append(rules, css[start:i+1])
				start = i + 1
			}
		}
	}
	return rules
}

// sanitizeHTML strips dangerous tags and attributes from HTML, allowing only
// safe structural elements. Used for CustomHeaderHTML on public status pages.
func sanitizeHTML(input string) string {
	if input == "" {
		return ""
	}
	tokenizer := html.NewTokenizer(strings.NewReader(input))
	var buf strings.Builder
	skipDepth := 0
	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return buf.String()
		case html.TextToken:
			if skipDepth == 0 {
				buf.WriteString(html.EscapeString(tokenizer.Token().Data))
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			t := tokenizer.Token()
			tag := t.DataAtom
			if !_safeHTMLTags[tag] {
				if tt == html.StartTagToken {
					skipDepth++
				}
				continue
			}
			if skipDepth > 0 {
				continue
			}
			buf.WriteByte('<')
			buf.WriteString(t.Data)
			for _, a := range t.Attr {
				key := strings.ToLower(a.Key)
				if strings.HasPrefix(key, "on") {
					continue
				}
				if !_safeHTMLAttrs[key] {
					continue
				}
				val := strings.TrimSpace(a.Val)
				valLower := strings.ToLower(val)
				if (key == "href" || key == "src" || key == "action") &&
					!strings.HasPrefix(valLower, "http://") &&
					!strings.HasPrefix(valLower, "https://") &&
					!strings.HasPrefix(valLower, "/") &&
					!strings.HasPrefix(valLower, "#") {
					continue
				}
				buf.WriteByte(' ')
				buf.WriteString(key)
				buf.WriteString(`="`)
				buf.WriteString(html.EscapeString(val))
				buf.WriteByte('"')
			}
			if tt == html.SelfClosingTagToken {
				buf.WriteString("/>")
			} else {
				buf.WriteByte('>')
			}
		case html.EndTagToken:
			t := tokenizer.Token()
			if !_safeHTMLTags[t.DataAtom] {
				if skipDepth > 0 {
					skipDepth--
				}
				continue
			}
			if skipDepth > 0 {
				continue
			}
			buf.WriteString("</")
			buf.WriteString(t.Data)
			buf.WriteByte('>')
		}
	}
}

var _safeHTMLTags = map[atom.Atom]bool{
	atom.Div: true, atom.Span: true, atom.P: true, atom.A: true,
	atom.Img: true, atom.Br: true, atom.Hr: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Strong: true, atom.Em: true, atom.B: true, atom.I: true, atom.U: true, atom.Small: true,
	atom.Ul: true, atom.Ol: true, atom.Li: true,
	atom.Blockquote: true, atom.Code: true, atom.Pre: true,
	atom.Table: true, atom.Thead: true, atom.Tbody: true, atom.Tr: true, atom.Th: true, atom.Td: true,
	atom.Figure: true, atom.Figcaption: true,
}

var _safeHTMLAttrs = map[string]bool{
	"class": true, "id": true, "style": true, "title": true, "alt": true,
	"href": true, "src": true, "target": true, "rel": true,
	"width": true, "height": true, "loading": true,
	"colspan": true, "rowspan": true, "align": true,
}

// sanitizeAnalyticsScript allows only <script> tags with a src attribute
// pointing to https URLs. Inline script bodies are stripped.
func sanitizeAnalyticsScript(input string) string {
	if input == "" {
		return ""
	}
	tokenizer := html.NewTokenizer(strings.NewReader(input))
	var buf strings.Builder
	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			return buf.String()
		case html.StartTagToken, html.SelfClosingTagToken:
			t := tokenizer.Token()
			if t.DataAtom != atom.Script {
				continue
			}
			var src string
			var safeAttrs []html.Attribute
			for _, a := range t.Attr {
				key := strings.ToLower(a.Key)
				if strings.HasPrefix(key, "on") {
					continue
				}
				if key == "src" {
					val := strings.TrimSpace(a.Val)
					if strings.HasPrefix(strings.ToLower(val), "https://") {
						src = val
						safeAttrs = append(safeAttrs, html.Attribute{Key: "src", Val: val})
					}
				} else if key == "async" || key == "defer" || key == "type" ||
					key == "crossorigin" || key == "integrity" ||
					key == "data-domain" || key == "data-website-id" ||
					key == "data-host-url" || key == "data-do-not-track" ||
					key == "data-api" || key == "data-exclude" {
					safeAttrs = append(safeAttrs, html.Attribute{Key: key, Val: a.Val})
				}
			}
			if src == "" {
				continue
			}
			buf.WriteString("<script")
			for _, a := range safeAttrs {
				buf.WriteByte(' ')
				buf.WriteString(a.Key)
				buf.WriteString(`="`)
				buf.WriteString(html.EscapeString(a.Val))
				buf.WriteByte('"')
			}
			buf.WriteString("></script>\n")
		case html.EndTagToken:
			// script end tags handled inline above
		}
	}
}

func sanitizeCSSDeclarations(body string) string {
	declarations := strings.Split(body, ";")
	var safe []string

	for _, decl := range declarations {
		decl = strings.TrimSpace(decl)
		if decl == "" {
			continue
		}

		parts := strings.SplitN(decl, ":", 2)
		if len(parts) != 2 {
			continue
		}

		prop := strings.TrimSpace(strings.ToLower(parts[0]))
		value := strings.TrimSpace(parts[1])

		if !_safeCSSProperties[prop] {
			continue
		}

		if _dangerousValuePattern.MatchString(value) {
			continue
		}

		safe = append(safe, prop+": "+value)
	}

	return strings.Join(safe, "; ")
}
