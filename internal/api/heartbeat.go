package api

import (
	"database/sql"
	"errors"
	"net/http"
)

func (h *Handler) HeartbeatPing(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		writeError(w, http.StatusBadRequest, "token is required")
		return
	}

	hb, err := h.store.GetHeartbeatByToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "heartbeat not found")
			return
		}
		h.logger.Error("get heartbeat by token", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get heartbeat")
		return
	}

	if h.pipeline != nil {
		err = h.pipeline.ProcessHeartbeatPing(r.Context(), hb.MonitorID, token)
	} else {
		err = h.store.UpdateHeartbeatPing(r.Context(), hb.MonitorID, token)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "heartbeat not found")
			return
		}
		h.logger.Error("update heartbeat ping", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to record ping")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
