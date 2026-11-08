package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ApplyConfiguration validates and replaces or merges the entire portable
// configuration in one writer transaction. No result, remapped ID or fresh
// credential escapes on failure. The input remains unchanged on either path.
func (s *SQLiteStore) ApplyConfiguration(ctx context.Context, data *ConfigurationSnapshot, options ConfigurationImportOptions) (*ConfigurationImportResult, error) {
	if data == nil || options.Validate == nil {
		return nil, invalidConfiguration("configuration and typed validation are required")
	}
	if options.Mode != "merge" && options.Mode != "replace" {
		return nil, invalidConfiguration("mode must be merge or replace")
	}
	ic := &configurationImport{ctx: ctx, data: cloneConfiguration(data)}
	if err := checkConfigurationShape(ic.data); err != nil {
		return nil, err
	}
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ic.tx = tx
	if err := options.Validate(ic.data); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConfiguration, err)
	}
	if err := checkConfigurationShape(ic.data); err != nil {
		return nil, err
	}
	// Validation may normalize a private candidate. Build every identity map
	// afterwards, so no stale ID/name mapping can survive that callback.
	if err := ic.prepareCatalogs(options.Mode); err != nil {
		return nil, err
	}
	if err := ic.resolveAgents(options.AgentIDs); err != nil {
		return nil, err
	}
	if err := ic.checkReferences(); err != nil {
		return nil, err
	}
	if err := ic.prepareSecrets(s); err != nil {
		return nil, err
	}
	if options.Mode == "replace" {
		// These are precisely the portable entities. Cascades deliberately remove
		// monitor history and status-page subscriptions; enrollments, auth/session
		// state, audit data and runtime YAML are outside this operation.
		for _, table := range []string{"status_pages", "maintenance_windows", "on_call_rotations", "monitors", "escalation_policies", "notification_channels", "proxies", "tags", "monitor_groups"} {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
				return nil, fmt.Errorf("replace %s: %w", table, err)
			}
		}
	}
	if err := ic.write(s); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ic.result, nil
}

