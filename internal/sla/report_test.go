package sla

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

type reportStore struct {
	storage.Store
	target          float64
	total, up, down int64
	fail            error
	pages, calls    int
	from, to        time.Time
}

func (s *reportStore) ListMonitors(_ context.Context, _ storage.MonitorListFilter, p storage.Pagination) (*storage.PaginatedResult, error) {
	s.calls++
	return &storage.PaginatedResult{Data: []*storage.Monitor{{ID: int64(p.Page), Name: "report fixture", SLATarget: s.target}}, TotalPages: max(s.pages, 1)}, nil
}

func (s *reportStore) GetMonitorSLASnapshot(_ context.Context, _ int64, from, to, _ time.Time, _ ...storage.MonitorAccess) (*storage.MonitorSLASnapshot, error) {
	s.from, s.to = from, to
	days := []*storage.DailyUptime{{Date: from.Format("2006-01-02"), TotalChecks: s.total, UpChecks: s.up, DownChecks: s.down}}
	return &storage.MonitorSLASnapshot{Sampled: true, Target: s.target, Period: days, Rolling: days}, s.fail
}

func TestReportAgreesWithLiveMonthlyBudget(t *testing.T) {
	for _, tc := range []struct {
		name     string
		now      time.Time
		target   float64
		up       int64
		breached bool
	}{
		{"early outage still within monthly budget", time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC), 99.9, 70000, false},
		{"late outage exhausts monthly budget", time.Date(2026, 11, 30, 23, 0, 0, 0, time.UTC), 99.9, 99700, true},
		{"no downtime allowance", time.Date(2026, 11, 1, 0, 10, 0, 0, time.UTC), 100, 99999, true},
		{"leap February", time.Date(2028, 2, 10, 12, 0, 0, 0, time.UTC), 99.9, 99999, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &reportStore{target: tc.target, total: 100000, up: tc.up, down: 100000 - tc.up}
			live, err := computeAt(context.Background(), s, 1, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := computeReportAt(context.Background(), s, tc.now.Year(), tc.now.Month(), tc.now)
			if err != nil || len(entries) != 1 {
				t.Fatalf("report entries=%d, error=%v", len(entries), err)
			}
			report := entries[0]
			if report.BudgetSecs != live.BudgetTotalSecs || report.UsedSecs != live.BudgetUsedSecs || report.RemainSecs != live.BudgetRemainSecs || report.Breached != live.Breached || report.UptimePct != live.UptimePctMonth {
				t.Errorf("report disagrees with live status: report=%+v, live=%+v", report, live)
			}
			if report.Breached != tc.breached {
				t.Errorf("breached=%t, want %t", report.Breached, tc.breached)
			}
			if report.TotalChecks != s.total || report.UpChecks != s.up || report.DownChecks != s.down || !s.to.Equal(tc.now) {
				t.Errorf("report omitted summarized samples or read past now: %+v, end=%v", report, s.to)
			}
		})
	}
}

func TestReportPastMonthPaginationAndFailure(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s := &reportStore{target: 99.9, total: 1000, up: 999, down: 1, pages: 2}
	entries, err := computeReportAt(context.Background(), s, 2026, time.October, now)
	if err != nil || len(entries) != 2 || s.calls != 2 {
		t.Fatalf("pagination: entries=%d, calls=%d, error=%v", len(entries), s.calls, err)
	}
	if want := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC); !s.to.Equal(want) {
		t.Errorf("past period ends at %v, want %v", s.to, want)
	}
	s.fail = errors.New("uptime unavailable")
	if entries, err := computeReportAt(context.Background(), s, 2026, time.October, now); !errors.Is(err, s.fail) || entries != nil {
		t.Fatalf("incomplete report presented as success: entries=%v, error=%v", entries, err)
	}
	if _, err := computeReportAt(context.Background(), s, 2026, time.December, now); !errors.Is(err, ErrFuturePeriod) {
		t.Fatalf("future period accepted: %v", err)
	}
}

func TestReportPeriodSelector(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		month time.Month
		valid bool
	}{
		{"", time.November, true}, {"2026-11", time.November, true},
		{"2026-10", time.October, true}, {"2026-12", 0, false},
		{"bad", 0, false}, {"2026-13", 0, false}, {"0000-01", 0, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			year, month, err := ParsePeriod(tc.value, now)
			if (err == nil) != tc.valid || (tc.valid && (year != 2026 || month != tc.month)) {
				t.Fatalf("period %q = %d/%d, %v", tc.value, year, month, err)
			}
		})
	}
}
