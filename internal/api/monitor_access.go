package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/storage"
)

// requireMonitor is shared by monitor-owned read endpoints. Collection queries
// and mutations also receive their own filters so authorization is applied to
// counts and writes, not only to this preliminary lookup.
func (h *Handler) requireMonitor(w http.ResponseWriter, r *http.Request, id int64, actions ...string) (*storage.Monitor, bool) {
	monitor, err := h.store.GetMonitor(r.Context(), id, httputil.MonitorAccess(r.Context(), actions...)...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "monitor not found")
		} else {
			h.logger.Error("load authorized monitor", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to load monitor")
		}
		return nil, false
	}
	return monitor, true
}

func monitorMutationError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, storage.ErrInvalidMonitorProxy) {
		writeError(w, http.StatusBadRequest, storage.ErrInvalidMonitorProxy.Error())
		return true
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "monitor not found")
		return true
	}
	if errors.Is(err, storage.ErrInvalidMonitorRelation) {
		writeError(w, http.StatusBadRequest, storage.ErrInvalidMonitorRelation.Error())
		return true
	}
	return false
}
