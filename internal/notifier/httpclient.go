package notifier

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/y0f/asura/internal/safenet"
)

// Two shared clients (private targets blocked / allowed) so senders reuse
// keep-alive connections instead of building a new transport, with its own
// idle pool, on every request.
var (
	_safeHTTPClient    = sync.OnceValue(func() *http.Client { return buildHTTPClient(false) })
	_privateHTTPClient = sync.OnceValue(func() *http.Client { return buildHTTPClient(true) })
)

// newHTTPClient returns a shared HTTP client whose dialer blocks connections
// to private/reserved IPs unless allowPrivate is true. All outbound sender
// requests must use this so user-controlled URLs cannot be used for SSRF.
// Callers must not modify the returned client.
func newHTTPClient(allowPrivate bool) *http.Client {
	if allowPrivate {
		return _privateHTTPClient()
	}
	return _safeHTTPClient()
}

func buildHTTPClient(allowPrivate bool) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
				Control: safenet.MaybeDialControl(allowPrivate),
			}).DialContext,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
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
