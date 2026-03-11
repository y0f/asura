package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/y0f/asura/internal/escalation"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

func (h *Handler) ListIncidents(w http.ResponseWriter, r *http.Request) {
	p := httputil.ParsePagination(r)
	monitorID, _ := strconv.ParseInt(r.URL.Query().Get("monitor_id"), 10, 64)
	status := r.URL.Query().Get("status")
	if !validate.ValidIncidentStatuses[status] {
		status = ""
	}

	result, err := h.store.ListIncidents(r.Context(), monitorID, status, "", p)
	if err != nil {
		h.logger.Error("list incidents", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list incidents")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) GetIncident(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	inc, err := h.store.GetIncident(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		h.logger.Error("get incident", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get incident")
		return
	}

	events, err := h.store.ListIncidentEvents(r.Context(), id)
	if err != nil {
		h.logger.Error("list incident events", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list incident events")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"incident": inc,
		"timeline": events,
	})
}

func (h *Handler) AckIncident(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	inc, err := h.store.GetIncident(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		h.logger.Error("get incident for ack", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get incident")
		return
	}

	if inc.Status != incident.StatusOpen {
		writeError(w, http.StatusConflict, "incident is not open")
		return
	}

	now := time.Now().UTC()
	inc.Status = incident.StatusAcknowledged
	inc.AcknowledgedAt = &now
	inc.AcknowledgedBy = httputil.GetAPIKeyName(r.Context())

	if err := h.store.UpdateIncident(r.Context(), inc); err != nil {
		h.logger.Error("ack incident", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to acknowledge incident")
		return
	}

	if err := h.store.InsertIncidentEvent(r.Context(), newIncidentEvent(inc.ID, incident.EventAcknowledged, "Acknowledged by "+inc.AcknowledgedBy)); err != nil {
		h.logger.Error("insert ack event", "error", err)
	}

	escalation.CancelEscalation(r.Context(), h.store, inc.ID)
	h.audit(r, "acknowledge", "incident", id, "")

	if h.notifier != nil {
		h.notifier.NotifyWithPayload(&notifier.Payload{
			EventType: "incident.acknowledged",
			Incident:  inc,
		})
	}

	writeJSON(w, http.StatusOK, inc)
}

func (h *Handler) ResolveIncident(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	inc, err := h.store.GetIncident(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		h.logger.Error("get incident for resolve", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get incident")
		return
	}

	if inc.Status == incident.StatusResolved {
		writeError(w, http.StatusConflict, "incident is already resolved")
		return
	}

	now := time.Now().UTC()
	inc.Status = incident.StatusResolved
	inc.ResolvedAt = &now
	inc.ResolvedBy = httputil.GetAPIKeyName(r.Context())

	if err := h.store.UpdateIncident(r.Context(), inc); err != nil {
		h.logger.Error("resolve incident", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to resolve incident")
		return
	}

	if err := h.store.InsertIncidentEvent(r.Context(), newIncidentEvent(inc.ID, incident.EventResolved, "Manually resolved by "+inc.ResolvedBy)); err != nil {
		h.logger.Error("insert resolve event", "error", err)
	}

	escalation.CancelEscalation(r.Context(), h.store, inc.ID)
	h.audit(r, "resolve", "incident", id, "")

	if h.notifier != nil {
		h.notifier.NotifyWithPayload(&notifier.Payload{
			EventType: "incident.resolved",
			Incident:  inc,
		})
	}

	writeJSON(w, http.StatusOK, inc)
}

func (h *Handler) DeleteIncident(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if _, err := h.store.GetIncident(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "incident not found")
			return
		}
		h.logger.Error("get incident for delete", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get incident")
		return
	}

	if err := h.store.DeleteIncident(r.Context(), id); err != nil {
		h.logger.Error("delete incident", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete incident")
		return
	}

	h.audit(r, "delete", "incident", id, "")
	w.WriteHeader(http.StatusNoContent)
}

func newIncidentEvent(incidentID int64, eventType, message string) *storage.IncidentEvent {
	return &storage.IncidentEvent{
		IncidentID: incidentID,
		Type:       eventType,
		Message:    message,
	}
}
