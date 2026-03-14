package views

import (
	"encoding/json"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/y0f/asura/internal/storage"
)

func StatusColor(status string) string {
	switch status {
	case "up":
		return "text-emerald-400"
	case "down":
		return "text-red-400"
	case "degraded", "paused":
		return "text-yellow-400"
	default:
		return "text-gray-500"
	}
}

func StatusBg(status string) string {
	switch status {
	case "up":
		return "bg-emerald-500/10 text-emerald-400 border-emerald-500/20"
	case "down":
		return "bg-red-500/10 text-red-400 border-red-500/20"
	case "degraded", "paused":
		return "bg-yellow-500/10 text-yellow-400 border-yellow-500/20"
	case "open":
		return "bg-red-500/10 text-red-400 border-red-500/20"
	case "acknowledged":
		return "bg-yellow-500/10 text-yellow-400 border-yellow-500/20"
	case "resolved":
		return "bg-emerald-500/10 text-emerald-400 border-emerald-500/20"
	default:
		return "bg-gray-500/10 text-gray-400 border-gray-500/20"
	}
}

func StatusDot(status string) string {
	switch status {
	case "up", "resolved":
		return "bg-emerald-400"
	case "down", "created":
		return "bg-red-400"
	case "degraded", "acknowledged", "paused":
		return "bg-yellow-400"
	default:
		return "bg-gray-500"
	}
}

func TimeAgo(t any) string {
	var tm time.Time
	switch v := t.(type) {
	case time.Time:
		tm = v
	case *time.Time:
		if v == nil {
			return "never"
		}
		tm = *v
	default:
		return ""
	}
	d := time.Since(tm)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	}
}

func FormatMs(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

func IncidentDuration(started time.Time, resolved *time.Time) string {
	end := time.Now()
	if resolved != nil {
		end = *resolved
	}
	d := end.Sub(started)
	if d < time.Minute {
		return "< 1m"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours()/24), int(d.Hours())%24)
}

func UptimeFmt(pct float64) string {
	if pct >= 99.995 {
		return "100%"
	}
	return fmt.Sprintf("%.2f%%", pct)
}

func UptimeColor(pct float64) string {
	if pct >= 99.9 {
		return "text-emerald-400"
	}
	if pct >= 99 {
		return "text-yellow-400"
	}
	return "text-red-400"
}

func UptimeBarColor(pct float64, hasData bool) string {
	if !hasData {
		return "bg-muted/20"
	}
	if pct >= 99 {
		return "bg-emerald-500"
	}
	if pct >= 95 {
		return "bg-yellow-500"
	}
	return "bg-red-500"
}

func UptimeBarTooltip(pct float64, hasData bool, label string) string {
	safe := jsSingleQuoteEscaper.Replace(label)
	if !hasData {
		return safe + " — No data"
	}
	if pct >= 99.995 {
		return safe + " — 100% uptime"
	}
	return fmt.Sprintf("%s — %.2f%% uptime", safe, pct)
}

func JSEscapeString(s string) string {
	return jsSingleQuoteEscaper.Replace(s)
}

func HttpStatusColor(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "text-emerald-400"
	case code >= 300 && code < 400:
		return "text-blue-400"
	case code >= 400 && code < 500:
		return "text-yellow-400"
	case code >= 500:
		return "text-red-400"
	default:
		return "text-gray-500"
	}
}

func CertDays(t *time.Time) int {
	if t == nil {
		return -1
	}
	return int(time.Until(*t).Hours() / 24)
}

func CertColor(t *time.Time) string {
	if t == nil {
		return "text-gray-500"
	}
	days := int(time.Until(*t).Hours() / 24)
	if days < 7 {
		return "text-red-400"
	}
	if days < 30 {
		return "text-yellow-400"
	}
	return "text-emerald-400"
}

func TypeLabel(t string) string {
	switch t {
	case "http":
		return "HTTP"
	case "tcp":
		return "TCP"
	case "dns":
		return "DNS"
	case "icmp":
		return "ICMP"
	case "tls":
		return "TLS"
	case "websocket":
		return "WebSocket"
	case "command":
		return "Command"
	case "heartbeat":
		return "Heartbeat"
	case "docker":
		return "Docker"
	case "domain":
		return "Domain"
	case "grpc":
		return "gRPC"
	case "mqtt":
		return "MQTT"
	case "manual":
		return "Manual"
	default:
		return t
	}
}

func FormatFloat(f float64) string {
	if f == 0 {
		return "-"
	}
	if f < 1000 {
		return fmt.Sprintf("%.0fms", f)
	}
	return fmt.Sprintf("%.1fs", f/1000)
}

var jsEscaper = strings.NewReplacer(
	"</", `<\/`,
	"<!--", `<\!--`,
)

var jsSingleQuoteEscaper = strings.NewReplacer(
	`\`, `\\`,
	`'`, `\'`,
	"\n", `\n`,
	"\r", `\r`,
	"</", `<\/`,
)

