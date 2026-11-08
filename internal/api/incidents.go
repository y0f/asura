package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/incident"
	"github.com/y0f/asura/v2/internal/notifier"
	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/validate"
)

func (h *Handler) ListIncidents(w http.ResponseWriter, r *http.Request) {
	p := httputil.ParsePagination(r)
	monitorID, _ := strconv.ParseInt(r.URL.Query().Get("monitor_id"), 10, 64)
	status := r.URL.Query().Get("status")
	if !validate.ValidIncidentStatuses[status] {
		status = ""
	}
	result, err := h.store.ListIncidentsFiltered(r.Context(), storage.IncidentListFilter{
		MonitorID: monitorID, Status: status,
		Access:                httputil.MonitorAccess(r.Context(), "incidents.read"),
		MonitorReadAccess:     httputil.MonitorAccess(r.Context(), "monitors.read"),
		DiagnosticsReadAccess: httputil.MonitorAccess(r.Context(), "diagnostics.read"),
	}, p)
	if err == nil {
		result, err = httputil.IncidentListResponse(r.Context(), result)
	}
	if err != nil {
		h.logger.Error("list incidents", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list incidents")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) incidentError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "incident not found")
	case errors.Is(err, storage.ErrIncidentConflict):
		writeError(w, http.StatusConflict, storage.ErrIncidentConflict.Error())
	default:
		h.logger.Error("incident request", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to process incident")
	}
	return true
}

func (h *Handler) GetIncident(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	access := httputil.MonitorAccess(r.Context(), "incidents.read")
	inc, err := h.store.GetIncident(r.Context(), id, access...)
	if h.incidentError(w, err) {
		return
	}
	access, diagnostics := httputil.DiagnosticReadAccess(r.Context(), "incidents.read", inc.MonitorID, inc.MonitorGroupID)
	events, err := h.store.ListIncidentEvents(r.Context(), id, access...)
	if h.incidentError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"incident": httputil.IncidentReadResponse(r.Context(), inc),
		"timeline": httputil.IncidentEventsResponse(events, diagnostics),
	})
}

func (h *Handler) AckIncident(w http.ResponseWriter, r *http.Request) {
	h.changeIncident(w, r, "acknowledge", "")
}

func (h *Handler) ResolveIncident(w http.ResponseWriter, r *http.Request) {
	h.changeIncident(w, r, "resolve", "")
}

func (h *Handler) SetIncidentSeverity(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Severity string `json:"severity"`
	}
	if err := h.readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !incident.ValidSeverity(req.Severity) {
		writeError(w, http.StatusBadRequest, "severity must be one of: critical, major, minor, warning")
		return
	}
	h.changeIncident(w, r, "severity", req.Severity)
}

func (h *Handler) changeIncident(w http.ResponseWriter, r *http.Request, action, severity string) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	inc, err := h.store.TransitionIncident(ctx, id, storage.IncidentTransition{
		Action: action, Actor: httputil.GetAPIKeyName(ctx), Severity: severity,
	}, httputil.MonitorAccess(ctx, "incidents."+action)...)
	if h.incidentError(w, err) {
		return
	}
	auditAction := action
	if action == "severity" {
		auditAction = "set_severity"
	}
	h.audit(r, auditAction, "incident", id, severity)
	if h.notifier != nil && action != "severity" {
		event := "incident.acknowledged"
		if action == "resolve" {
			event = "incident.resolved"
		}
		if err := h.notifier.NotifyWithPayload(ctx, &notifier.Payload{EventType: event, Incident: inc}); err != nil {
			h.logger.Error("queue incident notification", "error", err)
		}
	}
	if !httputil.GetAPIKey(ctx).AllowsMonitor("incidents.read", inc.MonitorID, inc.MonitorGroupID) {
		writeJSON(w, http.StatusOK, map[string]int64{"id": inc.ID})
		return
	}
	writeJSON(w, http.StatusOK, httputil.IncidentReadResponse(ctx, inc))
}

func (h *Handler) DeleteIncident(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.incidentError(w, h.store.DeleteIncident(r.Context(), id, httputil.MonitorAccess(r.Context(), "incidents.delete")...)) {
		return
	}
	h.audit(r, "delete", "incident", id, "")
	w.WriteHeader(http.StatusNoContent)
}
