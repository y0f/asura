package incident

const (
	StatusOpen         = "open"
	StatusAcknowledged = "acknowledged"
	StatusResolved     = "resolved"
)

const (
	SeverityCritical = "critical"
	SeverityMajor    = "major"
	SeverityMinor    = "minor"
	SeverityWarning  = "warning"
)

func SeverityForStatus(monitorStatus string) string {
	switch monitorStatus {
	case "down":
		return SeverityCritical
	case "degraded":
		return SeverityWarning
	default:
		return SeverityCritical
	}
}

func ValidSeverity(s string) bool {
	switch s {
	case SeverityCritical, SeverityMajor, SeverityMinor, SeverityWarning:
		return true
	}
	return false
}

const (
	EventCreated        = "created"
	EventAcknowledged   = "acknowledged"
	EventResolved       = "resolved"
	EventCheckFailed    = "check_failed"
	EventCheckRecovered = "check_recovered"
)
