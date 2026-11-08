package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/storage"
)

type badgeDataStore struct {
	storage.Store
	monitor             *storage.Monitor
	monitorErr, dataErr error
	days                []*storage.DailyUptime
	response            storage.ResponseTimeSummary
	check               *storage.CheckResult
}

func (s *badgeDataStore) GetPublicBadgeMonitor(context.Context, int64) (*storage.Monitor, error) {
	return s.monitor, s.monitorErr
}
func (s *badgeDataStore) GetPublicBadgeData(context.Context, int64, storage.PublicBadgeMetric, time.Time) (*storage.PublicBadgeData, error) {
	if s.monitorErr != nil {
		return nil, s.monitorErr
	}
	data := &storage.PublicBadgeData{Monitor: s.monitor, Response: s.response}
	data.UptimePercent, data.TotalChecks = storage.SummarizeUptime(s.days)
	if s.check != nil {
		data.CertExpiry = s.check.CertExpiry
	}
	return data, s.dataErr
}

func TestBadgesDistinguishMissingMeasuredAndFailedData(t *testing.T) {
	past, soon := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	for _, tc := range []struct {
		name, kind, value string
		store             badgeDataStore
	}{
		{name: "uptime no checks", kind: "uptime", value: "no checks"},
		{name: "uptime all failed is measured zero", kind: "uptime", value: "0.00%", store: badgeDataStore{days: []*storage.DailyUptime{{TotalChecks: 4}}}},
		{name: "uptime weighted days", kind: "uptime", value: "90.00%", store: badgeDataStore{days: []*storage.DailyUptime{{TotalChecks: 9, UpChecks: 9}, {TotalChecks: 1}}}},
		{name: "response no successful checks", kind: "response", value: "no samples"},
		{name: "response measured zero", kind: "response", value: "0ms", store: badgeDataStore{response: storage.ResponseTimeSummary{Samples: 1}}},
		{name: "response measured median", kind: "response", value: "125ms", store: badgeDataStore{response: storage.ResponseTimeSummary{Samples: 3, P50: 125}}},
		{name: "manual has no response duration", kind: "response", value: "n/a", store: badgeDataStore{monitor: &storage.Monitor{ID: 1, Type: "manual", Enabled: true}}},
		{name: "heartbeat has no response duration", kind: "response", value: "n/a", store: badgeDataStore{monitor: &storage.Monitor{ID: 1, Type: "heartbeat", Enabled: true}}},
		{name: "cert absent", kind: "cert", value: "n/a"},
		{name: "cert check without expiry", kind: "cert", value: "n/a", store: badgeDataStore{check: &storage.CheckResult{}}},
		{name: "cert recently expired", kind: "cert", value: "expired", store: badgeDataStore{check: &storage.CheckResult{CertExpiry: &past}}},
		{name: "cert less than one day", kind: "cert", value: "&lt;1d", store: badgeDataStore{check: &storage.CheckResult{CertExpiry: &soon}}},
		{name: "paused status", kind: "status", value: "paused", store: badgeDataStore{monitor: &storage.Monitor{ID: 1, Status: "up"}}},
		{name: "pending status", kind: "status", value: "pending", store: badgeDataStore{monitor: &storage.Monitor{ID: 1, Enabled: true, Status: "pending"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.store.monitor == nil {
				tc.store.monitor = &storage.Monitor{ID: 1, Name: "Public service", Type: "http", Enabled: true, Status: "up"}
			}
			h := &Handler{store: &tc.store}
			r := httptest.NewRequest(http.MethodGet, "/badge?label=Service", nil)
			r.SetPathValue("id", "1")
			w := httptest.NewRecorder()
			badgeHandler(h, tc.kind)(w, r)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), ">"+tc.value+"</text>") || !strings.Contains(w.Body.String(), ">Service</text>") {
				t.Fatalf("unexpected badge: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func badgeHandler(h *Handler, kind string) http.HandlerFunc {
	switch kind {
	case "uptime":
		return h.BadgeUptime
	case "response":
		return h.BadgeResponseTime
	case "cert":
		return h.BadgeCert
	default:
		return h.BadgeStatus
	}
}

func TestBadgeErrorsAreNotCachedOrDisguisedAsData(t *testing.T) {
	for _, kind := range []string{"status", "uptime", "response", "cert"} {
		for _, tc := range []struct {
			name, id            string
			monitorErr, dataErr error
			status              int
		}{
			{name: "invalid identifier", id: "invalid", status: http.StatusBadRequest},
			{name: "unpublished or missing", id: "1", monitorErr: sql.ErrNoRows, status: http.StatusNotFound},
			{name: "publication query failed", id: "1", monitorErr: errors.New("private database detail"), status: http.StatusInternalServerError},
			{name: "metric query failed", id: "1", dataErr: errors.New("private database detail"), status: http.StatusInternalServerError},
		} {
			if kind == "status" && tc.dataErr != nil {
				continue
			}
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				s := &badgeDataStore{monitor: &storage.Monitor{ID: 1, Type: "http", Enabled: true}, monitorErr: tc.monitorErr, dataErr: tc.dataErr}
				h := &Handler{store: s}
				r := httptest.NewRequest(http.MethodGet, "/badge", nil)
				r.SetPathValue("id", tc.id)
				w := httptest.NewRecorder()
				badgeHandler(h, kind)(w, r)
				if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "image/svg+xml" {
					t.Fatalf("incorrect error response: %d %v", w.Code, w.Header())
				}
				if strings.Contains(w.Body.String(), "private database") || strings.Contains(w.Body.String(), ">n/a</text>") || strings.Contains(w.Body.String(), ">100.00%</text>") {
					t.Fatalf("error leaked details or fabricated data: %s", w.Body.String())
				}
			})
		}
	}
}
