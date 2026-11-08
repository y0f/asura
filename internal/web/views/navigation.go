package views

func navigationPermission(path string) string {
	switch path {
	case "/", "/monitors":
		return "monitors.read"
	case "/monitors/new":
		return "monitors.create"
	case "/groups":
		return "groups.read"
	case "/tags":
		return "tags.read"
	case "/sla":
		return "sla.read"
	case "/status-pages":
		return "status_pages.read"
	case "/agents":
		return "agents.read"
	case "/proxies":
		return "proxies.read"
	case "/incidents":
		return "incidents.read"
	case "/notifications":
		return "notifications.read"
	case "/notifications/history":
		return "notification_history.read"
	case "/on-call":
		return "on_call.read"
	case "/escalation-policies":
		return "escalation_policies.read"
	case "/maintenance":
		return "maintenance.read"
	case "/logs":
		return "request_logs.read"
	case "/audit":
		return "audit.read"
	case "/settings":
		return "settings.read"
	default:
		return ""
	}
}

func (p LayoutParams) CanVisit(path string) bool {
	return p.HasCapability(navigationPermission(path))
}

// LandingPath returns the first readable page, or empty when no page is allowed.
func LandingPath(perms map[string]bool) string {
	return landingPath(func(permission string) bool { return perms[permission] })
}

func landingPath(can func(string) bool) string {
	for _, path := range []string{"/", "/incidents", "/notifications", "/escalation-policies", "/maintenance", "/groups", "/tags", "/sla", "/status-pages", "/agents", "/proxies", "/on-call", "/notifications/history", "/logs", "/audit", "/settings"} {
		if can(navigationPermission(path)) {
			return path
		}
	}
	return ""
}

func (p LayoutParams) HomePath() string {
	path := landingPath(p.HasCapability)
	if path == "" {
		path = "/login"
	}
	return p.BasePath + path
}
