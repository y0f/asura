package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

func (h *Handler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.store.ListMonitorGroups(r.Context())
	if err != nil {
		h.logger.Error("list groups", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list groups")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": groups})
}

func (h *Handler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var g storage.MonitorGroup
	if err := h.readJSON(r, &g); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := validate.ValidateMonitorGroup(&g); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.CreateMonitorGroup(r.Context(), &g); err != nil {
		h.logger.Error("create group", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create group")
		return
	}

	h.audit(r, "create", "monitor_group", g.ID, "")
	writeJSON(w, http.StatusCreated, g)
}

func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_, err = h.store.GetMonitorGroup(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
			return
		}
		h.logger.Error("get group for update", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get group")
		return
	}

	var g storage.MonitorGroup
	if err := h.readJSON(r, &g); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	g.ID = id

	if err := validate.ValidateMonitorGroup(&g); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.UpdateMonitorGroup(r.Context(), &g); err != nil {
		h.logger.Error("update group", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update group")
		return
	}

	h.audit(r, "update", "monitor_group", g.ID, "")
	writeJSON(w, http.StatusOK, g)
}

func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_, err = h.store.GetMonitorGroup(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
			return
		}
		h.logger.Error("get group for delete", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get group")
		return
	}

	if err := h.store.DeleteMonitorGroup(r.Context(), id); err != nil {
		h.logger.Error("delete group", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete group")
		return
	}

	h.audit(r, "delete", "monitor_group", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
