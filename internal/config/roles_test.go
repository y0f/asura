package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRoleActionsRetainTheirOwnResourceScope(t *testing.T) {
	roles := []RoleConfig{
		{Name: "reader", Permissions: []string{"monitors.read", "incidents.read"}},
		{Name: "editor", Permissions: []string{"monitors.update"}},
		{Name: "writer", Permissions: []string{"monitors.write"}},
	}
	keys := []APIKeyConfig{{Name: "operator", Hash: "synthetic", RoleBindings: []RoleBindingConfig{
		{Role: "reader", Scope: MonitorAccess{MonitorIDs: []int64{10}, GroupIDs: []int64{3}}},
		{Role: "editor", Scope: MonitorAccess{MonitorIDs: []int64{20}}},
		{Role: "writer", Scope: MonitorAccess{GroupIDs: []int64{4}}},
	}}}
	if err := validateRolesAndAPIKeys(roles, keys); err != nil {
		t.Fatal(err)
	}
	key := &keys[0]
	group3, group4, group5 := int64(3), int64(4), int64(5)
	for _, tc := range []struct {
		action string
		id     int64
		group  *int64
		allow  bool
	}{
		{"monitors.read", 10, nil, true},
		{"monitors.read", 20, nil, false},
		{"monitors.update", 10, nil, false},
		{"monitors.update", 20, nil, true},
		{"monitors.read", 30, &group3, true},
		{"incidents.read", 30, &group3, true},
		{"monitors.update", 30, &group3, false},
		{"monitors.delete", 30, &group4, true},
		{"monitors.read", 30, &group4, false},
		{"incidents.delete", 30, &group4, false},
		{"monitors.read", 30, &group5, false},
		{"groups.read", 3, &group3, false},
		{"monitors.create", 0, &group4, true},
		{"monitors.create", 0, nil, false},
		{"monitors.create", 0, &group3, false},
		{"monitors.unknown", 10, &group3, false},
	} {
		if got := key.AllowsMonitor(tc.action, tc.id, tc.group); got != tc.allow {
			t.Errorf("AllowsMonitor(%s, %d, %v) = %t, want %t", tc.action, tc.id, tc.group, got, tc.allow)
		}
	}
	if !key.HasAllCapabilities("monitors.read", "monitors.update") || key.HasAllPermissions("monitors.read", "monitors.update") {
		t.Fatal("coarse route capabilities must not become global authority")
	}
	for _, permission := range AllPermissions {
		if key.PermissionMap()[permission] {
			t.Errorf("scoped binding granted global %s", permission)
		}
		if key.CapabilityMap()[permission] != key.HasCapability(permission) {
			t.Errorf("capability map differs for %s", permission)
		}
	}
	access := key.AccessFor("monitors.update")
	if access.All || !reflect.DeepEqual(access.MonitorIDs, []int64{20}) || !reflect.DeepEqual(access.GroupIDs, []int64{4}) {
		t.Fatalf("update selectors combined other actions: %+v", access)
	}
	access.MonitorIDs[0] = 10
	access.GroupIDs[0] = 3
	if key.AllowsMonitor("monitors.update", 10, &group3) {
		t.Fatal("query caller mutated compiled authority through returned selectors")
	}
}

