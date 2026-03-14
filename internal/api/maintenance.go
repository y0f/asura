package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

func (h *Handler) ListMaintenance(w http.ResponseWriter, r *http.Request) {
	windows, err := h.store.ListMaintenanceWindows(r.Context())
	if err != nil {
		h.logger.Error("list maintenance", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list maintenance windows")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": windows})
}

func (h *Handler) CreateMaintenance(w http.ResponseWriter, r *http.Request) {
	var mw storage.MaintenanceWindow
	if err := readJSON(r, &mw); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if mw.MonitorIDs == nil {
		mw.MonitorIDs = []int64{}
	}

	if err := validate.ValidateMaintenanceWindow(&mw); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.CreateMaintenanceWindow(r.Context(), &mw); err != nil {
		h.logger.Error("create maintenance", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create maintenance window")
		return
	}

	h.audit(r, "create", "maintenance_window", mw.ID, "")
	writeJSON(w, http.StatusCreated, mw)
}

func (h *Handler) UpdateMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_, err = h.store.GetMaintenanceWindow(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "maintenance window not found")
			return
		}
		h.logger.Error("get maintenance for update", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get maintenance window")
		return
	}

	var mw storage.MaintenanceWindow
	if err := readJSON(r, &mw); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mw.ID = id
	if mw.MonitorIDs == nil {
		mw.MonitorIDs = []int64{}
	}

	if err := validate.ValidateMaintenanceWindow(&mw); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.UpdateMaintenanceWindow(r.Context(), &mw); err != nil {
		h.logger.Error("update maintenance", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update maintenance window")
		return
	}

	h.audit(r, "update", "maintenance_window", mw.ID, "")
	writeJSON(w, http.StatusOK, mw)
}

func (h *Handler) ToggleMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mw, err := h.store.GetMaintenanceWindow(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "maintenance window not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get maintenance window")
		return
	}
	newActive := !mw.Active
	if err := h.store.ToggleMaintenanceWindow(r.Context(), id, newActive); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to toggle maintenance window")
		return
	}
	h.audit(r, "toggle", "maintenance_window", id, fmt.Sprintf("active=%v", newActive))
	writeJSON(w, http.StatusOK, map[string]bool{"active": newActive})
}

func (h *Handler) DeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_, err = h.store.GetMaintenanceWindow(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "maintenance window not found")
			return
		}
		h.logger.Error("get maintenance for delete", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get maintenance window")
		return
	}

	if err := h.store.DeleteMaintenanceWindow(r.Context(), id); err != nil {
		h.logger.Error("delete maintenance", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete maintenance window")
		return
	}

	h.audit(r, "delete", "maintenance_window", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
