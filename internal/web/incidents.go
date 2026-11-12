package web

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/notifier"
	"github.com/y0f/asura/v3/internal/storage"
	"github.com/y0f/asura/v3/internal/validate"
	"github.com/y0f/asura/v3/internal/web/views"
)

func (h *Handler) Incidents(w http.ResponseWriter, r *http.Request) {
	p := httputil.ParsePagination(r)
	status := r.URL.Query().Get("status")
	if !validate.ValidIncidentStatuses[status] {
		status = ""
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	result, err := h.store.ListIncidentsFiltered(r.Context(), storage.IncidentListFilter{
		Status: status, Search: q,
		Access:                httputil.MonitorAccess(r.Context(), "incidents.read"),
		MonitorReadAccess:     httputil.MonitorAccess(r.Context(), "monitors.read"),
		DiagnosticsReadAccess: httputil.MonitorAccess(r.Context(), "diagnostics.read"),
	}, p)
	if err == nil {
		result, err = httputil.IncidentListResponse(r.Context(), result)
	}
	if err != nil {
		h.logger.Error("web: list incidents", "error", err)
		http.Error(w, "Failed to load incidents", http.StatusInternalServerError)
		return
	}
	h.renderComponent(w, r, views.IncidentListPage(views.IncidentListParams{
		LayoutParams: h.newLayoutParams(r, "Incidents", "incidents"),
		Result:       result, Filter: status, Search: q,
	}))
}

func (h *Handler) incidentError(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case errors.Is(err, storage.ErrIncidentConflict):
		http.Error(w, storage.ErrIncidentConflict.Error(), http.StatusConflict)
	default:
		h.logger.Error("web: incident request", "error", err)
		http.Error(w, "Failed to process incident", http.StatusInternalServerError)
	}
	return true
}

func (h *Handler) IncidentDetail(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	access := httputil.MonitorAccess(r.Context(), "incidents.read")
	inc, err := h.store.GetIncident(r.Context(), id, access...)
	if h.incidentError(w, r, err) {
		return
	}
	access, diagnostics := httputil.DiagnosticReadAccess(r.Context(), "incidents.read", inc.MonitorID, inc.MonitorGroupID)
	events, err := h.store.ListIncidentEvents(r.Context(), id, access...)
	if h.incidentError(w, r, err) {
		return
	}
	h.renderComponent(w, r, views.IncidentDetailPage(views.IncidentDetailParams{
		LayoutParams: h.newLayoutParams(r, "Incident #"+r.PathValue("id"), "incidents"),
		Incident:     httputil.IncidentReadResponse(r.Context(), inc), Events: httputil.IncidentEventsResponse(events, diagnostics),
	}))
}

func (h *Handler) IncidentAck(w http.ResponseWriter, r *http.Request) {
	h.changeIncident(w, r, "acknowledge")
}

func (h *Handler) IncidentResolve(w http.ResponseWriter, r *http.Request) {
	h.changeIncident(w, r, "resolve")
}

func (h *Handler) changeIncident(w http.ResponseWriter, r *http.Request, action string) {
	id, err := httputil.ParseID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	inc, err := h.store.TransitionIncident(ctx, id, storage.IncidentTransition{
		Action: action, Actor: httputil.GetAPIKeyName(ctx),
	}, httputil.MonitorAccess(ctx, "incidents."+action)...)
	if h.incidentError(w, r, err) {
		return
	}
	h.audit(r, action, "incident", id, "")
	message, event := "Incident acknowledged", "incident.acknowledged"
	if action == "resolve" {
		message, event = "Incident resolved", "incident.resolved"
	}
	if h.notifier != nil {
		if err := h.notifier.NotifyWithPayload(ctx, &notifier.Payload{EventType: event, Incident: inc}); err != nil {
			h.logger.Error("queue incident notification", "error", err)
		}
	}
	h.setFlash(w, message)
	if httputil.GetAPIKey(ctx).AllowsMonitor("incidents.read", inc.MonitorID, inc.MonitorGroupID) {
		h.redirect(w, r, "/incidents/"+r.PathValue("id"))
		return
	}
	h.redirect(w, r, "/")
}

func (h *Handler) IncidentDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.incidentError(w, r, h.store.DeleteIncident(r.Context(), id, httputil.MonitorAccess(r.Context(), "incidents.delete")...)) {
		return
	}
	h.audit(r, "delete", "incident", id, "")
	h.setFlash(w, "Incident deleted")
	if httputil.GetAPIKey(r.Context()).HasCapability("incidents.read") {
		h.redirect(w, r, "/incidents")
		return
	}
	h.redirect(w, r, "/")
}
