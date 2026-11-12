package httputil

import (
	"errors"
	"fmt"

	"github.com/y0f/asura/v3/internal/config"
	"github.com/y0f/asura/v3/internal/storage"
	"github.com/y0f/asura/v3/internal/validate"
)

var ErrMonitorRelationForbidden = errors.New("monitor relationship change is not permitted")

// ErrHostLocalMonitorForbidden wraps ErrMonitorRelationForbidden so existing
// handlers answer it with 403 Forbidden.
var ErrHostLocalMonitorForbidden = fmt.Errorf("%w: command and docker monitors require super admin access", ErrMonitorRelationForbidden)

// IsHostLocalMonitorType reports whether a monitor type runs on the server host
// itself (a local executable or the container runtime socket).
func IsHostLocalMonitorType(monitorType string) bool {
	return monitorType == "command" || monitorType == "docker"
}

// PrepareMonitorRelations is pure so it can run inside the monitor update
// transaction. Omitted fields keep current assignments before credentials are
// merged; explicit changes require assignment and related-resource authority.
// Initial group placement is governed by the separate create destination grant.
func PrepareMonitorRelations(key *config.APIKeyConfig, m, current *storage.Monitor, fields storage.MonitorRelationChanges) (storage.MonitorRelationChanges, error) {
	creating := current == nil
	// Command and Docker checks run on the server host with its privileges.
	// Only a super admin may create, clone or edit one, or convert to or from one.
	if (IsHostLocalMonitorType(m.Type) || (!creating && IsHostLocalMonitorType(current.Type))) && (key == nil || !key.SuperAdmin) {
		return storage.MonitorRelationChanges{}, ErrHostLocalMonitorForbidden
	}
	if !creating && m.Type != current.Type && (m.Type == "heartbeat" || current.Type == "heartbeat") {
		return storage.MonitorRelationChanges{}, fmt.Errorf("heartbeat monitor type cannot be changed; create a new monitor and delete the old one")
	}
	if creating {
		current = &storage.Monitor{GroupID: m.GroupID}
	}
	if !fields.Group {
		m.GroupID = current.GroupID
	}
	if !fields.Proxy {
		m.ProxyID = current.ProxyID
	}
	if !fields.EscalationPolicy {
		m.EscalationPolicyID = current.EscalationPolicyID
	}
	if !fields.Notifications {
		m.NotificationChannelIDs = current.NotificationChannelIDs
	}
	if !fields.Tags {
		m.MonitorTags = current.MonitorTags
	}
	if !fields.Agents {
		m.AgentIDs, m.AgentEnabled = current.AgentIDs, current.AgentEnabled
	}
	if !creating && !fields.Enabled {
		m.Enabled = current.Enabled
	}
	for _, id := range []*int64{m.GroupID, m.ProxyID, m.EscalationPolicyID} {
		if id != nil && *id <= 0 {
			return storage.MonitorRelationChanges{}, fmt.Errorf("relationship IDs must be positive")
		}
	}
	for _, ids := range [][]int64{m.NotificationChannelIDs, m.AgentIDs} {
		if err := validRelationIDs(ids); err != nil {
			return storage.MonitorRelationChanges{}, err
		}
	}
	tagIDs := make([]int64, len(m.MonitorTags))
	for i, tag := range m.MonitorTags {
		tagIDs[i] = tag.TagID
	}
	if err := validRelationIDs(tagIDs); err != nil {
		return storage.MonitorRelationChanges{}, err
	}
	if err := validate.ValidateMonitorTags(m.MonitorTags); err != nil {
		return storage.MonitorRelationChanges{}, err
	}
	changes := storage.MonitorRelationChanges{
		Group: !sameRelationID(m.GroupID, current.GroupID), Proxy: !sameRelationID(m.ProxyID, current.ProxyID),
		EscalationPolicy: !sameRelationID(m.EscalationPolicyID, current.EscalationPolicyID),
		Notifications:    !sameRelationIDs(m.NotificationChannelIDs, current.NotificationChannelIDs),
		Tags:             !sameMonitorTags(m.MonitorTags, current.MonitorTags),
		Agents:           m.AgentEnabled != current.AgentEnabled || !sameRelationIDs(m.AgentIDs, current.AgentIDs),
		Enabled:          !creating && m.Enabled != current.Enabled,
	}
	assignID, assignGroup := current.ID, current.GroupID
	if creating {
		assignID, assignGroup = 0, m.GroupID
	}
	for _, relation := range []struct {
		changed  bool
		resource string
	}{{changes.Group, "groups"}, {changes.Proxy, "proxies"}, {changes.EscalationPolicy, "escalation_policies"}, {changes.Notifications, "notifications"}, {changes.Tags, "tags"}, {changes.Agents, "agents"}} {
		if relation.changed && (!key.HasPermission(relation.resource+".read") || !key.AllowsMonitor("monitors.assign", assignID, assignGroup)) {
			return storage.MonitorRelationChanges{}, fmt.Errorf("%w: requires monitors.assign and %s.read", ErrMonitorRelationForbidden, relation.resource)
		}
	}
	if changes.Group && (!key.AllowsMonitor("monitors.update", 0, m.GroupID) || !key.AllowsMonitor("monitors.assign", 0, m.GroupID)) {
		return storage.MonitorRelationChanges{}, fmt.Errorf("%w: destination requires monitors.update and monitors.assign", ErrMonitorRelationForbidden)
	}
	if changes.Enabled {
		action := "monitors.pause"
		if m.Enabled {
			action = "monitors.resume"
		}
		if !key.AllowsMonitor(action, current.ID, current.GroupID) || !key.AllowsMonitor(action, current.ID, m.GroupID) {
			return storage.MonitorRelationChanges{}, fmt.Errorf("%w: requires %s", ErrMonitorRelationForbidden, action)
		}
	}
	if (creating || changes.Agents) && m.AgentEnabled && len(m.AgentIDs) == 0 {
		return storage.MonitorRelationChanges{}, fmt.Errorf("select at least one agent before enabling agent checks")
	}
	return changes, nil
}

func validRelationIDs(ids []int64) error {
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("relationship IDs must be positive")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("relationship IDs must be unique")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func sameRelationID(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func sameRelationIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[int64]struct{}, len(a))
	for _, id := range a {
		set[id] = struct{}{}
	}
	for _, id := range b {
		if _, exists := set[id]; !exists {
			return false
		}
	}
	return true
}

func sameMonitorTags(a, b []storage.MonitorTag) bool {
	if len(a) != len(b) {
		return false
	}
	values := make(map[int64]string, len(a))
	for _, tag := range a {
		values[tag.TagID] = tag.Value
	}
	for _, tag := range b {
		if value, exists := values[tag.TagID]; !exists || value != tag.Value {
			return false
		}
	}
	return true
}
