package config

import (
	"slices"
	"testing"
)

func TestDiagnosticsRequireExplicitScopedOptIn(t *testing.T) {
	for _, role := range BuiltinRoles() {
		if got := slices.Contains(role.Permissions, "diagnostics.read"); got != (role.Name == "administrator") {
			t.Fatalf("role %s diagnostic default = %t", role.Name, got)
		}
	}
	if slices.Contains(ReadPermissions(), "diagnostics.read") {
		t.Fatal("legacy readonly silently gained diagnostics")
	}
	keys := []APIKeyConfig{{Name: "scoped", Hash: "test", RoleBindings: []RoleBindingConfig{
		{Role: "viewer", Scope: MonitorAccess{MonitorIDs: []int64{1}}},
		{Role: "debugger", Scope: MonitorAccess{GroupIDs: []int64{2}}},
	}}}
	if err := validateRolesAndAPIKeys([]RoleConfig{{Name: "debugger", Permissions: []string{"diagnostics.read"}}}, keys); err != nil {
		t.Fatal(err)
	}
	group := int64(2)
	key := &keys[0]
	if key.HasPermission("diagnostics.read") || !key.AllowsMonitor("diagnostics.read", 9, &group) || key.AllowsMonitor("diagnostics.read", 1, nil) || key.AllowsMonitor("monitors.read", 9, &group) {
		t.Fatal("diagnostic binding widened another scope or action")
	}
}
