package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/internal/storage"
)

type publicReadStore struct {
	storage.Store
	dailyCalls               int
	failDaily, failIncidents bool
}

func (s *publicReadStore) GetDailyUptime(ctx context.Context, id int64, from, to time.Time, access ...storage.MonitorAccess) ([]*storage.DailyUptime, error) {
	s.dailyCalls++
	if s.failDaily {
		return nil, errors.New("injected uptime failure")
	}
	return s.Store.GetDailyUptime(ctx, id, from, to, access...)
}
func (s *publicReadStore) ListRecentIncidents(ctx context.Context, ids []int64, from time.Time, limit int) ([]*storage.Incident, error) {
	if s.failIncidents {
		return nil, errors.New("injected incident failure")
	}
	return s.Store.ListRecentIncidents(ctx, ids, from, limit)
}

func TestPublicStatusMissingDataAndReadErrors(t *testing.T) {
	h, store := storeHandler(t)
	ctx := context.Background()
	m := &storage.Monitor{Name: "waiting service", Type: "http", Target: "https://private.example.test", Enabled: true, Interval: 60, Timeout: 10}
	if err := store.CreateMonitor(ctx, m); err != nil {
		t.Fatal(err)
	}
	sp := &storage.StatusPage{Title: "Services", Slug: "services", Enabled: true, ShowIncidents: true, LogoURL: "https://images.example/logo", CustomHeaderHTML: `<script>window.bad=true</script>`}
	if err := store.CreateStatusPage(ctx, sp); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatusPageMonitors(ctx, sp.ID, []storage.StatusPageMonitor{{MonitorID: m.ID}}); err != nil {
		t.Fatal(err)
	}
	wrapped := &publicReadStore{Store: store}
	h.store = wrapped
	w := httptest.NewRecorder()
	h.StatusPageByID(w, httptest.NewRequest("GET", "/services", nil), sp.ID)
	body := w.Body.String()
	if w.Code != http.StatusOK || wrapped.dailyCalls != 1 || !strings.Contains(body, "Awaiting check data") || strings.Contains(body, "100.00%") || strings.Contains(body, "window.bad") {
		t.Fatalf("missing data/branding response: %d calls=%d body=%s", w.Code, wrapped.dailyCalls, body)
	}
	if w.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "img-src 'self' data: https://images.example;") {
		t.Fatalf("public header policy: %v", w.Header())
	}
	for _, failure := range []string{"daily", "incidents"} {
		wrapped.failDaily, wrapped.failIncidents = failure == "daily", failure == "incidents"
		w := httptest.NewRecorder()
		h.StatusPageByID(w, httptest.NewRequest("GET", "/services", nil), sp.ID)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("%s failure became successful page: %d", failure, w.Code)
		}
	}
	wrapped.failDaily = false
	w = httptest.NewRecorder()
	h.StatusPageRSS(w, httptest.NewRequest("GET", "/services/rss", nil), sp.ID)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("incident read failure became empty RSS: %d", w.Code)
	}
}
