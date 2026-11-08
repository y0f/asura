package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

var ErrInvalidConfiguration = errors.New("invalid configuration")

func invalidConfiguration(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfiguration, fmt.Sprintf(format, args...))
}

type configurationIdentity struct {
	id   int64
	name string
}
type configurationCatalog struct {
	ids     map[int64]int64 // source-local ID -> destination ID; zero means new
	created *int
}

type configurationImport struct {
	ctx                                              context.Context
	tx                                               *sql.Tx
	data                                             *ConfigurationSnapshot
	result                                           ConfigurationImportResult
	catalogs                                         map[string]*configurationCatalog
	agents                                           map[int64]int64
	monitorSettings, channelSettings, proxyPasswords map[int64]string
	heartbeatTokens                                  map[int64]string
}

func checkConfigurationShape(c *ConfigurationSnapshot) error {
	if slices.Contains(c.Monitors, nil) || slices.Contains(c.Groups, nil) || slices.Contains(c.Tags, nil) ||
		slices.Contains(c.Proxies, nil) || slices.Contains(c.Channels, nil) || slices.Contains(c.EscalationPolicies, nil) ||
		slices.Contains(c.Rotations, nil) || slices.Contains(c.Maintenance, nil) || slices.Contains(c.StatusPages, nil) {
		return invalidConfiguration("null configuration entity")
	}
	for _, p := range c.EscalationPolicies {
		if slices.Contains(p.Steps, nil) {
			return invalidConfiguration("null escalation step")
		}
	}
	return nil
}

func configurationIdentities[T any](items []*T, identity func(*T) (int64, string)) ([]configurationIdentity, error) {
	out := make([]configurationIdentity, 0, len(items))
	for _, item := range items {
		if item == nil {
			return nil, invalidConfiguration("null entity")
		}
		id, name := identity(item)
		out = append(out, configurationIdentity{id, name})
	}
	return out, nil
}

func prepareConfigurationCatalog[T any](ic *configurationImport, mode, table, nameColumn string, items []*T, identity func(*T) (int64, string), created *int) error {
	identities, err := configurationIdentities(items, identity)
	if err != nil {
		return err
	}
	catalog := &configurationCatalog{ids: make(map[int64]int64, len(items)), created: created}
	nameCounts := make(map[string]int, len(items))
	for _, item := range identities {
		if item.id <= 0 {
			return invalidConfiguration("%s IDs must be positive", table)
		}
		if _, exists := catalog.ids[item.id]; exists {
			return invalidConfiguration("duplicate %s ID %d", table, item.id)
		}
		catalog.ids[item.id] = 0
		nameCounts[item.name]++
		if nameCounts[item.name] > 1 && (table == "tags" || table == "status_pages") {
			return invalidConfiguration("duplicate %s identity", table)
		}
	}
	if mode == "merge" {
		existing := make(map[string][]int64)
		if err := readConfigurationRows(ic.ctx, ic.tx, "SELECT id,"+nameColumn+" FROM "+table, func(row *sql.Rows) error {
			var id int64
			var name string
			if err := row.Scan(&id, &name); err != nil {
				return err
			}
			existing[name] = append(existing[name], id)
			return nil
		}); err != nil {
			return err
		}
		for _, item := range identities {
			matches := existing[item.name]
			if len(matches) > 1 || (len(matches) > 0 && nameCounts[item.name] > 1) {
				return invalidConfiguration("ambiguous %s name; use distinct names or replace", table)
			}
			if len(matches) == 1 {
				catalog.ids[item.id] = matches[0]
				ic.result.Skipped++
			}
		}
	}
	ic.catalogs[table] = catalog
	return nil
}

