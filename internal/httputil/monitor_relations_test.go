package httputil

import (
	"errors"
	"reflect"
	"testing"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/storage"
)

func relationKey(t *testing.T, permissions []string, bindings ...config.RoleBindingConfig) *config.APIKeyConfig {
	t.Helper()
	cfg := config.Defaults()
	cfg.Auth.Roles = []config.RoleConfig{
		{Name: "create", Permissions: []string{"monitors.create"}},
		{Name: "update", Permissions: []string{"monitors.update"}},
		{Name: "assign", Permissions: []string{"monitors.assign"}},
		{Name: "resume", Permissions: []string{"monitors.resume"}},
	}
	cfg.Auth.APIKeys = []config.APIKeyConfig{{Name: "editor", Hash: config.HashAPIKey("test"), Permissions: permissions, RoleBindings: bindings}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return &cfg.Auth.APIKeys[0]
}

func TestMonitorMoveAndResumeRequireBothScopes(t *testing.T) {
	source, destination := int64(10), int64(20)
	key := relationKey(t, []string{"groups.read", "monitors.update", "monitors.assign"},
		config.RoleBindingConfig{Role: "resume", Scope: config.MonitorAccess{GroupIDs: []int64{source}}})
	current := &storage.Monitor{ID: 1, GroupID: &source}
	candidate := &storage.Monitor{ID: 1, GroupID: &destination, Enabled: true}
	if _, err := PrepareMonitorRelations(key, candidate, current, storage.MonitorRelationChanges{Group: true, Enabled: true}); !errors.Is(err, ErrMonitorRelationForbidden) {
		t.Fatalf("source-only resume authority escaped to destination: %v", err)
	}
	key = relationKey(t, []string{"groups.read", "monitors.update", "monitors.assign"},
		config.RoleBindingConfig{Role: "resume", Scope: config.MonitorAccess{MonitorIDs: []int64{1}}})
	if _, err := PrepareMonitorRelations(key, candidate, current, storage.MonitorRelationChanges{Group: true, Enabled: true}); err != nil {
		t.Fatalf("explicit-ID resume authority should survive authorized move: %v", err)
	}
}

func TestHeartbeatTypeChangeRequiresANewMonitor(t *testing.T) {
	key := relationKey(t, []string{"monitors.update"})
	for _, pair := range [][2]string{{"heartbeat", "http"}, {"http", "heartbeat"}} {
		if _, err := PrepareMonitorRelations(key, &storage.Monitor{ID: 1, Type: pair[1]}, &storage.Monitor{ID: 1, Type: pair[0]}, storage.MonitorRelationChanges{}); err == nil {
			t.Fatalf("unsafe heartbeat type transition %v accepted", pair)
		}
	}
}

func TestMonitorRelationsKeepOmittedCurrentValues(t *testing.T) {
	group, proxy, policy := int64(5), int64(6), int64(7)
	current := &storage.Monitor{ID: 1, GroupID: &group, ProxyID: &proxy, EscalationPolicyID: &policy,
		NotificationChannelIDs: []int64{8}, MonitorTags: []storage.MonitorTag{{TagID: 9, Value: "hidden"}}, AgentIDs: []int64{10}, AgentEnabled: true}
	key := relationKey(t, []string{"monitors.update"})
	submitted := &storage.Monitor{ID: 1, Name: "edit", Enabled: true}
	changes, err := PrepareMonitorRelations(key, submitted, current, storage.MonitorRelationChanges{})
	if err != nil || changes != (storage.MonitorRelationChanges{}) {
		t.Fatalf("omitted relationships need no additional authority: %+v, %v", changes, err)
	}
	if submitted.GroupID != current.GroupID || submitted.ProxyID != current.ProxyID || submitted.EscalationPolicyID != current.EscalationPolicyID || submitted.Enabled || !submitted.AgentEnabled || !reflect.DeepEqual(submitted.AgentIDs, current.AgentIDs) || !reflect.DeepEqual(submitted.MonitorTags, current.MonitorTags) || !reflect.DeepEqual(submitted.NotificationChannelIDs, current.NotificationChannelIDs) {
		t.Fatalf("hidden current assignments or pause state were replaced: %+v", submitted)
	}
	// Round-tripping visible reference IDs is not a new delegation. Hidden names
	// are neither needed nor accepted as authority to change the relationships.
	all := storage.MonitorRelationChanges{Group: true, Proxy: true, EscalationPolicy: true, Notifications: true, Tags: true, Agents: true}
	if changes, err := PrepareMonitorRelations(key, submitted, current, all); err != nil || changes != (storage.MonitorRelationChanges{}) {
		t.Fatalf("unchanged references should remain intact: %+v, %v", changes, err)
	}
	submitted.NotificationChannelIDs = nil
	if _, err := PrepareMonitorRelations(key, submitted, current, all); !errors.Is(err, ErrMonitorRelationForbidden) {
		t.Fatalf("explicit clearing bypassed assignment authority: %v", err)
	}
}

func TestMonitorRelationsRequireBothDestinationActions(t *testing.T) {
	source, destination, other := int64(10), int64(20), int64(30)
	key := relationKey(t, []string{"groups.read"},
		config.RoleBindingConfig{Role: "update", Scope: config.MonitorAccess{MonitorIDs: []int64{1}, GroupIDs: []int64{destination}}},
		config.RoleBindingConfig{Role: "assign", Scope: config.MonitorAccess{MonitorIDs: []int64{1}, GroupIDs: []int64{other}}})
	current := &storage.Monitor{ID: 1, GroupID: &source}
	for _, target := range []*int64{&destination, &other, nil} {
		submitted := &storage.Monitor{ID: 1, GroupID: target}
		if _, err := PrepareMonitorRelations(key, submitted, current, storage.MonitorRelationChanges{Group: true}); !errors.Is(err, ErrMonitorRelationForbidden) {
			t.Fatalf("destination actions were unioned or ID grant widened: %v", err)
		}
	}
	allowed := relationKey(t, []string{"groups.read"},
		config.RoleBindingConfig{Role: "update", Scope: config.MonitorAccess{MonitorIDs: []int64{1}, GroupIDs: []int64{destination}}},
		config.RoleBindingConfig{Role: "assign", Scope: config.MonitorAccess{MonitorIDs: []int64{1}, GroupIDs: []int64{destination}}})
	changes, err := PrepareMonitorRelations(allowed, &storage.Monitor{ID: 1, GroupID: &destination}, current, storage.MonitorRelationChanges{Group: true})
	if err != nil || !changes.Group {
		t.Fatalf("matching source/destination grants rejected: %+v, %v", changes, err)
	}
}

func TestInitialMonitorPlacementDoesNotGrantOtherAssignments(t *testing.T) {
	group, proxy := int64(10), int64(11)
	key := relationKey(t, nil, config.RoleBindingConfig{Role: "create", Scope: config.MonitorAccess{GroupIDs: []int64{group}}})
	if _, err := PrepareMonitorRelations(key, &storage.Monitor{GroupID: &group}, nil, storage.MonitorRelationChanges{Group: true}); err != nil {
		t.Fatalf("initial placement incorrectly needs group metadata authority: %v", err)
	}
	if _, err := PrepareMonitorRelations(key, &storage.Monitor{GroupID: &group, ProxyID: &proxy}, nil, storage.MonitorRelationChanges{Group: true, Proxy: true}); !errors.Is(err, ErrMonitorRelationForbidden) {
		t.Fatalf("create implicitly delegated another resource: %v", err)
	}
}

func TestMonitorEnabledAndAgentDelegationNeedExplicitAuthority(t *testing.T) {
	current := &storage.Monitor{ID: 1}
	for _, grants := range [][]string{{"monitors.update"}, {"monitors.update", "monitors.pause"}} {
		if _, err := PrepareMonitorRelations(relationKey(t, grants), &storage.Monitor{ID: 1, Enabled: true}, current, storage.MonitorRelationChanges{Enabled: true}); !errors.Is(err, ErrMonitorRelationForbidden) {
			t.Fatalf("configuration/pause grant resumed a monitor: %v", err)
		}
	}
	if changes, err := PrepareMonitorRelations(relationKey(t, []string{"monitors.resume"}), &storage.Monitor{ID: 1, Enabled: true}, current, storage.MonitorRelationChanges{Enabled: true}); err != nil || !changes.Enabled {
		t.Fatalf("resume authority rejected: %+v, %v", changes, err)
	}
	key := relationKey(t, []string{"monitors.assign", "agents.read"})
	if _, err := PrepareMonitorRelations(key, &storage.Monitor{AgentEnabled: true}, nil, storage.MonitorRelationChanges{Agents: true}); err == nil {
		t.Fatal("new enabled agent assignment must not imply every agent")
	}
	if changes, err := PrepareMonitorRelations(key, &storage.Monitor{AgentEnabled: true, AgentIDs: []int64{2}}, nil, storage.MonitorRelationChanges{Agents: true}); err != nil || !changes.Agents {
		t.Fatalf("explicit agent delegation rejected: %+v, %v", changes, err)
	}
}
