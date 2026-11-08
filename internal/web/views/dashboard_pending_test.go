package views

import (
	"fmt"
	"strings"
	"testing"
)

func TestDashboardPendingIsNotHealthy(t *testing.T) {
	for _, tc := range []struct {
		name               string
		total, up, pending int
	}{
		{"empty", 0, 0, 0},
		{"all pending", 3, 0, 3},
		{"mixed", 3, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := DashboardParams{Total: tc.total, Up: tc.up, Pending: tc.pending}
			text := textOf(render(t, DashboardPage(p)))
			if !strings.Contains(text, fmt.Sprintf("%d awaiting data", tc.pending)) {
				t.Fatal("pending count is missing")
			}
			if strings.Contains(text, "everything is up") {
				t.Fatal("empty or unobserved state presented as healthy")
			}
		})
	}
	if got := (DashboardParams{Total: 2, Up: 2}).summary(); !strings.Contains(got, "everything is up") {
		t.Fatalf("observed healthy summary lost: %s", got)
	}
}
