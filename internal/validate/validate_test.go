package validate

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/internal/storage"
)

func generateTestCertPEM(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certB := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyB := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return string(certB), string(keyB)
}

func validMonitor() *storage.Monitor {
	return &storage.Monitor{
		Name:             "Test",
		Type:             "http",
		Target:           "https://example.com",
		Interval:         60,
		Timeout:          10,
		FailureThreshold: 3,
		SuccessThreshold: 1,
		Tags:             []string{},
	}
}

func TestValidateMonitor(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(m *storage.Monitor)
		wantErr string
	}{
		{"valid", func(m *storage.Monitor) {}, ""},
		{"empty name", func(m *storage.Monitor) { m.Name = "" }, "name is required"},
		{"blank name", func(m *storage.Monitor) { m.Name = "   " }, "name is required"},
		{"name too long", func(m *storage.Monitor) { m.Name = strings.Repeat("a", 256) }, "at most 255"},
		{"invalid type", func(m *storage.Monitor) { m.Type = "ftp" }, "type must be one of"},
		{"empty target", func(m *storage.Monitor) { m.Target = "" }, "target is required"},
		{"target too long", func(m *storage.Monitor) { m.Target = strings.Repeat("x", 2049) }, "at most 2048"},
		{"heartbeat no target", func(m *storage.Monitor) { m.Type = "heartbeat"; m.Target = "" }, ""},
		{"interval too low", func(m *storage.Monitor) { m.Interval = 4 }, "at least 5"},
		{"interval too high", func(m *storage.Monitor) { m.Interval = 86401 }, "at most 86400"},
		{"timeout too low", func(m *storage.Monitor) { m.Timeout = 0 }, "at least 1"},
		{"timeout too high", func(m *storage.Monitor) { m.Timeout = 301 }, "at most 300"},
		{"failure threshold zero", func(m *storage.Monitor) { m.FailureThreshold = 0 }, "at least 1"},
		{"success threshold zero", func(m *storage.Monitor) { m.SuccessThreshold = 0 }, "at least 1"},
		{"resend interval zero", func(m *storage.Monitor) { m.ResendInterval = 0 }, ""},
		{"resend interval valid", func(m *storage.Monitor) { m.ResendInterval = 300 }, ""},
		{"resend interval negative", func(m *storage.Monitor) { m.ResendInterval = -1 }, "resend_interval must be non-negative"},
		{"resend interval too high", func(m *storage.Monitor) { m.ResendInterval = 86401 }, "resend_interval must be at most 86400"},
		{"invalid settings json", func(m *storage.Monitor) { m.Settings = json.RawMessage("not json") }, "valid JSON object"},
		{"invalid assertions json", func(m *storage.Monitor) { m.Assertions = json.RawMessage("not json") }, "valid JSON array"},
		{"valid settings", func(m *storage.Monitor) { m.Settings = json.RawMessage(`{"method":"GET"}`) }, ""},
		{"valid assertions", func(m *storage.Monitor) { m.Assertions = json.RawMessage(`[{"type":"status_code"}]`) }, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validMonitor()
			tt.modify(m)
			err := ValidateMonitor(m)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestValidateNotificationChannel(t *testing.T) {
	tests := []struct {
		name    string
		ch      *storage.NotificationChannel
		wantErr string
	}{
		{
			"valid",
			&storage.NotificationChannel{
				Name: "Hook", Type: "webhook",
				Settings: json.RawMessage(`{"url":"https://example.com"}`),
				Events:   []string{"incident.created"},
			},
			"",
		},
		{
			"valid reminder event",
			&storage.NotificationChannel{
				Name: "Hook", Type: "webhook",
				Settings: json.RawMessage(`{"url":"https://example.com"}`),
				Events:   []string{"incident.created", "incident.reminder"},
			},
			"",
		},
		{
			"empty name",
			&storage.NotificationChannel{Name: "", Type: "webhook", Settings: json.RawMessage("{}")},
			"name is required",
		},
		{
			"name too long",
			&storage.NotificationChannel{Name: strings.Repeat("n", 256), Type: "webhook", Settings: json.RawMessage("{}")},
			"at most 255",
		},
		{
			"invalid type",
			&storage.NotificationChannel{Name: "X", Type: "sms", Settings: json.RawMessage("{}")},
			"type must be one of",
		},
		{
			"empty settings",
			&storage.NotificationChannel{Name: "X", Type: "webhook"},
			"settings is required",
		},
		{
			"invalid event",
			&storage.NotificationChannel{
				Name: "X", Type: "webhook",
				Settings: json.RawMessage("{}"),
				Events:   []string{"bad.event"},
			},
			"invalid event",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNotificationChannel(tt.ch)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestValidateMaintenanceWindow(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)

	tests := []struct {
		name    string
		mw      *storage.MaintenanceWindow
		wantErr string
	}{
		{"valid", &storage.MaintenanceWindow{Name: "MW", StartTime: now, EndTime: later}, ""},
		{"valid recurring", &storage.MaintenanceWindow{Name: "MW", StartTime: now, EndTime: later, Recurring: "daily"}, ""},
		{"empty name", &storage.MaintenanceWindow{StartTime: now, EndTime: later}, "name is required"},
		{"zero start", &storage.MaintenanceWindow{Name: "MW", EndTime: later}, "start_time is required"},
		{"zero end", &storage.MaintenanceWindow{Name: "MW", StartTime: now}, "end_time is required"},
		{"end before start", &storage.MaintenanceWindow{Name: "MW", StartTime: later, EndTime: now}, "end_time must be after"},
		{"invalid recurring", &storage.MaintenanceWindow{Name: "MW", StartTime: now, EndTime: later, Recurring: "yearly"}, "recurring must be one of"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMaintenanceWindow(tt.mw)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestValidateMonitorGroup(t *testing.T) {
	tests := []struct {
		name    string
		g       *storage.MonitorGroup
		wantErr string
	}{
		{"valid", &storage.MonitorGroup{Name: "Group"}, ""},
		{"empty name", &storage.MonitorGroup{Name: ""}, "name is required"},
		{"blank name", &storage.MonitorGroup{Name: "   "}, "name is required"},
		{"name too long", &storage.MonitorGroup{Name: strings.Repeat("g", 256)}, "at most 255"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMonitorGroup(tt.g)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestValidateProxy(t *testing.T) {
	tests := []struct {
		name    string
		p       *storage.Proxy
		wantErr string
	}{
		{"valid http", &storage.Proxy{Name: "P", Protocol: "http", Host: "proxy.local", Port: 8080}, ""},
		{"valid socks5", &storage.Proxy{Name: "P", Protocol: "socks5", Host: "proxy.local", Port: 1080}, ""},
		{"empty name", &storage.Proxy{Protocol: "http", Host: "h", Port: 80}, "name is required"},
		{"bad protocol", &storage.Proxy{Name: "P", Protocol: "ftp", Host: "h", Port: 80}, "protocol must be"},
		{"empty host", &storage.Proxy{Name: "P", Protocol: "http", Port: 80}, "host is required"},
		{"port zero", &storage.Proxy{Name: "P", Protocol: "http", Host: "h", Port: 0}, "port must be"},
		{"port too high", &storage.Proxy{Name: "P", Protocol: "http", Host: "h", Port: 70000}, "port must be"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProxy(tt.p)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestValidateStatusPage(t *testing.T) {
	tests := []struct {
		name    string
		sp      *storage.StatusPage
		wantErr string
	}{
		{"valid", &storage.StatusPage{Title: "My Page", Slug: "my-page"}, ""},
		{"empty title", &storage.StatusPage{Slug: "slug"}, "title is required"},
		{"title too long", &storage.StatusPage{Title: strings.Repeat("t", 201), Slug: "slug"}, "at most 200"},
		{"empty slug", &storage.StatusPage{Title: "T"}, "slug is required"},
		{"description too long", &storage.StatusPage{Title: "T", Slug: "s", Description: strings.Repeat("d", 1001)}, "at most 1000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStatusPage(tt.sp)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestSanitizeCSSAllowsValidProperties(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"basic color", `.card { color: red; }`, `.card { color: red }`},
		{"multiple properties", `.card { color: #fff; background-color: #1a1a1a; border-radius: 8px; }`, `.card { color: #fff; background-color: #1a1a1a; border-radius: 8px }`},
		{"spacing properties", `.box { margin: 10px; padding: 20px 15px; }`, `.box { margin: 10px; padding: 20px 15px }`},
		{"font properties", `body { font-size: 16px; font-weight: bold; font-family: Arial, sans-serif; }`, `body { font-size: 16px; font-weight: bold; font-family: Arial, sans-serif }`},
		{"flex layout", `.flex { display: flex; flex-direction: column; gap: 1rem; }`, `.flex { display: flex; flex-direction: column; gap: 1rem }`},
		{"box shadow", `.shadow { box-shadow: 0 2px 4px rgba(0,0,0,0.2); }`, `.shadow { box-shadow: 0 2px 4px rgba(0,0,0,0.2) }`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeCSS(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeCSS() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeCSSBlocksDangerous(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"javascript url", `.x { background: url(javascript:alert(1)); }`},
		{"expression", `.x { width: expression(document.body.clientWidth); }`},
		{"import rule", `@import url("evil.css"); .x { color: red; }`},
		{"behavior", `.x { behavior: url(xss.htc); }`},
		{"moz-binding", `.x { -moz-binding: url("xss.xml#xss"); }`},
		{"data uri in value", `.x { background: data:text/html,<script>alert(1)</script>; }`},
		{"vbscript", `.x { background: vbscript:msgbox; }`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeCSS(tt.input)
			lower := strings.ToLower(got)
			for _, dangerous := range []string{"javascript", "expression", "@import", "behavior", "-moz-binding", "data:", "vbscript"} {
				if strings.Contains(lower, dangerous) {
					t.Errorf("sanitizeCSS() should not contain %q, got %q", dangerous, got)
				}
			}
		})
	}
}

func TestSanitizeCSSBlocksUnknownProperties(t *testing.T) {
	input := `.x { -webkit-animation: evil; unknown-prop: value; color: red; }`
	got := sanitizeCSS(input)
	if strings.Contains(got, "-webkit-animation") {
		t.Error("should strip -webkit-animation")
	}
	if strings.Contains(got, "unknown-prop") {
		t.Error("should strip unknown-prop")
	}
	if !strings.Contains(got, "color: red") {
		t.Error("should keep color: red")
	}
}

func TestSanitizeCSSStripsComments(t *testing.T) {
	input := `.x { color: red; /* comment */ font-size: 14px; }`
	got := sanitizeCSS(input)
	if strings.Contains(got, "comment") {
		t.Error("should strip CSS comments")
	}
	if !strings.Contains(got, "color: red") {
		t.Error("should keep color: red")
	}
	if !strings.Contains(got, "font-size: 14px") {
		t.Error("should keep font-size: 14px")
	}
}

func TestSanitizeCSSStripsHTMLTags(t *testing.T) {
	input := `.x { color: red; } <script>alert(1)</script>`
	got := sanitizeCSS(input)
	if strings.Contains(got, "script") {
		t.Error("should strip HTML tags")
	}
}

func TestSanitizeCSSMultipleRules(t *testing.T) {
	input := `.a { color: red; } .b { font-size: 14px; }`
	got := sanitizeCSS(input)
	if !strings.Contains(got, ".a") || !strings.Contains(got, ".b") {
		t.Errorf("should preserve multiple rules, got %q", got)
	}
}

func TestSanitizeCSSMaxLength(t *testing.T) {
	input := strings.Repeat("x", 20000)
	got := sanitizeCSS(input)
	if len(got) > 10000 {
		t.Error("should truncate at 10000 chars")
	}
}

func TestSanitizeCSSEmpty(t *testing.T) {
	if got := sanitizeCSS(""); got != "" {
		t.Errorf("empty input should give empty output, got %q", got)
	}
}

func TestValidateSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-page", "my-page"},
		{"test", "test"},
		{"", "status"},
		{"LOGIN", "status"},
		{"login", "status"},
		{"a", "a"},
		{"ab", "ab"},
		{"a-b", "a-b"},
		{"has spaces", "status"},
		{"a--b", "a--b"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ValidateSlug(tt.input); got != tt.want {
				t.Errorf("ValidateSlug(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidateDockerSettings(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		settings string
		wantErr  string
	}{
		{"valid container name", "my-container", `{}`, ""},
		{"name from settings", "ignored", `{"container_name":"my-app"}`, ""},
		{"invalid chars in name", "bad/name", `{}`, "invalid characters"},
		{"path traversal in socket", "ok", `{"socket_path":"/../etc/shadow"}`, "path traversal"},
		{"relative socket path", "ok", `{"socket_path":"var/run/docker.sock"}`, "absolute path"},
		{"valid socket path", "ok", `{"socket_path":"/var/run/docker.sock"}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &storage.Monitor{
				Name:             "Docker Test",
				Type:             "docker",
				Target:           tt.target,
				Interval:         30,
				Timeout:          5,
				FailureThreshold: 1,
				SuccessThreshold: 1,
			}
			if tt.settings != "" {
				m.Settings = json.RawMessage(tt.settings)
			}
			err := ValidateMonitor(m)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestValidateTag(t *testing.T) {
	tests := []struct {
		name    string
		tag     *storage.Tag
		wantErr string
	}{
		{"valid", &storage.Tag{Name: "prod", Color: "#ff0000"}, ""},
		{"valid default color", &storage.Tag{Name: "prod"}, ""},
		{"empty name", &storage.Tag{Name: "", Color: "#ff0000"}, "name is required"},
		{"name too long", &storage.Tag{Name: strings.Repeat("t", 51), Color: "#ff0000"}, "at most 50"},
		{"invalid color", &storage.Tag{Name: "tag", Color: "red"}, "valid hex color"},
		{"short hex", &storage.Tag{Name: "tag", Color: "#fff"}, "valid hex color"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTag(tt.tag)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestSanitizeHTML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain text", "Hello world", "Hello world"},
		{"safe tags", `<p>Hello <strong>world</strong></p>`, `<p>Hello <strong>world</strong></p>`},
		{"safe link", `<a href="https://example.com">link</a>`, `<a href="https://example.com">link</a>`},
		{"safe image", `<img src="https://img.example.com/logo.png" alt="Logo"/>`, `<img src="https://img.example.com/logo.png" alt="Logo"/>`},
		{"strips script", `<p>Hello</p><script>alert(1)</script>`, `<p>Hello</p>`},
		{"strips iframe", `<div><iframe src="https://evil.com"></iframe></div>`, `<div></div>`},
		{"strips object", `<object data="evil.swf"></object>`, ``},
		{"strips form", `<form action="/steal"><input></form>`, ``},
		{"strips event handlers", `<div onclick="alert(1)">click</div>`, `<div>click</div>`},
		{"strips onload", `<img src="https://img.example.com/x.png" onload="alert(1)"/>`, `<img src="https://img.example.com/x.png"/>`},
		{"strips onerror", `<img src="/logo.png" onerror="alert(1)"/>`, `<img src="/logo.png"/>`},
		{"strips javascript href", `<a href="javascript:alert(1)">xss</a>`, `<a>xss</a>`},
		{"strips data href", `<a href="data:text/html,<script>alert(1)</script>">xss</a>`, `<a>xss</a>`},
		{"strips style tag", `<style>body{display:none}</style><p>hi</p>`, `<p>hi</p>`},
		{"keeps class attribute", `<div class="banner">hi</div>`, `<div class="banner">hi</div>`},
		{"keeps style attribute", `<div style="color:red">hi</div>`, `<div style="color:red">hi</div>`},
		{"strips unknown attributes", `<div data-evil="x" draggable="true">hi</div>`, `<div>hi</div>`},
		{"nested safe", `<div><p><strong>bold</strong> and <em>italic</em></p></div>`, `<div><p><strong>bold</strong> and <em>italic</em></p></div>`},
		{"relative src ok", `<img src="/static/logo.png" alt="ok"/>`, `<img src="/static/logo.png" alt="ok"/>`},
		{"anchor href ok", `<a href="#section">jump</a>`, `<a href="#section">jump</a>`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeHTML(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeHTML() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeAnalyticsScript(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"valid plausible", `<script defer data-domain="example.com" src="https://plausible.io/js/script.js"></script>`, `<script defer="" data-domain="example.com" src="https://plausible.io/js/script.js"></script>` + "\n"},
		{"valid umami", `<script async src="https://analytics.umami.is/script.js" data-website-id="abc123"></script>`, `<script async="" src="https://analytics.umami.is/script.js" data-website-id="abc123"></script>` + "\n"},
		{"strips inline script", `<script>alert(document.cookie)</script>`, ``},
		{"strips http src", `<script src="http://evil.com/track.js"></script>`, ``},
		{"strips non-script tags", `<div>hello</div><script src="https://ok.com/a.js"></script>`, `<script src="https://ok.com/a.js"></script>` + "\n"},
		{"strips event handlers", `<script src="https://ok.com/a.js" onload="alert(1)"></script>`, `<script src="https://ok.com/a.js"></script>` + "\n"},
		{"strips javascript src", `<script src="javascript:alert(1)"></script>`, ``},
		{"plain text stripped", `alert('xss')`, ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeAnalyticsScript(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeAnalyticsScript() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateMonitorTags(t *testing.T) {
	tests := []struct {
		name    string
		tags    []storage.MonitorTag
		wantErr string
	}{
		{"valid empty", nil, ""},
		{"valid tags", []storage.MonitorTag{{TagID: 1, Value: "v1"}, {TagID: 2}}, ""},
		{"too many", func() []storage.MonitorTag {
			tags := make([]storage.MonitorTag, 21)
			for i := range tags {
				tags[i] = storage.MonitorTag{TagID: int64(i + 1)}
			}
			return tags
		}(), "at most 20 tags"},
		{"value too long", []storage.MonitorTag{{TagID: 1, Value: strings.Repeat("v", 51)}}, "at most 50"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMonitorTags(tt.tags)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			} else {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestValidateHTTPSettingsOAuth2(t *testing.T) {
	tests := []struct {
		name    string
		s       storage.HTTPSettings
		wantErr string
	}{
		{
			name: "valid oauth2",
			s: storage.HTTPSettings{
				AuthMethod:         "oauth2",
				OAuth2TokenURL:     "https://auth.example.com/token",
				OAuth2ClientID:     "client-id",
				OAuth2ClientSecret: "client-secret",
				OAuth2Scopes:       "read",
			},
		},
		{
			name: "missing token URL",
			s: storage.HTTPSettings{
				AuthMethod:         "oauth2",
				OAuth2ClientID:     "client-id",
				OAuth2ClientSecret: "client-secret",
			},
			wantErr: "oauth2 token URL is required",
		},
		{
			name: "invalid token URL",
			s: storage.HTTPSettings{
				AuthMethod:         "oauth2",
				OAuth2TokenURL:     "not-a-url",
				OAuth2ClientID:     "client-id",
				OAuth2ClientSecret: "client-secret",
			},
			wantErr: "oauth2 token URL must be a valid HTTP(S) URL",
		},
		{
			name: "missing client ID",
			s: storage.HTTPSettings{
				AuthMethod:         "oauth2",
				OAuth2TokenURL:     "https://auth.example.com/token",
				OAuth2ClientSecret: "client-secret",
			},
			wantErr: "oauth2 client ID is required",
		},
		{
			name: "missing client secret",
			s: storage.HTTPSettings{
				AuthMethod:     "oauth2",
				OAuth2TokenURL: "https://auth.example.com/token",
				OAuth2ClientID: "client-id",
			},
			wantErr: "oauth2 client secret is required",
		},
		{
			name: "non-oauth2 auth method skips",
			s:    storage.HTTPSettings{AuthMethod: "basic"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validMonitor()
			b, _ := json.Marshal(tt.s)
			m.Settings = b

			err := ValidateMonitor(m)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestValidateHTTPSettingsMTLS(t *testing.T) {
	certPEM, keyPEM := generateTestCertPEM(t)

	tests := []struct {
		name    string
		s       storage.HTTPSettings
		wantErr string
	}{
		{
			name: "mtls disabled",
			s:    storage.HTTPSettings{MTLSEnabled: false},
		},
		{
			name:    "missing client cert",
			s:       storage.HTTPSettings{MTLSEnabled: true, MTLSClientKey: keyPEM},
			wantErr: "mTLS client certificate is required",
		},
		{
			name:    "missing client key",
			s:       storage.HTTPSettings{MTLSEnabled: true, MTLSClientCert: certPEM},
			wantErr: "mTLS client key is required",
		},
		{
			name: "invalid cert/key pair",
			s: storage.HTTPSettings{
				MTLSEnabled:    true,
				MTLSClientCert: "not-a-cert",
				MTLSClientKey:  "not-a-key",
			},
			wantErr: "mTLS cert/key pair invalid",
		},
		{
			name: "invalid CA cert",
			s: storage.HTTPSettings{
				MTLSEnabled:    true,
				MTLSClientCert: certPEM,
				MTLSClientKey:  keyPEM,
				MTLSCACert:     "not-a-cert",
			},
			wantErr: "mTLS CA certificate is not valid PEM",
		},
		{
			name: "valid with cert and key",
			s: storage.HTTPSettings{
				MTLSEnabled:    true,
				MTLSClientCert: certPEM,
				MTLSClientKey:  keyPEM,
			},
		},
		{
			name: "valid with CA",
			s: storage.HTTPSettings{
				MTLSEnabled:    true,
				MTLSClientCert: certPEM,
				MTLSClientKey:  keyPEM,
				MTLSCACert:     certPEM,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validMonitor()
			b, _ := json.Marshal(tt.s)
			m.Settings = b

			err := ValidateMonitor(m)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}
