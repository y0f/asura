package views

func navigationPermission(path string) string {
	switch path {
	case "/", "/monitors", "/groups", "/tags", "/sla", "/status-pages", "/agents", "/proxies":
		return "monitors.read"
	case "/monitors/new":
		return "monitors.write"
	case "/incidents":
		return "incidents.read"
	case "/notifications", "/notifications/history", "/on-call":
		return "notifications.read"
	case "/escalation-policies":
		return "escalation_policies.read"
	case "/maintenance":
		return "maintenance.read"
	case "/logs", "/audit", "/settings":
		return "metrics.read"
	default:
		return ""
	}
}

func (p LayoutParams) CanVisit(path string) bool {
	return p.Can(navigationPermission(path))
}

// LandingPath returns the first readable page, or empty when no page is allowed.
func LandingPath(perms map[string]bool) string {
	for _, path := range []string{"/", "/incidents", "/notifications", "/escalation-policies", "/maintenance", "/logs"} {
		if perms[navigationPermission(path)] {
			return path
		}
	}
	return ""
}

func (p LayoutParams) HomePath() string {
	path := LandingPath(p.Perms)
	if path == "" {
		path = "/login"
	}
	return p.BasePath + path
}
