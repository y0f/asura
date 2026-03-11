package web

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/y0f/asura/internal/escalation"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
	"github.com/y0f/asura/internal/web/views"
)

func newIncidentEvent(incidentID int64, eventType, message string) *storage.IncidentEvent {
	return &storage.IncidentEvent{
		IncidentID: incidentID,
		Type:       eventType,
		Message:    message,
	}
}

func (h *Handler) Incidents(w http.ResponseWriter, r *http.Request) {
	p := httputil.ParsePagination(r)
	status := r.URL.Query().Get("status")
	if !validate.ValidIncidentStatuses[status] {
		status = ""
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	result, err := h.store.ListIncidents(r.Context(), 0, status, q, p)
	if err != nil {
		h.logger.Error("web: list incidents", "error", err)
	}

	lp := h.newLayoutParams(r, "Incidents", "incidents")
	h.renderComponent(w, r, views.IncidentListPage(views.IncidentListParams{
		LayoutParams: lp,
		Result:       result,
		Filter:       status,
		Search:       q,
	}))
}

func (h *Handler) IncidentDetail(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/incidents")
		return
	}

	inc, err := h.store.GetIncident(r.Context(), id)
	if err != nil {
		h.redirect(w, r, "/incidents")
		return
	}

	events, _ := h.store.ListIncidentEvents(r.Context(), id)

	lp := h.newLayoutParams(r, "Incident #"+r.PathValue("id"), "incidents")
	h.renderComponent(w, r, views.IncidentDetailPage(views.IncidentDetailParams{
		LayoutParams: lp,
		Incident:     inc,
		Events:       events,
	}))
}

func (h *Handler) IncidentAck(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/incidents")
		return
	}
	ctx := r.Context()

	inc, err := h.store.GetIncident(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			h.redirect(w, r, "/incidents")
			return
		}
		h.logger.Error("web: get incident for ack", "error", err)
		h.redirect(w, r, "/incidents")
		return
	}

	if inc.Status != incident.StatusOpen {
		h.setFlash(w, "Incident is not open")
		h.redirect(w, r, "/incidents/"+r.PathValue("id"))
		return
	}

	now := time.Now().UTC()
	inc.Status = incident.StatusAcknowledged
	inc.AcknowledgedAt = &now
	inc.AcknowledgedBy = httputil.GetAPIKeyName(ctx)

	if err := h.store.UpdateIncident(ctx, inc); err != nil {
		h.logger.Error("web: ack incident", "error", err)
		h.setFlash(w, "Failed to acknowledge incident")
		h.redirect(w, r, "/incidents/"+r.PathValue("id"))
		return
	}

	if err := h.store.InsertIncidentEvent(ctx, newIncidentEvent(inc.ID, incident.EventAcknowledged, "Acknowledged by "+inc.AcknowledgedBy)); err != nil {
		h.logger.Error("web: insert ack event", "error", err)
	}

	escalation.CancelEscalation(ctx, h.store, inc.ID)

	if h.notifier != nil {
		h.notifier.NotifyWithPayload(&notifier.Payload{
			EventType: "incident.acknowledged",
			Incident:  inc,
		})
	}

	h.setFlash(w, "Incident acknowledged")
	h.redirect(w, r, "/incidents/"+r.PathValue("id"))
}

func (h *Handler) IncidentResolve(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/incidents")
		return
	}
	ctx := r.Context()

	inc, err := h.store.GetIncident(ctx, id)
	if err != nil {
		h.redirect(w, r, "/incidents")
		return
	}

	if inc.Status == incident.StatusResolved {
		h.setFlash(w, "Incident is already resolved")
		h.redirect(w, r, "/incidents/"+r.PathValue("id"))
		return
	}

	now := time.Now().UTC()
	inc.Status = incident.StatusResolved
	inc.ResolvedAt = &now
	inc.ResolvedBy = httputil.GetAPIKeyName(ctx)

	if err := h.store.UpdateIncident(ctx, inc); err != nil {
		h.logger.Error("web: resolve incident", "error", err)
		h.setFlash(w, "Failed to resolve incident")
		h.redirect(w, r, "/incidents/"+r.PathValue("id"))
		return
	}

	if err := h.store.InsertIncidentEvent(ctx, newIncidentEvent(inc.ID, incident.EventResolved, "Manually resolved by "+inc.ResolvedBy)); err != nil {
		h.logger.Error("web: insert resolve event", "error", err)
	}

	escalation.CancelEscalation(ctx, h.store, inc.ID)

	if h.notifier != nil {
		h.notifier.NotifyWithPayload(&notifier.Payload{
			EventType: "incident.resolved",
			Incident:  inc,
		})
	}

	h.setFlash(w, "Incident resolved")
	h.redirect(w, r, "/incidents/"+r.PathValue("id"))
}

func (h *Handler) IncidentDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/incidents")
		return
	}
	if err := h.store.DeleteIncident(r.Context(), id); err != nil {
		h.logger.Error("web: delete incident", "error", err)
	}
	h.setFlash(w, "Incident deleted")
	h.redirect(w, r, "/incidents")
}
