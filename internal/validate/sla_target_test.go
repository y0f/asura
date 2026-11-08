package validate

import (
	"math"
	"strings"
	"testing"
)

func TestMonitorSLATargetIsFiniteAndBoundedForEveryKind(t *testing.T) {
	for _, kind := range []string{"http", "manual", "heartbeat"} {
		for _, target := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.01, 100.01} {
			m := validMonitor()
			m.Type, m.SLATarget = kind, target
			if err := ValidateMonitor(m); err == nil || !strings.Contains(err.Error(), "sla_target") {
				t.Errorf("kind=%s target=%v returned %v", kind, target, err)
			}
		}
		for _, target := range []float64{0, 99.9, 100} {
			m := validMonitor()
			m.Type, m.SLATarget = kind, target
			if err := ValidateMonitor(m); err != nil {
				t.Errorf("valid kind=%s target=%v: %v", kind, target, err)
			}
		}
	}
}
