package validate

import (
	"testing"

	"github.com/y0f/asura/v2/internal/model"
)

func TestPassiveSLATargetChangesPreserveOnlyExistingInactiveValues(t *testing.T) {
	for _, kind := range []string{"manual", "heartbeat"} {
		next := &model.Monitor{Type: kind, SLATarget: 99.9}
		if err := ValidateMonitorSLAChange(next, nil); err == nil {
			t.Fatal("new passive promise accepted")
		}
		if err := ValidateMonitorSLAChange(next, &model.Monitor{Type: "http", SLATarget: next.SLATarget}); err == nil {
			t.Fatal("active target became a passive promise")
		}
		if err := ValidateMonitorSLAChange(next, &model.Monitor{Type: kind, SLATarget: next.SLATarget}); err != nil {
			t.Fatalf("legacy inactive metadata edit blocked: %v", err)
		}
		if err := ValidateMonitorSLAChange(next, &model.Monitor{Type: kind, SLATarget: 99}); err == nil {
			t.Fatal("inactive target change accepted")
		}
		next.SLATarget = 0
		if err := ValidateMonitorSLAChange(next, nil); err != nil {
			t.Fatalf("clearing passive target blocked: %v", err)
		}
	}
}
