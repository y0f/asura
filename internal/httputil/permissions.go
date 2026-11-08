package httputil

// MonitorBulkPermissions keeps API and form bulk operations on the same
// authorization boundary. Nil means the action is not supported.
func MonitorBulkPermissions(action string) []string {
	switch action {
	case "pause", "resume", "delete":
		return []string{"monitors." + action}
	case "set_group":
		return []string{"monitors.update", "monitors.assign", "groups.read"}
	default:
		return nil
	}
}
