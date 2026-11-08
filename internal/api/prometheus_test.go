package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/storage"
)

type prometheusTestStore struct {
	storage.Store
	visit func(context.Context, func(storage.PrometheusRow) error) (*storage.PrometheusSummary, error)
}

func (s prometheusTestStore) VisitPrometheus(ctx context.Context, from, to time.Time, visit func(storage.PrometheusRow) error) (*storage.PrometheusSummary, error) {
	if to.Sub(from) != 24*time.Hour {
		panic("incorrect scrape window")
	}
	return s.visit(ctx, visit)
}

func prometheusHandler(s storage.Store) *Handler {
	return &Handler{store: s, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestPrometheusCompleteDeterministicExposition(t *testing.T) {
	names := []string{"a b", "a_b", "a-b", "a_20b", "AB", "ab", "é", "\"", "\\", "\n"}
	store := prometheusTestStore{visit: func(ctx context.Context, visit func(storage.PrometheusRow) error) (*storage.PrometheusSummary, error) {
		for id := int64(1); id <= 1027; id++ {
			row := storage.PrometheusRow{MonitorID: id, Name: "Name\r\n\"\\", Type: "http", Status: "pending"}
			if id == 1 {
				row.Status = "up"
				zero := int64(0)
				row.ResponseTime = &zero
				for i, name := range names {
					row.Tag = &storage.MonitorTag{TagID: int64(i + 1), Name: name, Value: "v\r\n\"\\"}
					if err := visit(row); err != nil {
						return nil, err
					}
				}
			} else if err := visit(row); err != nil {
				return nil, err
			}
		}
		return &storage.PrometheusSummary{Requests: 23, UniqueVisitors: 5, AvgLatencyMs: 2, OpenIncidents: 7}, nil
	}}
	h := prometheusHandler(store)
	var previous string
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		h.Metrics(w, httptest.NewRequest("GET", "/metrics", nil))
		body := w.Body.String()
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain;") {
			t.Fatalf("scrape failed: %d %s", w.Code, body)
		}
		if i > 0 && body != previous {
			t.Fatal("identical data produced unstable ordering")
		}
		previous = body
		for _, want := range []string{
			`asura_monitors_total{status="pending"} 1026`,
			`asura_monitors_total{status="up"} 1`,
			`asura_monitors_by_type{type="http"} 1027`,
			`asura_http_requests_24h 23`, `asura_http_unique_visitors_24h 5`, `asura_http_avg_latency_ms 2`,
			`asura_incidents_total{status="open"} 7`,
		} {
			if !strings.Contains(body, want+"\n") {
				t.Errorf("missing %q", want)
			}
		}
		if strings.Count(body, "\nasura_monitor_up{") != 1027 || strings.Count(body, "\nasura_monitor_response_time_ms{") != 1 {
			t.Fatal("monitor series truncated or missing measurement invented")
		}
		if !strings.Contains(body, "name=\"Name\r\\n\\\"\\\\\"") {
			t.Fatal("label value was changed or incorrectly escaped")
		}
		up := strings.Split(body, "\nasura_monitor_up{")[1]
		up = strings.SplitN(up, "\n", 2)[0]
		labels := regexp.MustCompile(`,tag_([a-zA-Z0-9_]+)="`).FindAllStringSubmatch(up, -1)
		seen := make(map[string]bool)
		for _, label := range labels {
			if seen[label[1]] {
				t.Fatalf("duplicate label %s", label[1])
			}
			seen[label[1]] = true
		}
		for _, expected := range []string{"a_20b", "a_5Fb", "a_2Db", "a_5F20b", "AB", "ab", "_C3_A9", "_22", "_5C", "_0A"} {
			if !seen[expected] {
				t.Errorf("missing encoded tag %s", expected)
			}
		}
		if len(labels) != len(names) {
			t.Fatalf("invalid or missing tag labels: %d", len(labels))
		}
		// Text exposition requires each family to form a single contiguous group.
		closed := make(map[string]bool)
		last := ""
		for _, line := range strings.Split(body, "\n") {
			if line == "" || line[0] == '#' {
				continue
			}
			name := strings.FieldsFunc(line, func(r rune) bool { return r == '{' || r == ' ' })[0]
			if name != last {
				if closed[name] {
					t.Fatalf("noncontiguous metric family: %s", name)
				}
				closed[last] = true
				last = name
			}
		}
	}
}

