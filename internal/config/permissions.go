package config

var permissionResources = []struct {
	name    string
	actions []string
}{
	{"monitors", []string{"read", "create", "update", "delete", "pause", "resume", "status", "clone", "assign"}},
	{"diagnostics", []string{"read"}},
	{"incidents", []string{"read", "acknowledge", "resolve", "severity", "delete"}},
	{"notifications", []string{"read", "create", "update", "delete", "test"}},
	{"notification_history", []string{"read"}},
	{"escalation_policies", []string{"read", "create", "update", "delete"}},
	{"maintenance", []string{"read", "create", "update", "delete", "toggle"}},
	{"groups", []string{"read", "create", "update", "delete"}},
	{"tags", []string{"read", "create", "update", "delete"}},
	{"proxies", []string{"read", "create", "update", "delete"}},
	{"agents", []string{"read", "create", "delete"}},
	{"status_pages", []string{"read", "create", "update", "delete"}},
	{"subscribers", []string{"read", "delete"}},
	{"on_call", []string{"read", "create", "update", "delete", "override"}},
	{"request_logs", []string{"read"}},
	{"audit", []string{"read"}},
	{"settings", []string{"read"}},
	{"database", []string{"read", "maintain"}},
	{"metrics", []string{"read"}},
	{"sla", []string{"read"}},
}

// A write permission grants only its resource's mutations, never read access.
var AllPermissions, permissionParents = buildPermissions()

func buildPermissions() ([]string, map[string]string) {
	var all []string
	parents := make(map[string]string)
	for _, resource := range permissionResources {
		write := resource.name + ".write"
		mutates := false
		for _, action := range resource.actions {
			scope := resource.name + "." + action
			all = append(all, scope)
			parents[scope] = ""
			if action != "read" {
				parents[scope] = write
				mutates = true
			}
		}
		if mutates {
			all = append(all, write)
			parents[write] = ""
		}
	}
	return all, parents
}

func (k *APIKeyConfig) HasPermission(scope string) bool {
	parent, known := permissionParents[scope]
	if k == nil || !known {
		return false
	}
	if k.SuperAdmin {
		return true
	}
	if k.roleGrants[scope].All {
		return true
	}
	for _, grant := range k.Permissions {
		if grant == scope || (parent != "" && grant == parent) {
			return true
		}
	}
	return false
}

func (k *APIKeyConfig) HasAllPermissions(scopes ...string) bool {
	if k == nil {
		return false
	}
	for _, scope := range scopes {
		if !k.HasPermission(scope) {
			return false
		}
	}
	return true
}

func (k *APIKeyConfig) PermissionMap() map[string]bool {
	permissions := make(map[string]bool)
	for _, scope := range AllPermissions {
		if k.HasPermission(scope) {
			permissions[scope] = true
		}
	}
	return permissions
}

// ReadPermissions preserves the exact legacy readonly grant set. New resources
// must not silently widen old principals. Prefer an explicitly bound viewer role
// for new accounts; subscriber contacts have always needed an explicit grant.
func ReadPermissions() []string {
	return []string{
		"monitors.read", "incidents.read", "notifications.read", "notification_history.read",
		"escalation_policies.read", "maintenance.read", "groups.read", "tags.read", "proxies.read",
		"agents.read", "status_pages.read", "on_call.read", "request_logs.read", "audit.read",
		"settings.read", "database.read", "metrics.read", "sla.read",
	}
}