func TestRoleBindingsUnionOnlyTheSameAction(t *testing.T) {
	keys := []APIKeyConfig{{Name: "reader", Hash: "synthetic", Permissions: []string{"groups.read"}, RoleBindings: []RoleBindingConfig{
		{Role: "viewer", Scope: MonitorAccess{MonitorIDs: []int64{3, 1}, GroupIDs: []int64{2}}},
		{Role: "viewer", Scope: MonitorAccess{MonitorIDs: []int64{2, 3}, GroupIDs: []int64{3, 2}}},
	}}}
	if err := validateAPIKeys(keys); err != nil {
		t.Fatal(err)
	}
	access := keys[0].AccessFor("monitors.read")
	if !reflect.DeepEqual(access.MonitorIDs, []int64{1, 2, 3}) || !reflect.DeepEqual(access.GroupIDs, []int64{2, 3}) {
		t.Fatalf("same-action union = %+v", access)
	}
	if !keys[0].HasPermission("groups.read") || keys[0].HasPermission("monitors.read") || keys[0].HasCapability("monitors.update") {
		t.Fatal("explicit global compatibility grant widened unrelated actions")
	}
	keys[0].RoleBindings = append(keys[0].RoleBindings, RoleBindingConfig{Role: "viewer", Scope: MonitorAccess{All: true}})
	if err := validateAPIKeys(keys); err != nil {
		t.Fatal(err)
	}
	if !keys[0].HasPermission("monitors.read") || !keys[0].AccessFor("monitors.read").All || keys[0].HasCapability("monitors.update") {
		t.Fatal("global viewer must union only its read actions")
	}
}

func TestIDOnlyGrantDoesNotOfferMonitorCreation(t *testing.T) {
	roles := []RoleConfig{{Name: "writer", Permissions: []string{"monitors.write"}}}
	keys := []APIKeyConfig{{Name: "writer", Hash: "synthetic", RoleBindings: []RoleBindingConfig{{Role: "writer", Scope: MonitorAccess{MonitorIDs: []int64{1}}}}}}
	if err := validateRolesAndAPIKeys(roles, keys); err != nil {
		t.Fatal(err)
	}
	if keys[0].HasCapability("monitors.create") || keys[0].CapabilityMap()["monitors.create"] || !keys[0].HasCapability("monitors.update") {
		t.Fatal("ID-only grant offered an unusable create route or lost ordinary editing")
	}
	keys[0].RoleBindings[0].Scope.GroupIDs = []int64{2}
	if err := validateRolesAndAPIKeys(roles, keys); err != nil {
		t.Fatal(err)
	}
	if !keys[0].HasCapability("monitors.create") {
		t.Fatal("explicit group destination did not enable creation capability")
	}
}