func (ic *configurationImport) insert(table string, sourceID int64, query string, args ...any) error {
	res, err := ic.tx.ExecContext(ic.ctx, query, args...)
	if err != nil {
		return fmt.Errorf("import %s: %w", table, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	catalog := ic.catalogs[table]
	catalog.ids[sourceID] = id
	*catalog.created++
	return nil
}

func (ic *configurationImport) mapIDs(table string, source []int64) []int64 {
	ids := make([]int64, len(source))
	for i, id := range source {
		ids[i] = ic.catalogs[table].ids[id]
	}
	return ids
}

func (ic *configurationImport) mapOptional(table string, source *int64) *int64 {
	if source == nil {
		return nil
	}
	id := ic.catalogs[table].ids[*source]
	return &id
}

// Only concrete []int64 and []string values use this helper; neither can fail
// JSON encoding. Raw settings are validated and encrypted in prepareSecrets.
func configurationJSON[T int64 | string](values []T) string {
	data, _ := json.Marshal(values)
	return string(data)
}

func (ic *configurationImport) write(s *SQLiteStore) error {
	now := formatTime(time.Now().UTC())
	for _, g := range ic.data.Groups {
		if ic.catalogs["monitor_groups"].ids[g.ID] != 0 {
			continue
		}
		if err := ic.insert("monitor_groups", g.ID, "INSERT INTO monitor_groups (name,sort_order,created_at,updated_at) VALUES (?,?,?,?)", g.Name, g.SortOrder, now, now); err != nil {
			return err
		}
	}
	for _, t := range ic.data.Tags {
		if ic.catalogs["tags"].ids[t.ID] != 0 {
			continue
		}
		if err := ic.insert("tags", t.ID, "INSERT INTO tags (name,color,created_at) VALUES (?,?,?)", t.Name, t.Color, now); err != nil {
			return err
		}
	}
	for _, p := range ic.data.Proxies {
		if ic.catalogs["proxies"].ids[p.ID] != 0 {
			continue
		}
		if err := ic.insert("proxies", p.ID, "INSERT INTO proxies (name,protocol,host,port,auth_user,auth_pass,enabled,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)", p.Name, p.Protocol, p.Host, p.Port, p.AuthUser, ic.proxyPasswords[p.ID], p.Enabled, now, now); err != nil {
			return err
		}
	}
	for _, ch := range ic.data.Channels {
		if ic.catalogs["notification_channels"].ids[ch.ID] != 0 {
			continue
		}
		if err := ic.insert("notification_channels", ch.ID, "INSERT INTO notification_channels (name,type,enabled,settings,events,created_at,updated_at) VALUES (?,?,?,?,?,?,?)", ch.Name, ch.Type, ch.Enabled, ic.channelSettings[ch.ID], configurationJSON(ch.Events), now, now); err != nil {
			return err
		}
	}
	for _, p := range ic.data.EscalationPolicies {
		if ic.catalogs["escalation_policies"].ids[p.ID] != 0 {
			continue
		}
		if err := ic.insert("escalation_policies", p.ID, "INSERT INTO escalation_policies (name,description,enabled,repeat,created_at,updated_at) VALUES (?,?,?,?,?,?)", p.Name, p.Description, p.Enabled, p.Repeat, now, now); err != nil {
			return err
		}
		for _, step := range p.Steps {
			if _, err := ic.tx.ExecContext(ic.ctx, "INSERT INTO escalation_policy_steps (policy_id,step_order,delay_minutes,notification_channel_ids) VALUES (?,?,?,?)", ic.catalogs["escalation_policies"].ids[p.ID], step.StepOrder, step.DelayMinutes, configurationJSON(ic.mapIDs("notification_channels", step.NotificationChannelIDs))); err != nil {
				return fmt.Errorf("import escalation step: %w", err)
			}
		}
	}
	for _, m := range ic.data.Monitors {
		sourceID := m.ID
		if ic.catalogs["monitors"].ids[sourceID] != 0 {
			continue
		}
		m.GroupID = ic.mapOptional("monitor_groups", m.GroupID)
		m.ProxyID = ic.mapOptional("proxies", m.ProxyID)
		m.EscalationPolicyID = ic.mapOptional("escalation_policies", m.EscalationPolicyID)
		m.NotificationChannelIDs = ic.mapIDs("notification_channels", m.NotificationChannelIDs)
		for i := range m.MonitorTags {
			m.MonitorTags[i].TagID = ic.catalogs["tags"].ids[m.MonitorTags[i].TagID]
		}
		for i := range m.AgentIDs {
			m.AgentIDs[i] = ic.agents[m.AgentIDs[i]]
		}
		options := MonitorCreateOptions{}
		if token, ok := ic.heartbeatTokens[sourceID]; ok {
			options.Heartbeat = &Heartbeat{Token: token, Grace: ic.data.HeartbeatGrace[sourceID]}
		}
		created, heartbeat, err := s.insertPreparedMonitor(ic.ctx, ic.tx, m, ic.monitorSettings[sourceID], &options)
		if err != nil {
			return fmt.Errorf("import monitor: %w", err)
		}
		ic.catalogs["monitors"].ids[sourceID] = created.ID
		ic.result.Monitors++
		if heartbeat != nil {
			ic.result.Heartbeats = append(ic.result.Heartbeats, ImportedHeartbeat{MonitorID: created.ID, MonitorName: created.Name, Token: heartbeat.Token})
		}
	}
	for _, r := range ic.data.Rotations {
		if ic.catalogs["on_call_rotations"].ids[r.ID] != 0 {
			continue
		}
		if err := ic.insert("on_call_rotations", r.ID, `INSERT INTO on_call_rotations (name,channel_ids,period,current_index,period_started_at,override_channel_id,override_until,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`, r.Name, configurationJSON(ic.mapIDs("notification_channels", r.ChannelIDs)), r.Period, r.CurrentIndex, formatTime(r.PeriodStartedAt), ic.mapOptional("notification_channels", r.OverrideChannelID), rotationOverrideUntil(r), now, now); err != nil {
			return err
		}
	}
	for _, m := range ic.data.Maintenance {
		if ic.catalogs["maintenance_windows"].ids[m.ID] != 0 {
			continue
		}
		if err := ic.insert("maintenance_windows", m.ID, `INSERT INTO maintenance_windows (name,monitor_ids,start_time,end_time,recurring,cron_expr,active,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?)`, m.Name, configurationJSON(ic.mapIDs("monitors", m.MonitorIDs)), formatTime(m.StartTime), formatTime(m.EndTime), m.Recurring, m.CronExpr, m.Active, now, now); err != nil {
			return err
		}
	}
	newPages := make(map[int64]bool)
	for _, p := range ic.data.StatusPages {
		if ic.catalogs["status_pages"].ids[p.ID] != 0 {
			continue
		}
		if err := ic.insert("status_pages", p.ID, `INSERT INTO status_pages (slug,title,description,custom_css,show_incidents,enabled,api_enabled,sort_order,logo_url,favicon_url,custom_header_html,password_hash,analytics_script,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, p.Slug, p.Title, p.Description, p.CustomCSS, p.ShowIncidents, p.Enabled, p.APIEnabled, p.SortOrder, p.LogoURL, p.FaviconURL, p.CustomHeaderHTML, p.PasswordHash, p.AnalyticsScript, now, now); err != nil {
			return err
		}
		newPages[p.ID] = true
	}
	for _, m := range ic.data.StatusPageMonitors {
		if !newPages[m.PageID] {
			continue
		}
		if _, err := ic.tx.ExecContext(ic.ctx, "INSERT INTO status_page_monitors (page_id,monitor_id,sort_order,group_name) VALUES (?,?,?,?)", ic.catalogs["status_pages"].ids[m.PageID], ic.catalogs["monitors"].ids[m.MonitorID], m.SortOrder, m.GroupName); err != nil {
			return fmt.Errorf("import status page monitor: %w", err)
		}
	}
	return nil
}