func ToJSON(v any) string {
	b, _ := json.Marshal(v)
	return jsEscaper.Replace(string(b))
}

func InSlice(needle int64, haystack []int64) bool {
	return slices.Contains(haystack, needle)
}

func ParseDNS(s string) []string {
	if s == "" {
		return nil
	}
	var records []string
	json.Unmarshal([]byte(s), &records)
	return records
}

func statusPageMonitorSort(data map[int64]storage.StatusPageMonitor, monID int64) string {
	if spm, ok := data[monID]; ok && spm.SortOrder != 0 {
		return strconv.Itoa(spm.SortOrder)
	}
	return ""
}

func statusPageMonitorGroup(data map[int64]storage.StatusPageMonitor, monID int64) string {
	if spm, ok := data[monID]; ok {
		return spm.GroupName
	}
	return ""
}

// RenderMarkdown converts a markdown subset to safe HTML for display.
// Supports: headers, bold, italic, inline code, fenced code blocks,
// unordered/ordered lists, links (http/https only), and paragraphs.
func RenderMarkdown(text string) string {
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	var buf strings.Builder
	var pendingLines []string
	var inCodeBlock bool
	var codeBuf strings.Builder
	var inList bool
	var listTag string

	flushPara := func() {
		if len(pendingLines) == 0 {
			return
		}
		buf.WriteString(`<p class="mb-2 text-[13px] text-muted-light leading-relaxed">`)
		buf.WriteString(mdInline(strings.Join(pendingLines, "\n")))
		buf.WriteString("</p>")
		pendingLines = nil
	}
	flushList := func() {
		if !inList {
			return
		}
		buf.WriteString("</" + listTag + ">")
		inList = false
		listTag = ""
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			if inCodeBlock {
				buf.WriteString(`<pre class="text-[12px] bg-surface-100 border border-line rounded p-3 overflow-x-auto mb-2"><code>`)
				buf.WriteString(html.EscapeString(codeBuf.String()))
				buf.WriteString("</code></pre>")
				codeBuf.Reset()
				inCodeBlock = false
			} else {
				flushPara()
				flushList()
				inCodeBlock = true
			}
			continue
		}
		if inCodeBlock {
			if codeBuf.Len() > 0 {
				codeBuf.WriteByte('\n')
			}
			codeBuf.WriteString(line)
			continue
		}

		switch {
		case strings.HasPrefix(line, "### "):
			flushPara()
			flushList()
			buf.WriteString(`<h3 class="text-[13px] font-semibold text-white mt-4 mb-1">`)
			buf.WriteString(mdInline(line[4:]))
			buf.WriteString("</h3>")
		case strings.HasPrefix(line, "## "):
			flushPara()
			flushList()
			buf.WriteString(`<h2 class="text-[14px] font-semibold text-white mt-4 mb-1">`)
			buf.WriteString(mdInline(line[3:]))
			buf.WriteString("</h2>")
		case strings.HasPrefix(line, "# "):
			flushPara()
			flushList()
			buf.WriteString(`<h1 class="text-[15px] font-semibold text-white mt-4 mb-2">`)
			buf.WriteString(mdInline(line[2:]))
			buf.WriteString("</h1>")
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			flushPara()
			if !inList || listTag != "ul" {
				flushList()
				buf.WriteString(`<ul class="list-disc list-inside mb-2 space-y-0.5 text-[13px] text-muted-light">`)
				inList = true
				listTag = "ul"
			}
			buf.WriteString("<li>")
			buf.WriteString(mdInline(line[2:]))
			buf.WriteString("</li>")
		default:
			if content, ok := mdOrderedItem(line); ok {
				flushPara()
				if !inList || listTag != "ol" {
					flushList()
					buf.WriteString(`<ol class="list-decimal list-inside mb-2 space-y-0.5 text-[13px] text-muted-light">`)
					inList = true
					listTag = "ol"
				}
				buf.WriteString("<li>")
				buf.WriteString(mdInline(content))
				buf.WriteString("</li>")
			} else if strings.TrimSpace(line) == "" {
				flushPara()
				flushList()
			} else {
				flushList()
				pendingLines = append(pendingLines, line)
			}
		}
	}

	flushPara()
	flushList()
	if inCodeBlock {
		buf.WriteString(`<pre class="text-[12px] bg-surface-100 border border-line rounded p-3 overflow-x-auto mb-2"><code>`)
		buf.WriteString(html.EscapeString(codeBuf.String()))
		buf.WriteString("</code></pre>")
	}
	return buf.String()
}

func mdOrderedItem(line string) (string, bool) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' ' {
		return line[i+2:], true
	}
	return "", false
}

