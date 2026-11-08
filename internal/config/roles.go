package config

import (
	"fmt"
	"slices"
	"strings"
)

// RoleConfig defines reusable actions. A binding supplies their resource scope.
// Roles do not inherit other roles, and cannot grant super_admin.
type RoleConfig struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Permissions []string `yaml:"permissions"`
}

type RoleBindingConfig struct {
	Role  string        `yaml:"role"`
	Scope MonitorAccess `yaml:"scope"`
}

// MonitorAccess is an explicit union of monitor IDs and current group membership.
// Its zero value denies everything. All cannot be combined with selectors in a
// configured binding. Returned access values are copies, safe for query filters.
type MonitorAccess struct {
	All        bool    `yaml:"all,omitempty"`
	MonitorIDs []int64 `yaml:"monitor_ids,omitempty"`
	GroupIDs   []int64 `yaml:"group_ids,omitempty"`
}

func (a MonitorAccess) allows(id int64, groupID *int64) bool {
	if a.All {
		return true
	}
	if id > 0 {
		if _, found := slices.BinarySearch(a.MonitorIDs, id); found {
			return true
		}
	}
	if groupID != nil && *groupID > 0 {
		_, found := slices.BinarySearch(a.GroupIDs, *groupID)
		return found
	}
	return false
}

// SupportsMonitorScope identifies resources whose objects have monitor owners.
// A maintenance window needs coverage of every affected monitor, not just one.
func SupportsMonitorScope(permission string) bool {
	if _, known := permissionParents[permission]; !known {
		return false
	}
	resource, _, _ := strings.Cut(permission, ".")
	switch resource {
	case "monitors", "diagnostics", "incidents", "sla", "request_logs", "notification_history", "maintenance":
		return true
	default:
		return false
	}
}

// BuiltinRoles returns independent role definitions suitable for documentation
// or configuration tooling. Viewer deliberately excludes global administration,
// contact information, configuration, audit records and database metadata.
func BuiltinRoles() []RoleConfig {
	viewer := []string{
		"monitors.read", "incidents.read", "sla.read", "request_logs.read",
		"notification_history.read", "maintenance.read",
	}
	operator := append(slices.Clone(viewer),
		"monitors.pause", "monitors.resume", "monitors.status",
		"incidents.acknowledge", "incidents.resolve", "incidents.severity",
		"maintenance.create", "maintenance.update", "maintenance.delete", "maintenance.toggle",
	)
	return []RoleConfig{
		{Name: "viewer", Permissions: viewer},
		{Name: "operator", Permissions: operator},
		{Name: "administrator", Permissions: slices.Clone(AllPermissions)},
	}
}

func validateRolesAndAPIKeys(roles []RoleConfig, keys []APIKeyConfig) error {
	// Failed revalidation must not retain previously compiled role authority.
	for i := range keys {
		keys[i].roleGrants = nil
	}
	definitions := make(map[string][]string, len(roles)+3)
	for _, role := range BuiltinRoles() {
		definitions[role.Name] = role.Permissions
	}
	for i, role := range roles {
		path := fmt.Sprintf("auth.roles[%d]", i)
		if !validRoleName(role.Name) {
			return fmt.Errorf("%s.name must start with a lowercase letter and contain at most 64 lowercase letters, digits, underscores or hyphens", path)
		}
		if _, exists := definitions[role.Name]; exists || role.Name == "super_admin" || role.Name == "admin" || role.Name == "readonly" {
			return fmt.Errorf("%s.name %q is reserved or duplicated", path, role.Name)
		}
		if len(role.Permissions) == 0 {
			return fmt.Errorf("%s.permissions must contain at least one permission", path)
		}
		if err := validatePermissionList(path, role.Permissions); err != nil {
			return err
		}
		definitions[role.Name] = role.Permissions
	}

	// Validate and compile all principals before installing any role grants.
	validated := make([]APIKeyConfig, len(keys))
	seen := make(map[string]int, len(keys))
	for i, original := range keys {
		key := original
		path := fmt.Sprintf("auth.api_keys[%d]", i)
		if key.Name == "" {
			return fmt.Errorf("%s.name is required", path)
		}
		if j, duplicate := seen[key.Name]; duplicate {
			return fmt.Errorf("%s.name %q duplicates auth.api_keys[%d]", path, key.Name, j)
		}
		seen[key.Name] = i
		if key.Hash == "" {
			return fmt.Errorf("%s.hash is required", path)
		}
		if key.Role != "" {
			if key.SuperAdmin || len(key.Permissions) != 0 || len(key.RoleBindings) != 0 {
				return fmt.Errorf("%s.role cannot be combined with super_admin, permissions or role_bindings; migrate the legacy role to explicit grants", path)
			}
			switch key.Role {
			case "admin":
				key.SuperAdmin = true
			case "readonly":
				key.Permissions = ReadPermissions()
			default:
				return fmt.Errorf("%s invalid role: %s; use role_bindings for named roles", path, key.Role)
			}
			key.Role = ""
		}
		if !key.SuperAdmin && len(key.Permissions) == 0 && len(key.RoleBindings) == 0 {
			return fmt.Errorf("%s must have super_admin or permissions or role_bindings", path)
		}
		if err := validatePermissionList(path, key.Permissions); err != nil {
			return err
		}
		key.roleGrants = make(map[string]MonitorAccess)
		for j, binding := range key.RoleBindings {
			bindingPath := fmt.Sprintf("%s.role_bindings[%d]", path, j)
			permissions, exists := definitions[binding.Role]
			if !exists {
				return fmt.Errorf("%s.role %q is not a defined role", bindingPath, binding.Role)
			}
			access, err := validateBindingScope(bindingPath, binding.Scope)
			if err != nil {
				return err
			}
			for _, permission := range permissions {
				if !access.All && !SupportsMonitorScope(permission) {
					return fmt.Errorf("%s: permission %s requires scope.all: true; separate global and monitor-scoped roles", bindingPath, permission)
				}
				for _, action := range AllPermissions {
					if action == permission || permissionParents[action] == permission {
						key.roleGrants[action] = mergeAccess(key.roleGrants[action], access)
					}
				}
			}
		}
		validated[i] = key
	}
	copy(keys, validated)
	return nil
}

