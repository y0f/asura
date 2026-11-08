package api

import (
	"net/http"

	"github.com/y0f/asura/internal/httputil"
)

func (h *Handler) SetManualStatus(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := h.readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if req.Status != "up" && req.Status != "down" && req.Status != "degraded" {
		writeError(w, http.StatusBadRequest, "status must be one of: up, down, degraded")
		return
	}

	mon, ok := h.requireMonitor(w, r, id, "monitors.status")
	if !ok {
		return
	}

	if mon.Type != "manual" {
		writeError(w, http.StatusBadRequest, "status can only be set on manual monitors")
		return
	}

	if h.pipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "monitoring is unavailable")
		return
	}
	if err := h.pipeline.ProcessManualStatus(r.Context(), mon, req.Status, req.Message, httputil.MonitorAccess(r.Context(), "monitors.status")...); err != nil {
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("set manual status", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to set monitor status")
		return
	}

	h.audit(r, "set_status", "monitor", id, req.Status)
	writeJSON(w, http.StatusOK, map[string]string{"status": req.Status})
}
