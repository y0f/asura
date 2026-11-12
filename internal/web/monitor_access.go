package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
	"github.com/y0f/asura/v3/internal/web/views"
)

func (h *Handler) requireMonitor(w http.ResponseWriter, r *http.Request, id int64, actions ...string) (*storage.Monitor, bool) {
	monitor, err := h.store.GetMonitor(r.Context(), id, httputil.MonitorAccess(r.Context(), actions...)...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			h.logger.Error("web: load authorized monitor", "error", err)
			http.Error(w, "Failed to load monitor", http.StatusInternalServerError)
		}
		return nil, false
	}
	return monitor, true
}

func monitorRelationFields(r *http.Request) storage.MonitorRelationChanges {
	fields := r.Form["relation_fields"]
	return storage.MonitorRelationChanges{
		Group: r.Form.Has("group_id"), Proxy: r.Form.Has("proxy_id"), EscalationPolicy: r.Form.Has("escalation_policy_id"),
		Notifications: r.Form.Has("notification_channel_ids[]") || slices.Contains(fields, "notifications"),
		Tags:          r.Form.Has("tag_ids[]") || slices.Contains(fields, "tags"),
		Agents:        r.Form.Has("agent_ids[]") || r.Form.Has("agent_enabled") || slices.Contains(fields, "agents"),
	}
}

func validateMonitorRelationForm(r *http.Request) error {
	for _, name := range []string{"group_id", "proxy_id", "escalation_policy_id", "notification_channel_ids[]", "tag_ids[]", "agent_ids[]"} {
		for _, value := range r.Form[name] {
			if value == "" && (name == "group_id" || name == "proxy_id" || name == "escalation_policy_id") {
				continue
			}
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("%s must contain positive IDs", name)
			}
		}
	}
	return nil
}

// Only load metadata needed by visible relationship selectors. Scoped creators
// without groups.read receive their configured destination IDs with neutral labels.
func (h *Handler) loadMonitorFormChoices(r *http.Request, fd *views.MonitorFormParams) error {
	key := httputil.GetAPIKey(r.Context())
	creating := fd.Monitor == nil || fd.Monitor.ID == 0
	canAssign := key.HasCapability("monitors.assign")
	if !creating {
		canAssign = key.AllowsMonitor("monitors.assign", fd.Monitor.ID, fd.Monitor.GroupID)
	}
	var err error
	if creating || canAssign {
		if key.HasPermission("groups.read") {
			fd.Groups, err = h.store.ListMonitorGroups(r.Context())
			if err != nil {
				return err
			}
		} else if creating {
			for _, id := range key.AccessFor("monitors.create").GroupIDs {
				fd.Groups = append(fd.Groups, &storage.MonitorGroup{ID: id, Name: fmt.Sprintf("Group #%d", id)})
			}
		}
	}
	if !canAssign {
		return nil
	}
	if key.HasPermission("notifications.read") {
		fd.NotificationChannels, err = h.store.ListNotificationChannels(r.Context())
		if err != nil {
			return err
		}
	}
	if key.HasPermission("proxies.read") {
		fd.Proxies, err = h.store.ListProxies(r.Context())
		if err != nil {
			return err
		}
	}
	if key.HasPermission("tags.read") {
		fd.AllTags, err = h.store.ListTags(r.Context())
		if err != nil {
			return err
		}
	}
	if key.HasPermission("escalation_policies.read") {
		fd.EscalationPolicies, err = h.store.ListEscalationPolicies(r.Context())
		if err != nil {
			return err
		}
	}
	if key.HasPermission("agents.read") {
		fd.Agents, err = h.store.ListAgents(r.Context())
		if err != nil {
			return err
		}
	}
	return nil
}

// The POST repeats these checks against its transactional source. This preview
// only controls whether the detail page can offer a useful clone action.
func (h *Handler) monitorCloneAllowed(r *http.Request, monitor *storage.Monitor) bool {
	key := httputil.GetAPIKey(r.Context())
	if !key.AllowsMonitor("monitors.read", monitor.ID, monitor.GroupID) || !key.AllowsMonitor("monitors.clone", monitor.ID, monitor.GroupID) || !key.AllowsMonitor("monitors.clone", 0, monitor.GroupID) {
		return false
	}
	source := *monitor
	var err error
	if source.NotificationChannelIDs, err = h.store.GetMonitorNotificationChannelIDs(r.Context(), source.ID, httputil.MonitorAccess(r.Context(), "monitors.read", "monitors.clone")...); err != nil {
		return false
	}
	if source.MonitorTags, err = h.store.GetMonitorTags(r.Context(), source.ID, httputil.MonitorAccess(r.Context(), "monitors.read", "monitors.clone")...); err != nil {
		return false
	}
	if source.AgentIDs, err = h.store.GetMonitorAgentIDs(r.Context(), source.ID, httputil.MonitorAccess(r.Context(), "monitors.read", "monitors.clone")...); err != nil {
		return false
	}
	_, _, err = httputil.PrepareMonitorClone(key, &source, "")
	return err == nil
}

func (h *Handler) monitorReadError(w http.ResponseWriter, r *http.Request, err error, message string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
	} else {
		h.logger.Error(message, "error", err)
		http.Error(w, message, http.StatusInternalServerError)
	}
	return true
}
