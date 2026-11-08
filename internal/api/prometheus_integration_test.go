package api_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestPrometheusHTTPIncludesMonitorsBeyondFirstPage(t *testing.T) {
	env := newTestEnv(t, headless, false, nil)
	const count = 1001
	for i := 0; i < count; i++ {
		m := &storage.Monitor{Name: fmt.Sprintf("fixture-%04d", i), Type: "http", Target: "https://example.invalid", Enabled: true, Interval: 60, Timeout: 10}
		if err := env.store.CreateMonitor(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	w := env.do(http.MethodGet, "/metrics", "", admin)
	if w.Code != http.StatusOK {
		t.Fatalf("scrape: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if got := strings.Count(body, "\nasura_monitor_up{"); got != count {
		t.Errorf("scrape silently truncated: got %d, want %d monitors", got, count)
	}
	if !strings.Contains(body, `asura_monitors_by_type{type="http"} 1001`+"\n") {
		t.Error("type count does not cover the entire inventory")
	}
	if !strings.Contains(body, `asura_monitors_total{status="pending"} 1001`+"\n") {
		t.Error("unobserved monitors were reported as healthy")
	}
	if strings.Contains(body, "\nasura_monitor_response_time_ms{") {
		t.Error("unobserved monitors were assigned a measured duration")
	}
}