func TestRoleValidationRejectsAmbiguousOrInvalidAuthority(t *testing.T) {
	base := func() ([]RoleConfig, []APIKeyConfig) {
		return []RoleConfig{{Name: "custom", Permissions: []string{"monitors.read"}}}, []APIKeyConfig{{
			Name: "key", Hash: "synthetic", RoleBindings: []RoleBindingConfig{{Role: "custom", Scope: MonitorAccess{MonitorIDs: []int64{1}}}},
		}}
	}
	for _, tc := range []struct {
		name   string
		modify func(*[]RoleConfig, *[]APIKeyConfig)
		want   string
	}{
		{"empty role name", func(r *[]RoleConfig, _ *[]APIKeyConfig) { (*r)[0].Name = "" }, ".name must"},
		{"invalid role name", func(r *[]RoleConfig, _ *[]APIKeyConfig) { (*r)[0].Name = "Administrator" }, ".name must"},
		{"oversized role name", func(r *[]RoleConfig, _ *[]APIKeyConfig) { (*r)[0].Name = strings.Repeat("a", 65) }, ".name must"},
		{"reserved role", func(r *[]RoleConfig, _ *[]APIKeyConfig) { (*r)[0].Name = "viewer" }, "reserved or duplicated"},
		{"super admin role", func(r *[]RoleConfig, _ *[]APIKeyConfig) { (*r)[0].Name = "super_admin" }, "reserved or duplicated"},
		{"duplicate role", func(r *[]RoleConfig, _ *[]APIKeyConfig) { *r = append(*r, (*r)[0]) }, "reserved or duplicated"},
		{"empty permissions", func(r *[]RoleConfig, _ *[]APIKeyConfig) { (*r)[0].Permissions = nil }, "at least one"},
		{"unknown action", func(r *[]RoleConfig, _ *[]APIKeyConfig) { (*r)[0].Permissions = []string{"monitors.updtae"} }, "invalid permission"},
		{"super admin action", func(r *[]RoleConfig, _ *[]APIKeyConfig) { (*r)[0].Permissions = []string{"super_admin"} }, "invalid permission"},
		{"duplicate action", func(r *[]RoleConfig, _ *[]APIKeyConfig) {
			(*r)[0].Permissions = []string{"monitors.read", "monitors.read"}
		}, "duplicate permission"},
		{"undefined role", func(_ *[]RoleConfig, k *[]APIKeyConfig) { (*k)[0].RoleBindings[0].Role = "typo" }, "not a defined role"},
		{"empty scope", func(_ *[]RoleConfig, k *[]APIKeyConfig) { (*k)[0].RoleBindings[0].Scope = MonitorAccess{} }, "either all: true"},
		{"all plus monitor IDs", func(_ *[]RoleConfig, k *[]APIKeyConfig) { (*k)[0].RoleBindings[0].Scope.All = true }, "either all: true"},
		{"all plus group IDs", func(_ *[]RoleConfig, k *[]APIKeyConfig) {
			(*k)[0].RoleBindings[0].Scope = MonitorAccess{All: true, GroupIDs: []int64{1}}
		}, "either all: true"},
		{"zero monitor ID", func(_ *[]RoleConfig, k *[]APIKeyConfig) { (*k)[0].RoleBindings[0].Scope.MonitorIDs = []int64{0} }, "positive IDs"},
		{"negative group ID", func(_ *[]RoleConfig, k *[]APIKeyConfig) { (*k)[0].RoleBindings[0].Scope.GroupIDs = []int64{-1} }, "positive IDs"},
		{"duplicate ID", func(_ *[]RoleConfig, k *[]APIKeyConfig) { (*k)[0].RoleBindings[0].Scope.MonitorIDs = []int64{1, 1} }, "duplicate ID"},
		{"scoped global resource", func(r *[]RoleConfig, _ *[]APIKeyConfig) { (*r)[0].Permissions = []string{"groups.read"} }, "requires scope.all: true"},
		{"scoped administrator", func(_ *[]RoleConfig, k *[]APIKeyConfig) { (*k)[0].RoleBindings[0].Role = "administrator" }, "requires scope.all: true"},
		{"legacy plus bindings", func(_ *[]RoleConfig, k *[]APIKeyConfig) { (*k)[0].Role = "readonly" }, "cannot be combined"},
		{"legacy plus direct grants", func(_ *[]RoleConfig, k *[]APIKeyConfig) {
			(*k)[0].RoleBindings = nil
			(*k)[0].Role = "readonly"
			(*k)[0].Permissions = []string{"monitors.update"}
		}, "cannot be combined"},
		{"legacy plus super admin", func(_ *[]RoleConfig, k *[]APIKeyConfig) {
			(*k)[0].RoleBindings = nil
			(*k)[0].Role = "readonly"
			(*k)[0].SuperAdmin = true
		}, "cannot be combined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roles, keys := base()
			tc.modify(&roles, &keys)
			if err := validateRolesAndAPIKeys(roles, keys); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestRoleRevalidationRevokesCompiledGrants(t *testing.T) {
	keys := []APIKeyConfig{{Name: "key", Hash: "synthetic", RoleBindings: []RoleBindingConfig{{Role: "viewer", Scope: MonitorAccess{MonitorIDs: []int64{1}}}}}}
	if err := validateAPIKeys(keys); err != nil {
		t.Fatal(err)
	}
	keys[0].RoleBindings[0].Scope.MonitorIDs[0] = 2
	if !keys[0].AllowsMonitor("monitors.read", 1, nil) || keys[0].AllowsMonitor("monitors.read", 2, nil) {
		t.Fatal("changing source configuration mutated validated grant storage")
	}
	if err := validateAPIKeys(keys); err != nil {
		t.Fatal(err)
	}
	if keys[0].AllowsMonitor("monitors.read", 1, nil) || !keys[0].AllowsMonitor("monitors.read", 2, nil) {
		t.Fatal("revalidation retained previous object scope")
	}
	keys = append(keys, APIKeyConfig{Name: "bad", Hash: "synthetic", RoleBindings: []RoleBindingConfig{{Role: "missing", Scope: MonitorAccess{All: true}}}})
	if err := validateAPIKeys(keys); err == nil {
		t.Fatal("invalid second principal accepted")
	}
	if keys[0].HasCapability("monitors.read") {
		t.Fatal("failed validation left partial or stale role grants")
	}
}

func TestBuiltinRolesAndLegacyCompatibility(t *testing.T) {
	keys := []APIKeyConfig{
		{Name: "admin", Hash: "synthetic", Role: "admin"},
		{Name: "old-reader", Hash: "synthetic", Role: "readonly"},
		{Name: "reader", Hash: "synthetic", RoleBindings: []RoleBindingConfig{{Role: "viewer", Scope: MonitorAccess{All: true}}}},
		{Name: "operator", Hash: "synthetic", RoleBindings: []RoleBindingConfig{{Role: "operator", Scope: MonitorAccess{All: true}}}},
		{Name: "administrator", Hash: "synthetic", RoleBindings: []RoleBindingConfig{{Role: "administrator", Scope: MonitorAccess{All: true}}}},
	}
	if err := validateAPIKeys(keys); err != nil {
		t.Fatal(err)
	}
	if !keys[0].SuperAdmin || keys[0].Role != "" || keys[4].SuperAdmin {
		t.Fatal("ordinary administrator crossed explicit super-admin boundary")
	}
	if !keys[1].HasPermission("audit.read") || keys[2].HasPermission("audit.read") || keys[2].HasPermission("subscribers.read") {
		t.Fatal("safe viewer changed legacy behavior or exposed unrelated records")
	}
	if !keys[3].HasPermission("monitors.pause") || keys[3].HasPermission("monitors.update") || keys[3].HasPermission("monitors.assign") || keys[3].HasPermission("incidents.delete") {
		t.Fatal("operator exceeded operational actions")
	}
	for _, action := range AllPermissions {
		if !keys[4].HasPermission(action) || !keys[4].PermissionMap()[action] {
			t.Errorf("administrator missing ordinary action %s", action)
		}
	}
	for _, key := range []*APIKeyConfig{nil, {}, {SuperAdmin: true}, &keys[4]} {
		for _, action := range []string{"", "*", "monitors.*", "super_admin", "monitors.typo"} {
			if key.HasPermission(action) || key.HasCapability(action) || key.AllowsMonitor(action, 1, nil) || key.AccessFor(action).All {
				t.Errorf("unknown action %q authorized", action)
			}
		}
	}
	if (&APIKeyConfig{RoleBindings: []RoleBindingConfig{{Role: "viewer", Scope: MonitorAccess{All: true}}}}).HasCapability("monitors.read") {
		t.Fatal("unvalidated role binding authorized")
	}
}

func TestLoadRoleConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := `auth:
  roles:
    - name: production-editor
      description: Change production monitors without reading unrelated data
      permissions: [monitors.read, monitors.update]
  api_keys:
    - name: bootstrap
      hash: synthetic-admin-hash
      super_admin: true
    - name: production
      hash: synthetic-operator-hash
      role_bindings:
        - role: production-editor
          scope:
            group_ids: [2]
        - role: viewer
          scope:
            monitor_ids: [7]
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	key := cfg.LookupAPIKeyByName("production")
	group := int64(2)
	if !key.AllowsMonitor("monitors.update", 10, &group) || key.AllowsMonitor("monitors.update", 7, nil) || !key.AllowsMonitor("monitors.read", 7, nil) {
		t.Fatal("loaded bindings lost action-to-scope association")
	}
}
