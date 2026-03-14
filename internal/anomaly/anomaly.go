package anomaly

const (
	SensitivityOff    = "off"
	SensitivityLow    = "low"    // 3 standard deviations
	SensitivityMedium = "medium" // 2 standard deviations
	SensitivityHigh   = "high"   // 1.5 standard deviations
)

func Threshold(sensitivity string) float64 {
	switch sensitivity {
	case SensitivityLow:
		return 3.0
	case SensitivityMedium:
		return 2.0
	case SensitivityHigh:
		return 1.5
	default:
		return 0
	}
}

func IsAnomaly(responseTimeMs int64, baselineAvg, baselineStddev float64, sensitivity string) bool {
	if sensitivity == SensitivityOff || baselineAvg == 0 || baselineStddev == 0 {
		return false
	}
	t := Threshold(sensitivity)
	if t == 0 {
		return false
	}
	upper := baselineAvg + t*baselineStddev
	return float64(responseTimeMs) > upper
}

func ValidSensitivity(s string) bool {
	switch s {
	case SensitivityOff, SensitivityLow, SensitivityMedium, SensitivityHigh:
		return true
	}
	return false
}