func validatePermissionList(path string, permissions []string) error {
	seen := make(map[string]bool, len(permissions))
	for _, permission := range permissions {
		if _, known := permissionParents[permission]; !known {
			return fmt.Errorf("%s invalid permission: %s", path, permission)
		}
		if seen[permission] {
			return fmt.Errorf("%s duplicate permission: %s", path, permission)
		}
		seen[permission] = true
	}
	return nil
}

func validRoleName(name string) bool {
	if len(name) == 0 || len(name) > 64 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func validateBindingScope(path string, access MonitorAccess) (MonitorAccess, error) {
	hasSelectors := len(access.MonitorIDs) != 0 || len(access.GroupIDs) != 0
	if access.All == hasSelectors {
		return MonitorAccess{}, fmt.Errorf("%s.scope must specify either all: true or nonempty monitor_ids/group_ids", path)
	}
	access.MonitorIDs = slices.Clone(access.MonitorIDs)
	access.GroupIDs = slices.Clone(access.GroupIDs)
	for _, field := range []struct {
		name string
		ids  []int64
	}{{"monitor_ids", access.MonitorIDs}, {"group_ids", access.GroupIDs}} {
		slices.Sort(field.ids)
		for i, id := range field.ids {
			if id <= 0 {
				return MonitorAccess{}, fmt.Errorf("%s.scope.%s must contain positive IDs", path, field.name)
			}
			if i > 0 && id == field.ids[i-1] {
				return MonitorAccess{}, fmt.Errorf("%s.scope.%s contains duplicate ID %d", path, field.name, id)
			}
		}
	}
	return access, nil
}

func mergeAccess(a, b MonitorAccess) MonitorAccess {
	if a.All || b.All {
		return MonitorAccess{All: true}
	}
	merge := func(a, b []int64) []int64 {
		ids := append(slices.Clone(a), b...)
		slices.Sort(ids)
		return slices.Compact(ids)
	}
	return MonitorAccess{MonitorIDs: merge(a.MonitorIDs, b.MonitorIDs), GroupIDs: merge(a.GroupIDs, b.GroupIDs)}
}

// HasCapability admits a resource route or navigation entry. It is not an
// object authorization check: handlers must still filter and check each object.
func (k *APIKeyConfig) HasCapability(permission string) bool {
	if k.HasPermission(permission) {
		return true
	}
	if k == nil || !SupportsMonitorScope(permission) {
		return false
	}
	access := k.roleGrants[permission]
	// A new monitor cannot match an existing ID. Creation needs at least one
	// group destination (or the global grant handled above).
	if permission == "monitors.create" {
		return len(access.GroupIDs) != 0
	}
	return len(access.MonitorIDs) != 0 || len(access.GroupIDs) != 0
}

func (k *APIKeyConfig) HasAllCapabilities(permissions ...string) bool {
	if k == nil {
		return false
	}
	for _, permission := range permissions {
		if !k.HasCapability(permission) {
			return false
		}
	}
	return true
}

func (k *APIKeyConfig) CapabilityMap() map[string]bool {
	permissions := make(map[string]bool)
	for _, permission := range AllPermissions {
		if k.HasCapability(permission) {
			permissions[permission] = true
		}
	}
	return permissions
}

// AllowsMonitor checks one action against one current monitor. An ID of zero
// checks a destination group for creation; an ungrouped destination needs a
// global grant. Call this separately for every action required by an operation.
func (k *APIKeyConfig) AllowsMonitor(permission string, id int64, groupID *int64) bool {
	if !SupportsMonitorScope(permission) {
		return false
	}
	if k.HasPermission(permission) {
		return true
	}
	return k != nil && k.roleGrants[permission].allows(id, groupID)
}

// AccessFor returns selectors for one action, never a union across actions.
// Unknown permissions and unsupported monitor-scoped resources deny by default.
func (k *APIKeyConfig) AccessFor(permission string) MonitorAccess {
	if !SupportsMonitorScope(permission) || k == nil {
		return MonitorAccess{}
	}
	if k.HasPermission(permission) {
		return MonitorAccess{All: true}
	}
	access := k.roleGrants[permission]
	access.MonitorIDs = slices.Clone(access.MonitorIDs)
	access.GroupIDs = slices.Clone(access.GroupIDs)
	return access
}
