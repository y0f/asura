package api

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
	"github.com/y0f/asura/v3/internal/validate"
)

func (h *Handler) ListMaintenance(w http.ResponseWriter, r *http.Request) {
	windows, err := h.store.ListMaintenanceWindows(r.Context(), httputil.MonitorAccess(r.Context(), "maintenance.read")...)
	if err != nil {
		h.logger.Error("list maintenance", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list maintenance windows")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": windows})
}

func (h *Handler) CreateMaintenance(w http.ResponseWriter, r *http.Request) {
	var mw storage.MaintenanceWindow
	if err := h.readJSON(r, &mw); err != nil {
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

	if err := h.store.CreateMaintenanceWindow(r.Context(), &mw, httputil.MonitorAccess(r.Context(), "maintenance.create")...); err != nil {
		if maintenanceMutationMissing(w, err) {
			return
		}
		h.logger.Error("create maintenance", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create maintenance window")
		return
	}

	h.audit(r, "create", "maintenance_window", mw.ID, "")
	writeJSON(w, http.StatusCreated, h.maintenanceResponse(r, &mw))
}

func (h *Handler) UpdateMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var input struct {
		storage.MaintenanceWindow
		Active     *bool    `json:"active"`
		MonitorIDs *[]int64 `json:"monitor_ids"`
	}
	if err := h.readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mw := input.MaintenanceWindow
	mw.ID = id
	if input.MonitorIDs != nil {
		mw.MonitorIDs = *input.MonitorIDs
	}
	if err := validate.ValidateMaintenanceWindow(&mw); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := h.store.UpdateMaintenanceWindowWithActions(r.Context(), id, func(current *storage.MaintenanceWindow) (*storage.MaintenanceWindow, error) {
		next := mw
		next.Active = current.Active
		if input.Active != nil {
			next.Active = *input.Active
		}
		if input.MonitorIDs == nil {
			next.MonitorIDs = current.MonitorIDs
		}
		return &next, nil
	}, storage.MaintenanceUpdateAccess{Update: httputil.MonitorAccess(r.Context(), "maintenance.update"), Toggle: httputil.MonitorAccess(r.Context(), "maintenance.toggle")})
	if err != nil {
		if maintenanceMutationMissing(w, err) {
			return
		}
		h.logger.Error("update maintenance", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update maintenance window")
		return
	}

	h.audit(r, "update", "maintenance_window", mw.ID, "")
	writeJSON(w, http.StatusOK, h.maintenanceResponse(r, saved))
}

func (h *Handler) ToggleMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mw, err := h.store.FlipMaintenanceWindow(r.Context(), id, httputil.MonitorAccess(r.Context(), "maintenance.toggle"))
	if err != nil {
		if maintenanceMutationMissing(w, err) {
			return
		}
		h.logger.Error("toggle maintenance", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to toggle maintenance window")
		return
	}
	newActive := mw.Active

	h.audit(r, "toggle", "maintenance_window", id, fmt.Sprintf("active=%v", newActive))
	writeJSON(w, http.StatusOK, map[string]bool{"active": newActive})
}

func (h *Handler) DeleteMaintenance(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_, err = h.store.GetMaintenanceWindow(r.Context(), id, httputil.MonitorAccess(r.Context(), "maintenance.delete")...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "maintenance window not found")
			return
		}
		h.logger.Error("get maintenance for delete", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get maintenance window")
		return
	}

	if err := h.store.DeleteMaintenanceWindow(r.Context(), id, httputil.MonitorAccess(r.Context(), "maintenance.delete")...); err != nil {
		if maintenanceMutationMissing(w, err) {
			return
		}
		h.logger.Error("delete maintenance", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete maintenance window")
		return
	}

	h.audit(r, "delete", "maintenance_window", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
