package checker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type DomainChecker struct {
	AllowPrivate bool
}

func (c *DomainChecker) Type() string { return "domain" }

func (c *DomainChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.DomainSettings
	if len(monitor.Settings) > 0 {
		if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
			return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
		}
	}

	warnDays := settings.WarnDaysBefore
	if warnDays <= 0 {
		warnDays = 30
	}

	domain := sanitizeDomain(monitor.Target)
	if domain == "" {
		return &Result{Status: "down", Message: "domain target is empty"}, nil
	}

	server := whoisServerForTLD(extractTLD(domain))
	if server == "" {
		return &Result{Status: "down", Message: fmt.Sprintf("no WHOIS server known for TLD: %s", extractTLD(domain))}, nil
	}

	timeout := time.Duration(monitor.Timeout) * time.Second
	baseDial := (&net.Dialer{Timeout: timeout, Control: safenet.MaybeDialControl(c.AllowPrivate)}).DialContext

	dialFn := baseDial
	if socks := ProxyDialer(monitor.ProxyURL, baseDial, c.AllowPrivate); socks != nil {
		dialFn = socks
	}

	body, elapsed, err := queryWhois(ctx, domain, server, timeout, dialFn)
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: err.Error()}, nil
	}

	return evaluateDomainExpiry(body, elapsed, warnDays)
}

func sanitizeDomain(target string) string {
	domain := strings.TrimSpace(target)
	if domain == "" {
		return ""
	}
	domain = strings.ToLower(domain)
	domain = strings.NewReplacer("\r", "", "\n", "").Replace(domain)

	parts := strings.Split(domain, ".")
	if len(parts) > 2 {
		domain = strings.Join(parts[len(parts)-2:], ".")
	}
	return domain
}

func queryWhois(ctx context.Context, domain, server string, timeout time.Duration, dialFn func(context.Context, string, string) (net.Conn, error)) (string, int64, error) {
	start := time.Now()
	conn, err := dialFn(ctx, "tcp", server+":43")
	if err != nil {
		return "", time.Since(start).Milliseconds(), fmt.Errorf("WHOIS connection failed: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(timeout))

	if _, err = fmt.Fprintf(conn, "%s\r\n", domain); err != nil {
		return "", time.Since(start).Milliseconds(), fmt.Errorf("WHOIS query failed: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 4096)
	var response strings.Builder
	for scanner.Scan() {
		response.WriteString(scanner.Text())
		response.WriteByte('\n')
		if response.Len() > 8192 {
			break
		}
	}

	body := response.String()
	elapsed := time.Since(start).Milliseconds()

	if body == "" && scanner.Err() != nil {
		return "", elapsed, fmt.Errorf("WHOIS read failed: %w", scanner.Err())
	}
	return body, elapsed, nil
}

func evaluateDomainExpiry(body string, elapsed int64, warnDays int) (*Result, error) {
	lower := strings.ToLower(body)
	if strings.Contains(lower, "no match") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no data found") {
		return &Result{Status: "down", ResponseTime: elapsed, Message: "domain not found in WHOIS", Body: body}, nil
	}

	expiry, err := parseWhoisExpiry(body)
	if err != nil {
		return &Result{Status: "down", ResponseTime: elapsed, Message: fmt.Sprintf("could not parse expiry date: %v", err), Body: body}, nil
	}

	days := int(time.Until(expiry).Hours() / 24)
	result := &Result{
		Status:       "up",
		ResponseTime: elapsed,
		Message:      fmt.Sprintf("domain expires in %d days (%s)", days, expiry.Format("2006-01-02")),
		Body:         body,
	}

	if days <= 0 {
		result.Status = "down"
		result.Message = fmt.Sprintf("domain expired on %s", expiry.Format("2006-01-02"))
	} else if days <= warnDays {
		result.Status = "degraded"
		result.Message = fmt.Sprintf("domain expires in %d days (warning threshold: %d)", days, warnDays)
	}
	return result, nil
}

func extractTLD(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return domain
	}
	return "." + parts[len(parts)-1]
}

var _whoisServers = map[string]string{
	".com":   "whois.verisign-grs.com",
	".net":   "whois.verisign-grs.com",
	".org":   "whois.pir.org",
	".info":  "whois.afilias.net",
	".io":    "whois.nic.io",
	".dev":   "whois.nic.google",
	".app":   "whois.nic.google",
	".page":  "whois.nic.google",
	".me":    "whois.nic.me",
	".co":    "whois.nic.co",
	".us":    "whois.nic.us",
	".uk":    "whois.nic.uk",
	".de":    "whois.denic.de",
	".fr":    "whois.nic.fr",
	".nl":    "whois.sidn.nl",
	".eu":    "whois.eu",
	".ru":    "whois.tcinet.ru",
	".au":    "whois.auda.org.au",
	".ca":    "whois.cira.ca",
	".in":    "whois.registry.in",
	".br":    "whois.registro.br",
	".xyz":   "whois.nic.xyz",
	".biz":   "whois.nic.biz",
	".tech":  "whois.nic.tech",
	".cloud": "whois.nic.cloud",
	".site":  "whois.nic.site",
	".top":   "whois.nic.top",
	".name":  "whois.nic.name",
	".cc":    "ccwhois.verisign-grs.com",
	".tv":    "tvwhois.verisign-grs.com",
}

func whoisServerForTLD(tld string) string {
	if s, ok := _whoisServers[tld]; ok {
		return s
	}
	return ""
}

var _expiryPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)Registry Expiry Date:\s*(.+)`),
	regexp.MustCompile(`(?i)Registrar Registration Expiration Date:\s*(.+)`),
	regexp.MustCompile(`(?i)Expir(?:y|ation) Date:\s*(.+)`),
	regexp.MustCompile(`(?i)paid-till:\s*(.+)`),
	regexp.MustCompile(`(?i)expires:\s*(.+)`),
	regexp.MustCompile(`(?i)Expiration Time:\s*(.+)`),
	regexp.MustCompile(`(?i)expire:\s*(.+)`),
	regexp.MustCompile(`(?i)Valid Until:\s*(.+)`),
}

var _dateFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05-07:00",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"02-Jan-2006",
	"02/01/2006",
	"2006.01.02",
	"2006.01.02 15:04:05",
	"January 02 2006",
	"20060102",
}

func parseWhoisExpiry(response string) (time.Time, error) {
	for _, re := range _expiryPatterns {
		match := re.FindStringSubmatch(response)
		if len(match) < 2 {
			continue
		}
		dateStr := strings.TrimSpace(match[1])
		for _, format := range _dateFormats {
			t, err := time.Parse(format, dateStr)
			if err == nil {
				return t, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("no expiry date found in WHOIS response")
}
