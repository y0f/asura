package api

import (
	"net/http"

	"github.com/y0f/asura/internal/httputil"
)

func (h *Handler) DBVacuum(w http.ResponseWriter, r *http.Request) {
	k := httputil.GetAPIKey(r.Context())
	if k == nil || !k.SuperAdmin {
		writeError(w, http.StatusForbidden, "vacuum requires super admin access")
		return
	}
	if err := h.store.Vacuum(r.Context()); err != nil {
		h.logger.Error("api: vacuum", "error", err)
		writeError(w, http.StatusInternalServerError, "vacuum failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) DBSize(w http.ResponseWriter, r *http.Request) {
	size, err := h.store.DBSize()
	if err != nil {
		h.logger.Error("api: db size", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get database size")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"size_bytes": size})
}
