package sla

import "testing"

func TestErrorBudgetSecs(t *testing.T) {
	tests := []struct {
		name      string
		target    float64
		totalSecs int64
		want      int64
	}{
		{"99.9% of 30 days", 99.9, 30 * 86400, 2591},
		{"99.95% of 30 days", 99.95, 30 * 86400, 1295},
		{"99.99% of 30 days", 99.99, 30 * 86400, 259},
		{"100% target", 100, 30 * 86400, 0},
		{"0% target", 0, 30 * 86400, 0},
		{"negative target", -1, 30 * 86400, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrorBudgetSecs(tt.target, tt.totalSecs)
			if got != tt.want {
				t.Errorf("ErrorBudgetSecs(%v, %v) = %v, want %v", tt.target, tt.totalSecs, got, tt.want)
			}
		})
	}
}

func TestDowntimeSecs(t *testing.T) {
	tests := []struct {
		name        string
		uptimePct   float64
		elapsedSecs int64
		want        int64
	}{
		{"100% uptime", 100, 86400, 0},
		{"99.9% over 1 day", 99.9, 86400, 86},
		{"99% over 1 day", 99.0, 86400, 864},
		{"0 elapsed", 99.0, 0, 0},
		{"50% uptime", 50.0, 86400, 43200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DowntimeSecs(tt.uptimePct, tt.elapsedSecs)
			if got != tt.want {
				t.Errorf("DowntimeSecs(%v, %v) = %v, want %v", tt.uptimePct, tt.elapsedSecs, got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		secs int64
		want string
	}{
		{"zero", 0, "0s"},
		{"negative", -5, "0s"},
		{"seconds", 45, "45s"},
		{"minutes", 263, "4m 23s"},
		{"hours", 7500, "2h 5m"},
		{"days", 90061, "1d 1h 1m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDuration(tt.secs)
			if got != tt.want {
				t.Errorf("FormatDuration(%v) = %v, want %v", tt.secs, got, tt.want)
			}
		})
	}
}

func TestBudgetStatus(t *testing.T) {
	tests := []struct {
		name      string
		remainPct float64
		want      string
	}{
		{"healthy", 50, "healthy"},
		{"warning", 20, "warning"},
		{"critical", 5, "critical"},
		{"breached", 0, "breached"},
		{"negative", -10, "breached"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BudgetStatus(tt.remainPct)
			if got != tt.want {
				t.Errorf("BudgetStatus(%v) = %v, want %v", tt.remainPct, got, tt.want)
			}
		})
	}
}
