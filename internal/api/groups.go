package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
	"github.com/y0f/asura/v3/internal/validate"
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
	writeJSON(w, http.StatusCreated, groupResponse(r, &g))
}

func (h *Handler) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
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
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
			return
		}
		h.logger.Error("update group", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update group")
		return
	}

	h.audit(r, "update", "monitor_group", g.ID, "")
	writeJSON(w, http.StatusOK, groupResponse(r, &g))
}

func (h *Handler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.DeleteEmptyMonitorGroup(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrGroupNotEmpty) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "group not found")
			return
		}
		h.logger.Error("delete group", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete group")
		return
	}

	h.audit(r, "delete", "monitor_group", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func groupResponse(r *http.Request, g *storage.MonitorGroup) any {
	if !httputil.GetAPIKey(r.Context()).HasPermission("groups.read") {
		return map[string]int64{"id": g.ID}
	}
	return g
}
