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

// severityRank orders severities from least (warning) to most (critical) severe.
func severityRank(s string) int {
	switch s {
	case SeverityWarning:
		return 1
	case SeverityMinor:
		return 2
	case SeverityMajor:
		return 3
	case SeverityCritical:
		return 4
	}
	return 0
}

func ValidSeverity(s string) bool {
	switch s {
	case SeverityCritical, SeverityMajor, SeverityMinor, SeverityWarning:
		return true
	}
	return false
}

const (
	EventCreated         = "created"
	EventAcknowledged    = "acknowledged"
	EventResolved        = "resolved"
	EventCheckFailed     = "check_failed"
	EventCheckRecovered  = "check_recovered"
	EventSeverityChanged = "severity_changed"
)
