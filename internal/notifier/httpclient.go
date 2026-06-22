package notifier

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/y0f/asura/internal/safenet"
)

// newHTTPClient builds an HTTP client whose dialer blocks connections to
// private/reserved IPs unless allowPrivate is true. All outbound sender
// requests must use this so user-controlled URLs cannot be used for SSRF.
func newHTTPClient(allowPrivate bool) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
				Control: safenet.MaybeDialControl(allowPrivate),
			}).DialContext,
		},
	}
}

// redactErr strips credentials and query strings from any URLs embedded in an
// error message before it is logged or persisted. Sender errors frequently wrap
// *url.Error, whose string form includes the full request URL (gotify token,
// webhook secrets in the path, etc.).
func redactErr(err error) string {
	if err == nil {
		return ""
	}
	return redactURLs(err.Error())
}

func redactURLs(s string) string {
	const punct = ".,;:)(\"'"
	fields := strings.Fields(s)
	for i, f := range fields {
		// A *url.Error stringifies the URL wrapped in double quotes, e.g.
		// `Get "https://host/p?token=x": ...`, so strip leading punctuation
		// (the quote) as well as trailing, then re-attach both sides.
		start, end := 0, len(f)
		for start < end && strings.IndexByte(punct, f[start]) >= 0 {
			start++
		}
		for end > start && strings.IndexByte(punct, f[end-1]) >= 0 {
			end--
		}
		if r, ok := redactURL(f[start:end]); ok {
			fields[i] = f[:start] + r + f[end:]
		}
	}
	return strings.Join(fields, " ")
}

func redactURL(s string) (string, bool) {
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	if u.Path != "" && u.Path != "/" {
		u.Path = "/…"
	}
	return u.String(), true
}