func TestPrometheusFailureNeverPublishesPartialSuccess(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		maxBytes int
		timeout  time.Duration
		visit    func(context.Context, func(storage.PrometheusRow) error) (*storage.PrometheusSummary, error)
	}{
		{"missing summary", 500, prometheusMaxBytes, time.Second, func(context.Context, func(storage.PrometheusRow) error) (*storage.PrometheusSummary, error) {
			return nil, nil
		}},
		{"late database failure", 500, prometheusMaxBytes, time.Second, func(ctx context.Context, visit func(storage.PrometheusRow) error) (*storage.PrometheusSummary, error) {
			if err := visit(storage.PrometheusRow{MonitorID: 1, Name: "partial", Type: "http"}); err != nil {
				return nil, err
			}
			return nil, errors.New("injected late query failure")
		}},
		{"budget during rows", 503, 2048, time.Second, func(ctx context.Context, visit func(storage.PrometheusRow) error) (*storage.PrometheusSummary, error) {
			for id := int64(1); id <= 1000; id++ {
				if err := visit(storage.PrometheusRow{MonitorID: id, Name: "fixture", Type: "http"}); err != nil {
					return nil, err
				}
			}
			t.Fatal("visitor did not stop at output budget")
			return nil, nil
		}},
		{"budget in summary", 503, 700, time.Second, func(context.Context, func(storage.PrometheusRow) error) (*storage.PrometheusSummary, error) {
			return &storage.PrometheusSummary{}, nil
		}},
		{"deadline", 503, prometheusMaxBytes, time.Millisecond, func(ctx context.Context, visit func(storage.PrometheusRow) error) (*storage.PrometheusSummary, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}},
		{"invalid UTF-8", 500, prometheusMaxBytes, time.Second, func(ctx context.Context, visit func(storage.PrometheusRow) error) (*storage.PrometheusSummary, error) {
			return nil, visit(storage.PrometheusRow{MonitorID: 1, Name: string([]byte{255}), Type: "http"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := prometheusHandler(prometheusTestStore{visit: tc.visit})
			w := httptest.NewRecorder()
			h.metrics(w, httptest.NewRequest("GET", "/metrics", nil), tc.maxBytes, tc.timeout)
			if w.Code != tc.status || strings.Contains(w.Body.String(), "asura_") || strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
				t.Fatalf("partial success: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPrometheusLabelEncodingCannotEscapeTheBudget(t *testing.T) {
	for _, tag := range []storage.MonitorTag{
		{Name: strings.Repeat("é", 1000)},
		{Name: "value", Value: strings.Repeat("\n", 1000)},
	} {
		render := newPrometheusRenderer(2048)
		err := render.monitor(storage.PrometheusRow{MonitorID: 1, Name: "fixture", Type: "http", Tag: &tag})
		if !errors.Is(err, errPrometheusBudget) {
			t.Fatalf("expanding label escaped budget: %v", err)
		}
		size := 0
		for _, b := range render.families {
			size += b.text.Len()
		}
		if size > 2048 {
			t.Fatalf("wrote %d bytes past budget", size)
		}
	}
	// Every byte value, including literal underscores/escape-shaped input, has
	// a unique valid output. Exercise the encoder, not a second implementation.
	seen := make(map[string]bool)
	for i := 0; i < 256; i++ {
		budget := prometheusBudget{remaining: 100}
		b := prometheusBuffer{budget: &budget}
		b.tag(&storage.MonitorTag{Name: string([]byte{byte(i)})})
		got := b.text.String()
		if budget.err != nil || seen[got] || !regexp.MustCompile(`^,tag_[A-Za-z0-9_]+="true"$`).MatchString(got) {
			t.Fatal(fmt.Sprintf("invalid/colliding byte encoding %d: %q", i, got))
		}
		seen[got] = true
	}
}
