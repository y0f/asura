package httputil

import (
	"testing"

	"github.com/y0f/asura/internal/storage"
)

func TestOverallStatusNeverTreatsMissingChecksAsHealthy(t *testing.T) {
	for _, test := range []struct {
		states []string
		want   string
	}{
		{nil, "unknown"}, {[]string{"pending"}, "unknown"}, {[]string{"up", "pending"}, "unknown"},
		{[]string{"up", "up"}, "operational"}, {[]string{"pending", "degraded"}, "degraded"},
		{[]string{"degraded", "pending", "down"}, "major_outage"}, {[]string{"down", "pending", "degraded"}, "major_outage"},
	} {
		var monitors []*storage.Monitor
		for _, status := range test.states {
			monitors = append(monitors, &storage.Monitor{Status: status})
		}
		if got := OverallStatus(monitors); got != test.want {
			t.Errorf("%v => %q want %q", test.states, got, test.want)
		}
	}
}
