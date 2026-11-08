package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/y0f/asura/internal/checker"
	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/redact"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

// monitorAlias avoids JSON recursion when wrapping storage.Monitor in a DTO.
type monitorAlias storage.Monitor

// monitorCreateRequest decodes a monitor plus an optional enabled flag so an
// explicit enabled=false is honored and an absent field defaults to true.
type monitorCreateRequest struct {
	monitorAlias
	Enabled *bool `json:"enabled"`
}

type monitorUpdateRequest struct {
	monitorAlias
	Enabled                      *bool    `json:"enabled"`
	ClearSecrets                 []string `json:"clear_secrets"`
	fields                       storage.MonitorRelationChanges
	agentIDsSet, agentEnabledSet bool
}

func (req *monitorUpdateRequest) UnmarshalJSON(data []byte) error {
	type plain monitorUpdateRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode((*plain)(req)); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	req.fields = storage.MonitorRelationChanges{
		Group: fields["group_id"] != nil, Proxy: fields["proxy_id"] != nil,
		EscalationPolicy: fields["escalation_policy_id"] != nil,
		Notifications:    fields["notification_channel_ids"] != nil, Tags: fields["monitor_tags"] != nil,
		Agents: fields["agent_ids"] != nil || fields["agent_enabled"] != nil, Enabled: req.Enabled != nil,
	}
	req.agentIDsSet, req.agentEnabledSet = fields["agent_ids"] != nil, fields["agent_enabled"] != nil
	return nil
}