func (ic *configurationImport) prepareCatalogs(mode string) error {
	ic.catalogs = make(map[string]*configurationCatalog)
	c := ic.data
	steps := []func() error{
		func() error {
			return prepareConfigurationCatalog(ic, mode, "monitor_groups", "name", c.Groups, func(g *MonitorGroup) (int64, string) { return g.ID, g.Name }, &ic.result.Groups)
		},
		func() error {
			return prepareConfigurationCatalog(ic, mode, "tags", "name", c.Tags, func(t *Tag) (int64, string) { return t.ID, t.Name }, &ic.result.Tags)
		},
		func() error {
			return prepareConfigurationCatalog(ic, mode, "proxies", "name", c.Proxies, func(p *Proxy) (int64, string) { return p.ID, p.Name }, &ic.result.Proxies)
		},
		func() error {
			return prepareConfigurationCatalog(ic, mode, "notification_channels", "name", c.Channels, func(ch *NotificationChannel) (int64, string) { return ch.ID, ch.Name }, &ic.result.Channels)
		},
		func() error {
			return prepareConfigurationCatalog(ic, mode, "escalation_policies", "name", c.EscalationPolicies, func(p *EscalationPolicy) (int64, string) { return p.ID, p.Name }, &ic.result.Escalations)
		},
		func() error {
			return prepareConfigurationCatalog(ic, mode, "monitors", "name", c.Monitors, func(m *Monitor) (int64, string) { return m.ID, m.Name }, &ic.result.Monitors)
		},
		func() error {
			return prepareConfigurationCatalog(ic, mode, "on_call_rotations", "name", c.Rotations, func(r *OnCallRotation) (int64, string) { return r.ID, r.Name }, &ic.result.Rotations)
		},
		func() error {
			return prepareConfigurationCatalog(ic, mode, "maintenance_windows", "name", c.Maintenance, func(m *MaintenanceWindow) (int64, string) { return m.ID, m.Name }, &ic.result.Maintenance)
		},
		func() error {
			return prepareConfigurationCatalog(ic, mode, "status_pages", "slug", c.StatusPages, func(p *StatusPage) (int64, string) { return p.ID, p.Slug }, &ic.result.StatusPages)
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func (ic *configurationImport) checkReferences() error {
	check := func(table string, ids []int64) error {
		seen := make(map[int64]bool, len(ids))
		for _, id := range ids {
			if _, ok := ic.catalogs[table].ids[id]; !ok {
				return invalidConfiguration("unknown %s reference %d", table, id)
			}
			if seen[id] {
				return invalidConfiguration("duplicate %s relationship %d", table, id)
			}
			seen[id] = true
		}
		return nil
	}
	optional := func(table string, id *int64) error {
		if id == nil {
			return nil
		}
		return check(table, []int64{*id})
	}
	heartbeats := make(map[int64]bool, len(ic.data.HeartbeatGrace))
	for _, m := range ic.data.Monitors {
		for _, ref := range []struct {
			table string
			id    *int64
		}{{"monitor_groups", m.GroupID}, {"proxies", m.ProxyID}, {"escalation_policies", m.EscalationPolicyID}} {
			if err := optional(ref.table, ref.id); err != nil {
				return err
			}
		}
		if err := check("notification_channels", m.NotificationChannelIDs); err != nil {
			return err
		}
		tags := make([]int64, len(m.MonitorTags))
		for i, t := range m.MonitorTags {
			tags[i] = t.TagID
		}
		if err := check("tags", tags); err != nil {
			return err
		}
		seen := make(map[int64]bool, len(m.AgentIDs))
		for _, id := range m.AgentIDs {
			if _, ok := ic.agents[id]; !ok {
				return invalidConfiguration("unknown agent reference %d", id)
			}
			if seen[id] {
				return invalidConfiguration("duplicate agent reference")
			}
			seen[id] = true
		}
		if m.Type == "heartbeat" {
			heartbeats[m.ID] = true
			if grace, ok := ic.data.HeartbeatGrace[m.ID]; !ok || grace < 0 || grace > 86400 {
				return invalidConfiguration("heartbeat %d requires grace between 0 and 86400 seconds", m.ID)
			}
		}
	}
	for id := range ic.data.HeartbeatGrace {
		if !heartbeats[id] {
			return invalidConfiguration("heartbeat configuration references a non-heartbeat monitor")
		}
	}
	for _, p := range ic.data.EscalationPolicies {
		seen := make(map[int]bool, len(p.Steps))
		for _, step := range p.Steps {
			if step == nil || step.StepOrder < 0 || seen[step.StepOrder] {
				return invalidConfiguration("invalid or duplicate escalation step order")
			}
			seen[step.StepOrder] = true
			if err := check("notification_channels", step.NotificationChannelIDs); err != nil {
				return err
			}
		}
	}
	for _, r := range ic.data.Rotations {
		if err := validateRotation(r); err != nil {
			return invalidConfiguration("rotation: %v", err)
		}
		if r.PeriodStartedAt.IsZero() {
			return invalidConfiguration("rotation period_started_at is required")
		}
		if err := check("notification_channels", r.ChannelIDs); err != nil {
			return err
		}
		if err := optional("notification_channels", r.OverrideChannelID); err != nil {
			return err
		}
	}
	for _, mw := range ic.data.Maintenance {
		if err := check("monitors", mw.MonitorIDs); err != nil {
			return err
		}
	}
	seenPageMonitors := make(map[[2]int64]bool, len(ic.data.StatusPageMonitors))
	for _, m := range ic.data.StatusPageMonitors {
		if err := check("status_pages", []int64{m.PageID}); err != nil {
			return err
		}
		if err := check("monitors", []int64{m.MonitorID}); err != nil {
			return err
		}
		pair := [2]int64{m.PageID, m.MonitorID}
		if seenPageMonitors[pair] {
			return invalidConfiguration("duplicate status page monitor")
		}
		seenPageMonitors[pair] = true
	}
	return nil
}

// Prepare every ciphertext and fresh heartbeat credential before the first
// DELETE or INSERT. The writer transaction protects the already checked maps.
func (ic *configurationImport) prepareSecrets(s *SQLiteStore) error {
	ic.monitorSettings = make(map[int64]string, len(ic.data.Monitors))
	ic.channelSettings = make(map[int64]string, len(ic.data.Channels))
	ic.proxyPasswords = make(map[int64]string, len(ic.data.Proxies))
	ic.heartbeatTokens = make(map[int64]string, len(ic.data.HeartbeatGrace))
	for _, m := range ic.data.Monitors {
		if m.Settings == nil {
			m.Settings = json.RawMessage("{}")
		}
		if m.Assertions == nil {
			m.Assertions = json.RawMessage("[]")
		}
		if !json.Valid(m.Settings) || !json.Valid(m.Assertions) {
			return invalidConfiguration("monitor %d has invalid settings or assertions JSON", m.ID)
		}
		value, err := s.encryptSettings(string(m.Settings))
		if err != nil {
			return fmt.Errorf("encrypt imported monitor: %w", err)
		}
		ic.monitorSettings[m.ID] = value
		if m.Type == "heartbeat" && ic.catalogs["monitors"].ids[m.ID] == 0 {
			token, err := generateToken()
			if err != nil {
				return fmt.Errorf("prepare heartbeat credential: %w", err)
			}
			ic.heartbeatTokens[m.ID] = token
		}
	}
	for _, ch := range ic.data.Channels {
		if !json.Valid(ch.Settings) {
			return invalidConfiguration("channel %d has invalid settings JSON", ch.ID)
		}
		value, err := s.encryptSettings(string(ch.Settings))
		if err != nil {
			return fmt.Errorf("encrypt imported channel: %w", err)
		}
		ic.channelSettings[ch.ID] = value
	}
	for _, p := range ic.data.Proxies {
		value, err := s.encryptSettings(p.AuthPass)
		if err != nil {
			return fmt.Errorf("encrypt imported proxy: %w", err)
		}
		ic.proxyPasswords[p.ID] = value
	}
	return nil
}

func (ic *configurationImport) resolveAgents(explicit map[int64]int64) error {
	localByName := make(map[string][]int64)
	localIDs := make(map[int64]bool)
	if err := readConfigurationRows(ic.ctx, ic.tx, "SELECT id,name FROM agents", func(row *sql.Rows) error {
		var id int64
		var name string
		if err := row.Scan(&id, &name); err != nil {
			return err
		}
		localByName[name] = append(localByName[name], id)
		localIDs[id] = true
		return nil
	}); err != nil {
		return err
	}
	source := make(map[int64]string, len(ic.data.Agents))
	used := make(map[int64]bool)
	for _, a := range ic.data.Agents {
		if a.ID <= 0 {
			return invalidConfiguration("agent IDs must be positive")
		}
		if _, ok := source[a.ID]; ok {
			return invalidConfiguration("duplicate agent ID")
		}
		source[a.ID] = a.Name
	}
	for _, m := range ic.data.Monitors {
		for _, id := range m.AgentIDs {
			used[id] = true
		}
	}
	for id, destination := range explicit {
		if _, ok := source[id]; !ok || !localIDs[destination] {
			return invalidConfiguration("invalid explicit agent mapping")
		}
	}
	ic.agents = make(map[int64]int64, len(used))
	destinations := make(map[int64]int64, len(used))
	for id := range used {
		name, ok := source[id]
		if !ok {
			return invalidConfiguration("unknown agent reference %d", id)
		}
		destination, mapped := explicit[id]
		if !mapped {
			matches := localByName[name]
			if len(matches) != 1 {
				return invalidConfiguration("agent reference %d needs an explicit mapping to one enrolled destination agent", id)
			}
			destination = matches[0]
		}
		if other, exists := destinations[destination]; exists && other != id {
			return invalidConfiguration("distinct source agents must map to distinct destination enrollments")
		}
		destinations[destination] = id
		ic.agents[id] = destination
	}
	return nil
}

func cloneConfiguration(c *ConfigurationSnapshot) *ConfigurationSnapshot {
	out := *c
	out.Monitors = cloneConfigurationRecords(c.Monitors, copyMonitorSnapshot)
	out.Groups = cloneConfigurationRecords(c.Groups, nil)
	out.Tags = cloneConfigurationRecords(c.Tags, nil)
	out.Proxies = cloneConfigurationRecords(c.Proxies, nil)
	out.Channels = cloneConfigurationRecords(c.Channels, func(ch *NotificationChannel) *NotificationChannel {
		copy := *ch
		copy.Settings = slices.Clone(ch.Settings)
		copy.Events = slices.Clone(ch.Events)
		return &copy
	})
	out.EscalationPolicies = cloneConfigurationRecords(c.EscalationPolicies, func(p *EscalationPolicy) *EscalationPolicy {
		copy := *p
		copy.Steps = cloneConfigurationRecords(p.Steps, func(s *EscalationPolicyStep) *EscalationPolicyStep {
			copy := *s
			copy.NotificationChannelIDs = slices.Clone(s.NotificationChannelIDs)
			return &copy
		})
		return &copy
	})
	out.Rotations = cloneConfigurationRecords(c.Rotations, func(r *OnCallRotation) *OnCallRotation {
		copy := *r
		copy.ChannelIDs = slices.Clone(r.ChannelIDs)
		if r.OverrideChannelID != nil {
			id := *r.OverrideChannelID
			copy.OverrideChannelID = &id
		}
		if r.OverrideUntil != nil {
			until := *r.OverrideUntil
			copy.OverrideUntil = &until
		}
		return &copy
	})
	out.Maintenance = cloneConfigurationRecords(c.Maintenance, func(m *MaintenanceWindow) *MaintenanceWindow {
		copy := *m
		copy.MonitorIDs = slices.Clone(m.MonitorIDs)
		return &copy
	})
	out.StatusPages = cloneConfigurationRecords(c.StatusPages, nil)
	out.StatusPageMonitors = slices.Clone(c.StatusPageMonitors)
	out.Agents = slices.Clone(c.Agents)
	out.HeartbeatGrace = make(map[int64]int, len(c.HeartbeatGrace))
	for id, grace := range c.HeartbeatGrace {
		out.HeartbeatGrace[id] = grace
	}
	return &out
}

func cloneConfigurationRecords[T any](records []*T, clone func(*T) *T) []*T {
	if records == nil {
		return nil
	}
	out := make([]*T, len(records))
	for i, record := range records {
		if record == nil {
			continue
		}
		if clone != nil {
			out[i] = clone(record)
		} else {
			copy := *record
			out[i] = &copy
		}
	}
	return out
}
