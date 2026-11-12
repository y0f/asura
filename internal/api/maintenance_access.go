package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/storage"
)

func maintenanceMutationMissing(w http.ResponseWriter, err error) bool {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "maintenance window or target not found")
		return true
	}
	return false
}

func (h *Handler) maintenanceResponse(r *http.Request, window *storage.MaintenanceWindow) any {
	if readable, err := h.store.GetMaintenanceWindow(r.Context(), window.ID, httputil.MonitorAccess(r.Context(), "maintenance.read")...); err == nil {
		return readable
	}
	return map[string]int64{"id": window.ID}
}
