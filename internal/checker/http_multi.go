package checker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/y0f/asura/internal/model"
	"github.com/y0f/asura/internal/safenet"
)

type HTTPMultiChecker struct {
	AllowPrivate bool
}

const (
	maxMultiSettings = 1 << 20
	maxMultiURL      = 8 << 10
	maxMultiHeaders  = 64 << 10 // Combined names and values per request.
	maxMultiBody     = 1 << 20
)

var errExpansionLimit = errors.New("expanded value exceeds limit")

func (c *HTTPMultiChecker) Type() string { return "http_multi" }

func (c *HTTPMultiChecker) Check(ctx context.Context, monitor *model.Monitor) (*Result, error) {
	if len(monitor.Settings) > maxMultiSettings {
		return &Result{Status: "down", Message: "multi-step settings exceed 1 MiB"}, nil
	}
	var settings model.MultiStepHTTPSettings
	if err := json.Unmarshal(monitor.Settings, &settings); err != nil {
		return &Result{Status: "down", Message: fmt.Sprintf("invalid settings: %v", err)}, nil
	}
	if len(settings.Steps) == 0 {
		return &Result{Status: "down", Message: "no steps defined"}, nil
	}
	if len(settings.Steps) > 5 {
		return &Result{Status: "down", Message: "max 5 steps allowed"}, nil
	}

	timeout := time.Duration(monitor.Timeout) * time.Second
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: timeout,
				Control: safenet.MaybeDialControl(c.AllowPrivate),
			}).DialContext,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	// The transport is per check: keep connections alive across steps, then
	// release them so idle conns and their goroutines do not accumulate.
	defer client.CloseIdleConnections()

	vars := make(map[string]string)
	var totalRT int64

	for i, step := range settings.Steps {
		stepName := step.Name
		if stepName == "" {
			stepName = fmt.Sprintf("Step %d", i+1)
		}

		failExpansion := func(field string) (*Result, error) {
			return &Result{Status: "down", ResponseTime: totalRT,
				Message: fmt.Sprintf("step %d: expanded %s exceed request size limits", i+1, field)}, nil
		}
		url, err := interpolateVars(step.URL, vars, maxMultiURL)
		if err != nil {
			return failExpansion("URL bytes")
		}
		body, err := interpolateVars(step.Body, vars, maxMultiBody)
		if err != nil {
			return failExpansion("body bytes")
		}
		headers := make(http.Header, len(step.Headers))
		remainingHeaders := maxMultiHeaders
		for k, v := range step.Headers {
			if len(k) > remainingHeaders {
				return failExpansion("header bytes")
			}
			remainingHeaders -= len(k)
			value, err := interpolateVars(v, vars, remainingHeaders)
			if err != nil {
				return failExpansion("header bytes")
			}
			remainingHeaders -= len(value)
			headers.Set(k, value)
		}
		method := step.Method
		if method == "" {
			method = "GET"
		}

		req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
		if err != nil {
			return &Result{
				Status:       "down",
				ResponseTime: totalRT,
				Message:      fmt.Sprintf("%s: invalid request: %v", stepName, err),
			}, nil
		}

		req.Header = headers

		start := time.Now()
		resp, err := client.Do(req)
		stepRT := time.Since(start).Milliseconds()
		totalRT += stepRT

		if err != nil {
			return &Result{
				Status:       "down",
				ResponseTime: totalRT,
				Message:      fmt.Sprintf("%s: request failed: %v", stepName, err),
			}, nil
		}

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyRead))
		resp.Body.Close()
		if err != nil {
			return &Result{
				Status:       "down",
				ResponseTime: totalRT,
				StatusCode:   resp.StatusCode,
				Message:      fmt.Sprintf("%s: response body read failed: %v", stepName, err),
			}, nil
		}

		if step.ExpectedStatus > 0 && resp.StatusCode != step.ExpectedStatus {
			return &Result{
				Status:       "down",
				ResponseTime: totalRT,
				StatusCode:   resp.StatusCode,
				Message:      fmt.Sprintf("%s: expected status %d, got %d", stepName, step.ExpectedStatus, resp.StatusCode),
			}, nil
		}

		if step.ExtractVar != "" {
			val := extractVariable(string(respBody), step.ExtractRegex, step.ExtractJSON)
			if val != "" {
				vars[step.ExtractVar] = val
			}
		}
	}

	return &Result{
		Status:       "up",
		ResponseTime: totalRT,
		StatusCode:   200,
		Message:      fmt.Sprintf("all %d steps passed", len(settings.Steps)),
	}, nil
}

// interpolateVars expands only placeholders in the original text. Extracted
// response bytes are data, never a second template. Check the budget before
// copying each segment so a small template cannot allocate an enormous result.
func interpolateVars(s string, vars map[string]string, limit int) (string, error) {
	if len(vars) == 0 || !strings.Contains(s, "{{") {
		if len(s) > limit {
			return "", errExpansionLimit
		}
		return s, nil
	}
	var out strings.Builder
	appendBounded := func(value string) bool {
		if len(value) > limit-out.Len() {
			return false
		}
		out.WriteString(value)
		return true
	}
	for len(s) > 0 {
		start := strings.Index(s, "{{")
		if start < 0 {
			if !appendBounded(s) {
				return "", errExpansionLimit
			}
			break
		}
		end := strings.Index(s[start+2:], "}}")
		if end < 0 {
			if !appendBounded(s) {
				return "", errExpansionLimit
			}
			break
		}
		end += start + 2
		if !appendBounded(s[:start]) {
			return "", errExpansionLimit
		}
		value, found := vars[s[start+2:end]]
		if !found {
			value = s[start : end+2]
		}
		if !appendBounded(value) {
			return "", errExpansionLimit
		}
		s = s[end+2:]
	}
	return out.String(), nil
}

func extractVariable(body, regexPattern, jsonPath string) string {
	if regexPattern != "" {
		re, err := regexp.Compile(regexPattern)
		if err != nil {
			return ""
		}
		matches := re.FindStringSubmatch(body)
		if len(matches) > 1 {
			return matches[1]
		}
		if len(matches) > 0 {
			return matches[0]
		}
		return ""
	}

	if jsonPath != "" {
		return extractJSONPath(body, jsonPath)
	}

	return ""
}

func extractJSONPath(body, path string) string {
	parts := strings.Split(path, ".")
	var current any
	if err := json.Unmarshal([]byte(body), &current); err != nil {
		return ""
	}

	for _, part := range parts {
		m, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current, ok = m[part]
		if !ok {
			return ""
		}
	}

	switch v := current.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%g", v)
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
