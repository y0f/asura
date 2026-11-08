// Package logsafe removes credentials from request metadata before it reaches
// process logs or persistent request history. It has no storage dependencies.
package logsafe

import (
	"net/url"
	"path"
	"strings"
)

// Path accepts the decoded URL path. Normalize malformed separator/dot forms
// too: redirects, rejected requests and panic paths still reach log middleware.
func Path(value string) string {
	if value == "" {
		return ""
	}
	value, _, _ = strings.Cut(value, "?")
	value, _, _ = strings.Cut(value, "#")
	// Clean the prefix incrementally. Cleaning the entire path first could
	// remove the heartbeat marker through a trailing /token/../../ suffix.
	var segments []string
	for _, segment := range strings.Split(value, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, segment)
		}
		n := len(segments)
		if n >= 3 && strings.EqualFold(segments[n-3], "api") && strings.EqualFold(segments[n-2], "v1") && strings.EqualFold(segments[n-1], "heartbeat") {
			prefix := ""
			if n > 3 {
				prefix = "/" + strings.Join(segments[:n-3], "/")
			}
			return prefix + "/api/v1/heartbeat/[redacted]"
		}
	}
	return path.Clean(value)
}

// Referer retains only an HTTP(S) origin and credential-safe path. Query
// parameters can carry confirmation/unsubscribe tokens; userinfo and fragments
// are never useful for aggregate navigation statistics.
func Referer(value string) string {
	if value == "" {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment = nil, "", "", ""
	u.ForceQuery = false
	u.Path, u.RawPath = Path(u.Path), ""
	return u.String()
}
