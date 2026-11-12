package analytics

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/y0f/asura/v3/internal/storage"
)

func testStore(t *testing.T) *storage.SQLiteStore {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "asura-analytics-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	store, err := storage.NewSQLiteStore(tmpFile.Name(), 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func setupMetricsTest(t *testing.T) *MonitorMetrics {
	t.Helper()
	store := testStore(t)
	ctx := context.Background()

	mon := &storage.Monitor{
		Name: "Analytics Test", Type: "http", Target: "https://example.com",
		Interval: 60, Timeout: 10, Enabled: true,
		FailureThreshold: 3, SuccessThreshold: 1,
	}
	if err := store.CreateMonitor(ctx, mon); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		status       string
		responseTime int64
	}{
		{"up", 50}, {"up", 100}, {"up", 150}, {"up", 200}, {"up", 250},
		{"up", 300}, {"up", 350}, {"down", 0}, {"degraded", 400}, {"up", 100},
	} {
		if err := store.InsertCheckResult(ctx, &storage.CheckResult{
			MonitorID: mon.ID, Status: c.status, ResponseTime: c.responseTime,
		}); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	metrics, err := ComputeMetrics(ctx, store, mon.ID, now.Add(-1*time.Hour), now.Add(1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return metrics
}

func TestComputeMetricsCounts(t *testing.T) {
	m := setupMetricsTest(t)
	if m.TotalChecks != 10 {
		t.Fatalf("expected 10 total checks, got %d", m.TotalChecks)
	}
	if m.UpChecks != 8 {
		t.Fatalf("expected 8 up checks, got %d", m.UpChecks)
	}
	if m.DownChecks != 1 {
		t.Fatalf("expected 1 down check, got %d", m.DownChecks)
	}
	if m.DegradedChecks != 1 {
		t.Fatalf("expected 1 degraded check, got %d", m.DegradedChecks)
	}
}

func TestComputeMetricsUptime(t *testing.T) {
	m := setupMetricsTest(t)
	if m.UptimePct < 79 || m.UptimePct > 81 {
		t.Fatalf("expected uptime ~80%%, got %.2f%%", m.UptimePct)
	}
}

func TestComputeMetricsPercentiles(t *testing.T) {
	m := setupMetricsTest(t)
	if m.P50 <= 0 || m.P95 <= 0 || m.P99 <= 0 {
		t.Fatalf("expected positive percentiles, got P50=%v P95=%v P99=%v", m.P50, m.P95, m.P99)
	}
	if m.P50 > m.P95 {
		t.Fatalf("expected P50 <= P95, got P50=%v P95=%v", m.P50, m.P95)
	}
	if m.P95 > m.P99 {
		t.Fatalf("expected P95 <= P99, got P95=%v P99=%v", m.P95, m.P99)
	}
}
