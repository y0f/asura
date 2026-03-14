package anomaly

import "testing"

func TestIsAnomaly(t *testing.T) {
	tests := []struct {
		name        string
		rt          int64
		avg, stddev float64
		sensitivity string
		want        bool
	}{
		{"off ignores everything", 9999, 100, 10, SensitivityOff, false},
		{"zero baseline never anomaly", 500, 0, 0, SensitivityHigh, false},
		{"normal within 3σ", 130, 100, 10, SensitivityLow, false},
		{"anomaly above 3σ", 131, 100, 10, SensitivityLow, true},
		{"normal within 2σ", 120, 100, 10, SensitivityMedium, false},
		{"anomaly above 2σ", 121, 100, 10, SensitivityMedium, true},
		{"normal within 1.5σ", 115, 100, 10, SensitivityHigh, false},
		{"anomaly above 1.5σ", 116, 100, 10, SensitivityHigh, true},
		{"exactly on boundary not anomaly", 130, 100, 10, SensitivityLow, false},
		{"invalid sensitivity", 9999, 100, 10, "invalid", false},
		{"zero stddev no anomaly", 500, 100, 0, SensitivityHigh, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAnomaly(tt.rt, tt.avg, tt.stddev, tt.sensitivity)
			if got != tt.want {
				t.Fatalf("IsAnomaly(%d, %.0f, %.0f, %s) = %v, want %v", tt.rt, tt.avg, tt.stddev, tt.sensitivity, got, tt.want)
			}
		})
	}
}

func TestValidSensitivity(t *testing.T) {
	for _, s := range []string{"off", "low", "medium", "high"} {
		if !ValidSensitivity(s) {
			t.Fatalf("expected %q to be valid", s)
		}
	}
	for _, s := range []string{"", "invalid", "extreme"} {
		if ValidSensitivity(s) {
			t.Fatalf("expected %q to be invalid", s)
		}
	}
}