// mdInline processes inline markdown within a single text span:
// bold (**text**), italic (*text*), inline code (`text`), links ([text](url)).
// All text content is HTML-escaped. Only http/https links are allowed.
func mdInline(s string) string {
	var buf strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '`' && i+1 < len(s):
			if j := strings.Index(s[i+1:], "`"); j >= 0 {
				buf.WriteString(`<code class="text-[12px] bg-surface-100 border border-line rounded px-1">`)
				buf.WriteString(html.EscapeString(s[i+1 : i+1+j]))
				buf.WriteString("</code>")
				i += j + 2
				continue
			}
		case c == '*' && i+1 < len(s) && s[i+1] == '*':
			if j := strings.Index(s[i+2:], "**"); j >= 0 {
				buf.WriteString("<strong>")
				buf.WriteString(html.EscapeString(s[i+2 : i+2+j]))
				buf.WriteString("</strong>")
				i += j + 4
				continue
			}
		case c == '_' && i+1 < len(s) && s[i+1] == '_':
			if j := strings.Index(s[i+2:], "__"); j >= 0 {
				buf.WriteString("<strong>")
				buf.WriteString(html.EscapeString(s[i+2 : i+2+j]))
				buf.WriteString("</strong>")
				i += j + 4
				continue
			}
		case c == '*':
			if j := strings.Index(s[i+1:], "*"); j >= 0 && (i+1 >= len(s) || s[i+1] != '*') {
				buf.WriteString("<em>")
				buf.WriteString(html.EscapeString(s[i+1 : i+1+j]))
				buf.WriteString("</em>")
				i += j + 2
				continue
			}
		case c == '_':
			if j := strings.Index(s[i+1:], "_"); j >= 0 && (i+1 >= len(s) || s[i+1] != '_') {
				buf.WriteString("<em>")
				buf.WriteString(html.EscapeString(s[i+1 : i+1+j]))
				buf.WriteString("</em>")
				i += j + 2
				continue
			}
		case c == '[':
			te := strings.Index(s[i+1:], "]")
			if te >= 0 && i+te+2 < len(s) && s[i+te+2] == '(' {
				if ue := strings.Index(s[i+te+3:], ")"); ue >= 0 {
					linkText := s[i+1 : i+1+te]
					linkURL := s[i+te+3 : i+te+3+ue]
					if strings.HasPrefix(linkURL, "http://") || strings.HasPrefix(linkURL, "https://") {
						buf.WriteString(`<a href="`)
						buf.WriteString(html.EscapeString(linkURL))
						buf.WriteString(`" target="_blank" rel="noopener" class="text-brand hover:underline">`)
						buf.WriteString(html.EscapeString(linkText))
						buf.WriteString("</a>")
					} else {
						buf.WriteString(html.EscapeString(linkText))
					}
					i += te + ue + 4
					continue
				}
			}
		case c == '\n':
			buf.WriteString("<br>")
			i++
			continue
		}
		switch c {
		case '&':
			buf.WriteString("&amp;")
		case '<':
			buf.WriteString("&lt;")
		case '>':
			buf.WriteString("&gt;")
		default:
			buf.WriteByte(c)
		}
		i++
	}
	return buf.String()
}

func SeverityBg(severity string) string {
	switch severity {
	case "critical":
		return "bg-red-500/10 text-red-400 border-red-500/20"
	case "major":
		return "bg-orange-500/10 text-orange-400 border-orange-500/20"
	case "minor":
		return "bg-yellow-500/10 text-yellow-400 border-yellow-500/20"
	case "warning":
		return "bg-blue-500/10 text-blue-400 border-blue-500/20"
	default:
		return "bg-gray-500/10 text-gray-400 border-gray-500/20"
	}
}

type HeatmapDay struct {
	Date      string
	UptimePct float64
	HasData   bool
	Label     string
	Weekday   int
	Week      int
}

func heatmapColor(pct float64, hasData bool) string {
	if !hasData {
		return "#1f2937"
	}
	if pct >= 99.995 {
		return "#34d399"
	}
	if pct >= 99 {
		return "#fbbf24"
	}
	if pct >= 95 {
		return "#f97316"
	}
	return "#f87171"
}

func HeatmapSVG(days []HeatmapDay) string {
	if len(days) == 0 {
		return ""
	}
	const cellSize = 11
	const cellGap = 2
	const step = cellSize + cellGap

	maxWeek := 0
	for _, d := range days {
		if d.Week > maxWeek {
			maxWeek = d.Week
		}
	}
	w := (maxWeek + 1) * step
	h := 7 * step

	var b strings.Builder
	fmt.Fprintf(&b, `<svg width="%d" height="%d" xmlns="http://www.w3.org/2000/svg">`, w, h)
	for _, d := range days {
		x := d.Week * step
		y := d.Weekday * step
		color := heatmapColor(d.UptimePct, d.HasData)
		tooltip := d.Label
		if d.HasData {
			tooltip += fmt.Sprintf(": %.2f%%", d.UptimePct)
		} else {
			tooltip += ": no data"
		}
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="2" fill="%s"><title>%s</title></rect>`,
			x, y, cellSize, cellSize, color, html.EscapeString(tooltip))
	}
	b.WriteString(`</svg>`)
	return b.String()
}
