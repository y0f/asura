package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/y0f/asura/internal/safenet"
	"github.com/y0f/asura/internal/storage"
)

type HTTPMultiChecker struct {
	AllowPrivate bool
}

func (c *HTTPMultiChecker) Type() string { return "http_multi" }

func (c *HTTPMultiChecker) Check(ctx context.Context, monitor *storage.Monitor) (*Result, error) {
	var settings storage.MultiStepHTTPSettings
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

	vars := make(map[string]string)
	var totalRT int64

	for i, step := range settings.Steps {
		stepName := step.Name
		if stepName == "" {
			stepName = fmt.Sprintf("Step %d", i+1)
		}

		url := interpolateVars(step.URL, vars)
		body := interpolateVars(step.Body, vars)
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

		for k, v := range step.Headers {
			req.Header.Set(k, interpolateVars(v, vars))
		}

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

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()

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

func interpolateVars(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
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
