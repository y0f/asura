package api

import (
	"encoding/json"
	"fmt"

	"github.com/y0f/asura/internal/storage"
)

// v1 used names for most relationships and omitted monitor IDs entirely. Give
// it file-local identities once, rejecting ambiguity instead of guessing.
func legacyConfigurationSnapshot(file *ExportData) (*storage.ConfigurationSnapshot, error) {
	c := &storage.ConfigurationSnapshot{HeartbeatGrace: make(map[int64]int)}
	groups, proxies, channels, monitors, tags, agents := map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]int64{}
	addName := func(index map[string]int64, name, kind string) (int64, error) {
		if _, exists := index[name]; exists {
			return 0, fmt.Errorf("v1 contains ambiguous %s names; export v2 from the source instead", kind)
		}
		id := int64(len(index) + 1)
		index[name] = id
		return id, nil
	}
	lookup := func(index map[string]int64, name, kind string) (int64, error) {
		id, ok := index[name]
		if !ok {
			return 0, fmt.Errorf("v1 references an unknown %s name", kind)
		}
		return id, nil
	}
	for _, g := range file.MonitorGroups {
		if g == nil {
			return nil, fmt.Errorf("v1 group must not be null")
		}
		id, err := addName(groups, g.Name, "group")
		if err != nil {
			return nil, err
		}
		c.Groups = append(c.Groups, &storage.MonitorGroup{ID: id, Name: g.Name, SortOrder: g.SortOrder})
	}
	for _, p := range file.Proxies {
		id, err := addName(proxies, p.Name, "proxy")
		if err != nil {
			return nil, err
		}
		c.Proxies = append(c.Proxies, &storage.Proxy{ID: id, Name: p.Name, Protocol: p.Protocol, Host: p.Host, Port: p.Port, AuthUser: p.AuthUser, AuthPass: p.AuthPass, Enabled: p.Enabled})
	}
	for _, ch := range file.NotificationChannels {
		if ch == nil {
			return nil, fmt.Errorf("v1 notification channel must not be null")
		}
		id, err := addName(channels, ch.Name, "notification channel")
		if err != nil {
			return nil, err
		}
		c.Channels = append(c.Channels, &storage.NotificationChannel{ID: id, Name: ch.Name, Type: ch.Type, Enabled: ch.Enabled, Settings: ch.Settings, Events: ch.Events})
	}
	for _, m := range file.Monitors {
		id, err := addName(monitors, m.Name, "monitor")
		if err != nil {
			return nil, err
		}
		out := &storage.Monitor{ID: id, Name: m.Name, Description: m.Description, Type: m.Type, Target: m.Target, Interval: m.Interval, Timeout: m.Timeout, Enabled: m.Enabled, Settings: m.Settings, Assertions: m.Assertions, TrackChanges: m.TrackChanges, FailureThreshold: m.FailureThreshold, SuccessThreshold: m.SuccessThreshold, UpsideDown: m.UpsideDown, ResendInterval: m.ResendInterval, SLATarget: m.SLATarget, AgentEnabled: m.AgentEnabled}
		if m.GroupName != "" {
			id, err := lookup(groups, m.GroupName, "group")
			if err != nil {
				return nil, err
			}
			out.GroupID = &id
		}
		if m.ProxyName != "" {
			id, err := lookup(proxies, m.ProxyName, "proxy")
			if err != nil {
				return nil, err
			}
			out.ProxyID = &id
		}
		for _, name := range m.NotificationChannelNames {
			id, err := lookup(channels, name, "notification channel")
			if err != nil {
				return nil, err
			}
			out.NotificationChannelIDs = append(out.NotificationChannelIDs, id)
		}
		for _, name := range m.AgentNames {
			id, exists := agents[name]
			if !exists {
				id = int64(len(agents) + 1)
				agents[name] = id
				c.Agents = append(c.Agents, storage.ConfigurationAgent{ID: id, Name: name})
			}
			out.AgentIDs = append(out.AgentIDs, id)
		}
		for _, tag := range m.Tags {
			id, exists := tags[tag.TagName]
			color := tag.Color
			if color == "" {
				color = "#808080"
			}
			if !exists {
				id = int64(len(tags) + 1)
				tags[tag.TagName] = id
				c.Tags = append(c.Tags, &storage.Tag{ID: id, Name: tag.TagName, Color: color})
			} else if c.Tags[id-1].Color != color {
				return nil, fmt.Errorf("v1 contains conflicting tag definitions")
			}
			out.MonitorTags = append(out.MonitorTags, storage.MonitorTag{TagID: id, Value: tag.Value})
		}
		if m.Type == "heartbeat" {
			var settings struct {
				Grace int `json:"grace"`
			}
			if len(m.Settings) > 0 && json.Unmarshal(m.Settings, &settings) != nil {
				return nil, fmt.Errorf("v1 heartbeat settings are invalid")
			}
			c.HeartbeatGrace[id] = settings.Grace
		}
		c.Monitors = append(c.Monitors, out)
	}
	for i, mw := range file.MaintenanceWindows {
		if mw == nil {
			return nil, fmt.Errorf("v1 maintenance window must not be null")
		}
		if len(mw.MonitorIDs) != 0 {
			return nil, fmt.Errorf("v1 maintenance monitor IDs cannot be mapped safely because v1 omitted monitor identities; export v2 from the source or restore the full database backup")
		}
		c.Maintenance = append(c.Maintenance, &storage.MaintenanceWindow{ID: int64(i + 1), Name: mw.Name, StartTime: mw.StartTime, EndTime: mw.EndTime, Recurring: mw.Recurring, CronExpr: mw.CronExpr, Active: mw.Active})
	}
	for i, p := range file.StatusPages {
		id := int64(i + 1)
		c.StatusPages = append(c.StatusPages, &storage.StatusPage{ID: id, Slug: p.Slug, Title: p.Title, Description: p.Description, CustomCSS: p.CustomCSS, ShowIncidents: p.ShowIncidents, Enabled: p.Enabled, APIEnabled: p.APIEnabled, SortOrder: p.SortOrder})
		for _, relation := range p.Monitors {
			monitorID, err := lookup(monitors, relation.MonitorName, "status page monitor")
			if err != nil {
				return nil, err
			}
			c.StatusPageMonitors = append(c.StatusPageMonitors, storage.StatusPageMonitor{PageID: id, MonitorID: monitorID, SortOrder: relation.SortOrder, GroupName: relation.GroupName})
		}
	}
	return c, nil
}
