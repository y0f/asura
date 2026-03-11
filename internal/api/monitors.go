package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

func (h *Handler) ListMonitors(w http.ResponseWriter, r *http.Request) {
	p := httputil.ParsePagination(r)
	result, err := h.store.ListMonitors(r.Context(), storage.MonitorListFilter{}, p)
	if err != nil {
		h.logger.Error("list monitors", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list monitors")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m, err := h.store.GetMonitor(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		h.logger.Error("get monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get monitor")
		return
	}

	channelIDs, _ := h.store.GetMonitorNotificationChannelIDs(r.Context(), m.ID)
	if channelIDs != nil {
		m.NotificationChannelIDs = channelIDs
	}

	m.MonitorTags, _ = h.store.GetMonitorTags(r.Context(), m.ID)

	if m.Type == "heartbeat" {
		hb, err := h.store.GetHeartbeatByMonitorID(r.Context(), m.ID)
		if err == nil && hb != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"monitor":   m,
				"heartbeat": hb,
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, m)
}

func (h *Handler) CreateMonitor(w http.ResponseWriter, r *http.Request) {
	var m storage.Monitor
	if err := readJSON(r, &m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	applyMonitorDefaults(&m, h.cfg.Monitor)

	if err := validate.ValidateMonitor(&m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.CreateMonitor(r.Context(), &m); err != nil {
		h.logger.Error("create monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create monitor")
		return
	}

	if len(m.NotificationChannelIDs) > 0 {
		if err := h.store.SetMonitorNotificationChannels(r.Context(), m.ID, m.NotificationChannelIDs); err != nil {
			h.logger.Error("set monitor notification channels", "error", err)
		}
	}

	if len(m.MonitorTags) > 0 {
		if err := validate.ValidateMonitorTags(m.MonitorTags); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.store.SetMonitorTags(r.Context(), m.ID, m.MonitorTags); err != nil {
			h.logger.Error("set monitor tags", "error", err)
		}
	}

	var heartbeat *storage.Heartbeat
	if m.Type == "heartbeat" {
		var err error
		heartbeat, err = h.createHeartbeat(r.Context(), &m)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	h.audit(r, "create", "monitor", m.ID, "")

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	if heartbeat != nil {
		writeJSON(w, http.StatusCreated, map[string]any{
			"monitor":   m,
			"heartbeat": heartbeat,
		})
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (h *Handler) UpdateMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing, err := h.store.GetMonitor(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		h.logger.Error("get monitor for update", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get monitor")
		return
	}

	var m storage.Monitor
	if err := readJSON(r, &m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	m.ID = existing.ID
	m.CreatedAt = existing.CreatedAt

	if err := validate.ValidateMonitor(&m); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.UpdateMonitor(r.Context(), &m); err != nil {
		h.logger.Error("update monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update monitor")
		return
	}

	if err := h.store.SetMonitorNotificationChannels(r.Context(), m.ID, m.NotificationChannelIDs); err != nil {
		h.logger.Error("set monitor notification channels", "error", err)
	}

	if err := validate.ValidateMonitorTags(m.MonitorTags); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.SetMonitorTags(r.Context(), m.ID, m.MonitorTags); err != nil {
		h.logger.Error("set monitor tags", "error", err)
	}

	h.audit(r, "update", "monitor", m.ID, "")

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	updated, _ := h.store.GetMonitor(r.Context(), id)
	if updated != nil {
		writeJSON(w, http.StatusOK, updated)
	} else {
		writeJSON(w, http.StatusOK, m)
	}
}

func (h *Handler) DeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_, err = h.store.GetMonitor(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		h.logger.Error("get monitor for delete", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get monitor")
		return
	}

	if err := h.store.DeleteMonitor(r.Context(), id); err != nil {
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
	if err := h.store.SetMonitorEnabled(r.Context(), id, false); err != nil {
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
	if err := h.store.SetMonitorEnabled(r.Context(), id, true); err != nil {
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
	p := httputil.ParsePagination(r)
	result, err := h.store.ListCheckResults(r.Context(), id, p)
	if err != nil {
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
	p := httputil.ParsePagination(r)
	result, err := h.store.ListContentChanges(r.Context(), id, p)
	if err != nil {
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
	src, err := h.store.GetMonitor(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "monitor not found")
			return
		}
		h.logger.Error("get monitor for clone", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get monitor")
		return
	}

	clone := &storage.Monitor{
		Name:             src.Name + " (copy)",
		Description:      src.Description,
		Type:             src.Type,
		Target:           src.Target,
		Interval:         src.Interval,
		Timeout:          src.Timeout,
		Enabled:          false,
		Settings:         src.Settings,
		Assertions:       src.Assertions,
		TrackChanges:     src.TrackChanges,
		FailureThreshold: src.FailureThreshold,
		SuccessThreshold: src.SuccessThreshold,
		UpsideDown:       src.UpsideDown,
		ResendInterval:   src.ResendInterval,
		SLATarget:        src.SLATarget,
		GroupID:          src.GroupID,
		ProxyID:          src.ProxyID,
	}

	if err := h.store.CreateMonitor(ctx, clone); err != nil {
		h.logger.Error("clone monitor", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to clone monitor")
		return
	}

	channelIDs, _ := h.store.GetMonitorNotificationChannelIDs(ctx, id)
	if len(channelIDs) > 0 {
		h.store.SetMonitorNotificationChannels(ctx, clone.ID, channelIDs)
	}

	srcTags, _ := h.store.GetMonitorTags(ctx, id)
	if len(srcTags) > 0 {
		h.store.SetMonitorTags(ctx, clone.ID, srcTags)
	}

	if clone.Type == "heartbeat" {
		h.createHeartbeat(ctx, clone)
	}

	h.audit(r, "clone", "monitor", clone.ID, fmt.Sprintf("from=%d", id))

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	writeJSON(w, http.StatusCreated, clone)
}

type bulkRequest struct {
	Action  string  `json:"action"`
	IDs     []int64 `json:"ids"`
	GroupID *int64  `json:"group_id,omitempty"`
}

func (h *Handler) BulkMonitors(w http.ResponseWriter, r *http.Request) {
	var req bulkRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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

	ctx := r.Context()
	var affected int64
	var err error

	switch req.Action {
	case "pause":
		affected, err = h.store.BulkSetMonitorsEnabled(ctx, req.IDs, false)
	case "resume":
		affected, err = h.store.BulkSetMonitorsEnabled(ctx, req.IDs, true)
	case "delete":
		affected, err = h.store.BulkDeleteMonitors(ctx, req.IDs)
	case "set_group":
		affected, err = h.store.BulkSetMonitorGroup(ctx, req.IDs, req.GroupID)
	default:
		writeError(w, http.StatusBadRequest, "action must be one of: pause, resume, delete, set_group")
		return
	}

	if err != nil {
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
	m.Enabled = true
}

func (h *Handler) createHeartbeat(ctx context.Context, m *storage.Monitor) (*storage.Heartbeat, error) {
	token, err := generateToken()
	if err != nil {
		h.logger.Error("generate heartbeat token", "error", err)
		return nil, fmt.Errorf("generate heartbeat token: %w", err)
	}
	grace := parseGraceFromSettings(m.Settings)
	hb := &storage.Heartbeat{
		MonitorID: m.ID,
		Token:     token,
		Grace:     grace,
		Status:    "pending",
	}
	if err := h.store.CreateHeartbeat(ctx, hb); err != nil {
		h.logger.Error("create heartbeat", "error", err)
		return nil, fmt.Errorf("create heartbeat: %w", err)
	}
	return hb, nil
}

func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func parseGraceFromSettings(settings json.RawMessage) int {
	if settings == nil {
		return 0
	}
	var s map[string]any
	if err := json.Unmarshal(settings, &s); err != nil {
		return 0
	}
	g, ok := s["grace"]
	if !ok {
		return 0
	}
	gf, ok := g.(float64)
	if !ok {
		return 0
	}
	return int(gf)
}
