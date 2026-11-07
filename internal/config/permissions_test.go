package config

import (
	"strings"
	"testing"
)

func TestPermissionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   *APIKeyConfig
		allow []string
		deny  []string
	}{
		{"no key", nil, nil, []string{"monitors.read", "monitors.create"}},
		{"read only", &APIKeyConfig{Permissions: []string{"monitors.read"}}, []string{"monitors.read"}, []string{"monitors.create", "groups.read", "proxies.read", "agents.read", "status_pages.read", "sla.read"}},
		{"writer", &APIKeyConfig{Permissions: []string{"monitors.write"}}, []string{"monitors.create", "monitors.update", "monitors.delete", "monitors.pause", "monitors.resume", "monitors.status", "monitors.clone", "monitors.assign"}, []string{"monitors.read", "agents.create", "proxies.create", "groups.create", "status_pages.create", "notifications.create", "database.maintain"}},
		{"one action", &APIKeyConfig{Permissions: []string{"incidents.acknowledge"}}, []string{"incidents.acknowledge"}, []string{"incidents.read", "incidents.resolve", "incidents.delete", "incidents.write"}},
		{"metrics only", &APIKeyConfig{Permissions: []string{"metrics.read"}}, []string{"metrics.read"}, []string{"settings.read", "audit.read", "request_logs.read", "database.read"}},
		{"admin", &APIKeyConfig{SuperAdmin: true}, AllPermissions, []string{"", "*", "monitors.*", "monitors.misspelled"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			permissions := tc.key.PermissionMap()
			for _, scope := range tc.allow {
				if !tc.key.HasPermission(scope) || !permissions[scope] {
					t.Errorf("missing allowed permission %s", scope)
				}
			}
			for _, scope := range tc.deny {
				if tc.key.HasPermission(scope) || permissions[scope] {
					t.Errorf("unexpected permission %s", scope)
				}
			}
		})
	}
}

func TestPermissionRegistry(t *testing.T) {
	seen := make(map[string]bool)
	for _, scope := range AllPermissions {
		if seen[scope] {
			t.Fatalf("duplicate permission %s", scope)
		}
		seen[scope] = true
		key := &APIKeyConfig{Name: "scope", Hash: "synthetic", Permissions: []string{scope}}
		if err := validateAPIKeys([]APIKeyConfig{*key}); err != nil {
			t.Errorf("registered scope rejected: %s: %v", scope, err)
		}
		permissions := key.PermissionMap()
		for _, other := range AllPermissions {
			if got := permissions[other]; got != key.HasPermission(other) {
				t.Errorf("map and handler disagree for %s / %s", scope, other)
			}
			if strings.HasSuffix(scope, ".write") && permissions[other] && strings.SplitN(scope, ".", 2)[0] != strings.SplitN(other, ".", 2)[0] {
				t.Errorf("%s grants another resource: %s", scope, other)
			}
		}
	}
	for _, scope := range []string{"monitors.*", "unknown.read", "monitors.updtae"} {
		if err := validateAPIKeys([]APIKeyConfig{{Name: "invalid", Hash: "synthetic", Permissions: []string{scope}}}); err == nil {
			t.Errorf("unknown scope accepted: %s", scope)
		}
	}
}

func TestPermissionIntersectionAndLegacyReadonly(t *testing.T) {
	key := &APIKeyConfig{Permissions: []string{"monitors.clone"}}
	if key.HasAllPermissions("monitors.read", "monitors.clone") {
		t.Fatal("clone alone grants source read")
	}
	key.Permissions = append(key.Permissions, "monitors.read")
	if !key.HasAllPermissions("monitors.read", "monitors.clone") {
		t.Fatal("explicit clone plus source read rejected")
	}
	keys := []APIKeyConfig{{Name: "legacy", Hash: "synthetic", Role: "readonly"}}
	if err := validateAPIKeys(keys); err != nil {
		t.Fatal(err)
	}
	if !keys[0].HasPermission("agents.read") || keys[0].HasPermission("subscribers.read") || keys[0].HasPermission("monitors.create") {
		t.Fatal("legacy readonly role crossed its read boundary")
	}
}