func (h *Handler) ListMonitors(w http.ResponseWriter, r *http.Request) {
	p := httputil.ParsePagination(r)
	result, err := h.store.ListMonitors(r.Context(), storage.MonitorListFilter{Access: httputil.MonitorAccess(r.Context(), "monitors.read")}, p)
	if err != nil {
		h.logger.Error("list monitors", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list monitors")
		return
	}
	if monitors, ok := result.Data.([]*storage.Monitor); ok {
		for i, monitor := range monitors {
			monitors[i] = monitorReadResponse(r, monitor)
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m, err := h.store.GetMonitor(r.Context(), id, httputil.MonitorAccess(r.Context(), "monitors.read")...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		h.logger.Error("get monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get monitor")
		return
	}

	if m.NotificationChannelIDs, err = h.store.GetMonitorNotificationChannelIDs(r.Context(), m.ID, httputil.MonitorAccess(r.Context(), "monitors.read")...); err != nil {
		if monitorMutationError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load monitor assignments")
		return
	}
	if m.MonitorTags, err = h.store.GetMonitorTags(r.Context(), m.ID, httputil.MonitorAccess(r.Context(), "monitors.read")...); err != nil {
		if monitorMutationError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load monitor assignments")
		return
	}
	if m.AgentIDs, err = h.store.GetMonitorAgentIDs(r.Context(), m.ID, httputil.MonitorAccess(r.Context(), "monitors.read")...); err != nil {
		if monitorMutationError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load monitor assignments")
		return
	}

	if m.Type == "heartbeat" {
		hb, err := h.store.GetHeartbeatByMonitorID(r.Context(), m.ID, httputil.MonitorAccess(r.Context(), "monitors.read")...)
		if err != nil {
			if monitorMutationError(w, err) {
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load heartbeat")
			return
		}
		if err == nil && hb != nil {
			// Storage returns a credential hash, never the original bearer token.
			hb.Token = ""
			writeJSON(w, http.StatusOK, map[string]any{
				"monitor":   monitorReadResponse(r, m),
				"heartbeat": hb,
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, monitorReadResponse(r, m))
}

func (h *Handler) CreateMonitor(w http.ResponseWriter, r *http.Request) {
	req := monitorCreateRequest{}
	if err := h.readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m := storage.Monitor(req.monitorAlias)

	applyMonitorDefaults(&m, h.cfg.Monitor)
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	} else {
		m.Enabled = true
	}

	if _, err := httputil.PrepareMonitorRelations(httputil.GetAPIKey(r.Context()), &m, nil, storage.MonitorRelationChanges{Group: true, Proxy: true, EscalationPolicy: true, Notifications: true, Tags: true, Agents: true}); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, httputil.ErrMonitorRelationForbidden) {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
		return
	}
	if err := validate.ValidateMonitor(&m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	options, err := httputil.MonitorCreateOptions(&m)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.CreateMonitorWithRelations(r.Context(), &m, options, httputil.MonitorAccess(r.Context(), "monitors.create")...); err != nil {
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("create monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create monitor")
		return
	}

	h.audit(r, "create", "monitor", m.ID, "")

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	if options.Heartbeat != nil {
		writeJSON(w, http.StatusCreated, map[string]any{
			"monitor":   monitorResponse(r, &m),
			"heartbeat": options.Heartbeat,
		})
		return
	}
	writeJSON(w, http.StatusCreated, monitorResponse(r, &m))
}

func (h *Handler) UpdateMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req monitorUpdateRequest
	if err := h.readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key := httputil.GetAPIKey(r.Context())
	var inputErr error
	m, err := h.store.UpdateMonitorWithRelations(r.Context(), id, func(current *storage.Monitor) (*storage.Monitor, storage.MonitorRelationChanges, error) {
		candidate := storage.Monitor(req.monitorAlias)
		candidate.ID = id
		if !req.agentIDsSet {
			candidate.AgentIDs = current.AgentIDs
		}
		if !req.agentEnabledSet {
			candidate.AgentEnabled = current.AgentEnabled && (!req.agentIDsSet || len(candidate.AgentIDs) > 0)
		}
		if req.Enabled != nil {
			candidate.Enabled = *req.Enabled
		}
		changes, err := httputil.PrepareMonitorRelations(key, &candidate, current, req.fields)
		if err == nil && candidate.Type == current.Type {
			clear := secretClearSet(req.ClearSecrets)
			if key == nil || !key.SuperAdmin {
				err = redact.CheckMonitorDestination(&candidate, current, redact.MonitorSecretKeys[candidate.Type], clear)
			}
			if err == nil {
				candidate.Settings, err = redact.MergeSecrets(candidate.Settings, current.Settings, redact.MonitorSecretKeys[candidate.Type], clear)
				if err == nil {
					candidate.Settings, err = redact.MergeMonitorHeaders(candidate.Settings, current.Settings, clear)
				}
			}
		}
		if err == nil {
			err = validate.ValidateMonitor(&candidate)
		}
		inputErr = err
		return &candidate, changes, err
	}, httputil.MonitorAccess(r.Context(), "monitors.update")...)
	if err != nil {
		if monitorMutationError(w, err) {
			return
		}
		if inputErr != nil {
			status := http.StatusBadRequest
			if errors.Is(inputErr, httputil.ErrMonitorRelationForbidden) {
				status = http.StatusForbidden
			}
			writeError(w, status, inputErr.Error())
			return
		}
		h.logger.Error("update monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update monitor")
		return
	}
	checker.ClearOAuth2TokenCache(m.ID)

	h.audit(r, "update", "monitor", m.ID, "")

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	writeJSON(w, http.StatusOK, monitorResponse(r, m))
}

func (h *Handler) DeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.DeleteMonitor(r.Context(), id, httputil.MonitorAccess(r.Context(), "monitors.delete")...); err != nil {
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("delete monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete monitor")
		return
	}

	h.audit(r, "delete", "monitor", id, "")

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) PauseMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.SetMonitorEnabled(r.Context(), id, false, httputil.MonitorAccess(r.Context(), "monitors.pause")...); err != nil {
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("pause monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to pause monitor")
		return
	}
	h.audit(r, "pause", "monitor", id, "")
	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (h *Handler) ResumeMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.SetMonitorEnabled(r.Context(), id, true, httputil.MonitorAccess(r.Context(), "monitors.resume")...); err != nil {
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("resume monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to resume monitor")
		return
	}
	h.audit(r, "resume", "monitor", id, "")
	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

func (h *Handler) ListChecks(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := h.requireMonitor(w, r, id, "monitors.read"); !ok {
		return
	}
	p := httputil.ParsePagination(r)
	result, err := h.store.ListCheckResults(r.Context(), id, p, httputil.MonitorAccess(r.Context(), "monitors.read")...)
	if err != nil {
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("list checks", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list checks")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) ListChanges(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := h.requireMonitor(w, r, id, "monitors.read"); !ok {
		return
	}
	p := httputil.ParsePagination(r)
	result, err := h.store.ListContentChanges(r.Context(), id, p, httputil.MonitorAccess(r.Context(), "monitors.read")...)
	if err != nil {
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("list changes", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list changes")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) CloneMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	seed, err := httputil.MonitorCreateOptions(&storage.Monitor{Type: "heartbeat"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to prepare monitor clone")
		return
	}
	var inputErr error
	clone, heartbeat, err := h.store.CloneMonitorWithRelations(ctx, id, func(source *storage.Monitor) (*storage.Monitor, storage.MonitorCreateOptions, error) {
		clone, options, err := httputil.PrepareMonitorClone(httputil.GetAPIKey(ctx), source, seed.Heartbeat.Token)
		inputErr = err
		return clone, options, err
	}, httputil.MonitorAccess(ctx, "monitors.read", "monitors.clone"), httputil.MonitorAccess(ctx, "monitors.clone"))
	if err != nil {
		if monitorMutationError(w, err) {
			return
		}
		if inputErr != nil {
			status := http.StatusBadRequest
			if errors.Is(inputErr, httputil.ErrMonitorRelationForbidden) {
				status = http.StatusForbidden
			}
			writeError(w, status, inputErr.Error())
			return
		}
		h.logger.Error("clone monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to clone monitor")
		return
	}

	h.audit(r, "clone", "monitor", clone.ID, fmt.Sprintf("from=%d", id))

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	if heartbeat != nil {
		writeJSON(w, http.StatusCreated, map[string]any{"monitor": monitorResponse(r, clone), "heartbeat": heartbeat})
		return
	}
	writeJSON(w, http.StatusCreated, monitorResponse(r, clone))
}

type bulkRequest struct {
	Action  string  `json:"action"`
	IDs     []int64 `json:"ids"`
	GroupID *int64  `json:"group_id,omitempty"`
}

func (h *Handler) BulkMonitors(w http.ResponseWriter, r *http.Request) {
	var req bulkRequest
	if err := h.readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scopes := httputil.MonitorBulkPermissions(req.Action)
	if scopes == nil {
		writeError(w, http.StatusBadRequest, "action must be one of: pause, resume, delete, set_group")
		return
	}
	if !httputil.GetAPIKey(r.Context()).HasAllCapabilities(scopes...) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids is required")
		return
	}
	if len(req.IDs) > 500 {
		writeError(w, http.StatusBadRequest, "max 500 monitors per request")
		return
	}
	seen := make(map[int64]bool, len(req.IDs))
	for _, id := range req.IDs {
		if id <= 0 || seen[id] {
			writeError(w, http.StatusBadRequest, "ids must contain unique positive monitor IDs")
			return
		}
		seen[id] = true
	}
	if req.Action == "set_group" && req.GroupID != nil && *req.GroupID <= 0 {
		writeError(w, http.StatusBadRequest, "group_id must be positive or null")
		return
	}

	ctx := r.Context()
	var affected int64
	var err error

	switch req.Action {
	case "pause":
		affected, err = h.store.BulkSetMonitorsEnabled(ctx, req.IDs, false, httputil.MonitorAccess(ctx, "monitors.pause")...)
	case "resume":
		affected, err = h.store.BulkSetMonitorsEnabled(ctx, req.IDs, true, httputil.MonitorAccess(ctx, "monitors.resume")...)
	case "delete":
		affected, err = h.store.BulkDeleteMonitors(ctx, req.IDs, httputil.MonitorAccess(ctx, "monitors.delete")...)
	case "set_group":
		affected, err = h.store.BulkSetMonitorGroup(ctx, req.IDs, req.GroupID, httputil.MonitorAccess(ctx, "monitors.update", "monitors.assign")...)
	}

	if err != nil {
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("bulk monitors", "action", req.Action, "error", err)
		writeError(w, http.StatusInternalServerError, "bulk operation failed")
		return
	}

	h.audit(r, "bulk_"+req.Action, "monitor", 0, fmt.Sprintf("ids=%v", req.IDs))

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   req.Action,
		"affected": affected,
	})
}

func applyMonitorDefaults(m *storage.Monitor, cfg config.MonitorConfig) {
	if m.Interval == 0 {
		m.Interval = int(cfg.DefaultInterval.Seconds())
	}
	if m.Timeout == 0 {
		m.Timeout = int(cfg.DefaultTimeout.Seconds())
	}
	if m.FailureThreshold == 0 {
		m.FailureThreshold = cfg.FailureThreshold
	}
	if m.SuccessThreshold == 0 {
		m.SuccessThreshold = cfg.SuccessThreshold
	}
	if m.Type == "heartbeat" && m.Target == "" {
		m.Target = "heartbeat"
	}
	if m.Type == "manual" && m.Target == "" {
		m.Target = "manual"
	}
}
