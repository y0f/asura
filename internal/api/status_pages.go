package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

func (h *Handler) ListStatusPages(w http.ResponseWriter, r *http.Request) {
	pages, err := h.store.ListStatusPages(r.Context())
	if err != nil {
		h.logger.Error("list status pages", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list status pages")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": pages})
}

func (h *Handler) GetStatusPage(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	sp, err := h.store.GetStatusPage(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get status page")
		return
	}

	monitors, err := h.store.ListStatusPageMonitors(r.Context(), id)
	if err != nil {
		h.logger.Error("get status page monitors", "error", err)
		monitors = []storage.StatusPageMonitor{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status_page": sp,
		"monitors":    monitors,
	})
}

func (h *Handler) CreateStatusPage(w http.ResponseWriter, r *http.Request) {
	var input struct {
		storage.StatusPage
		Monitors []storage.StatusPageMonitor `json:"monitors"`
	}
	if err := h.readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	sp := &input.StatusPage
	if err := validate.ValidateStatusPage(sp); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()

	existing, err := h.store.GetStatusPageBySlug(ctx, sp.Slug)
	if err == nil && existing != nil {
		writeError(w, http.StatusConflict, "slug already in use")
		return
	}

	if err := h.store.CreateStatusPage(ctx, sp); err != nil {
		h.logger.Error("create status page", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create status page")
		return
	}

	if len(input.Monitors) > 0 {
		for i := range input.Monitors {
			input.Monitors[i].PageID = sp.ID
		}
		if err := h.store.SetStatusPageMonitors(ctx, sp.ID, input.Monitors); err != nil {
			h.logger.Error("set status page monitors", "error", err)
			writeError(w, http.StatusInternalServerError, "status page created but failed to set monitors")
			return
		}
	}

	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}
	h.audit(r, "create", "status_page", sp.ID, "")
	writeJSON(w, http.StatusCreated, sp)
}

func (h *Handler) UpdateStatusPage(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	existing, err := h.store.GetStatusPage(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get status page")
		return
	}

	var input struct {
		storage.StatusPage
		Monitors *[]storage.StatusPageMonitor `json:"monitors"`
	}
	if err := h.readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	sp := &input.StatusPage
	sp.ID = id
	sp.PasswordHash = existing.PasswordHash
	if err := validate.ValidateStatusPage(sp); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	slugOwner, err := h.store.GetStatusPageBySlug(ctx, sp.Slug)
	if err == nil && slugOwner != nil && slugOwner.ID != id {
		writeError(w, http.StatusConflict, "slug already in use")
		return
	}

	if err := h.store.UpdateStatusPage(ctx, sp); err != nil {
		h.logger.Error("update status page", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update status page")
		return
	}

	if input.Monitors != nil {
		for i := range *input.Monitors {
			(*input.Monitors)[i].PageID = id
		}
		if err := h.store.SetStatusPageMonitors(ctx, id, *input.Monitors); err != nil {
			h.logger.Error("set status page monitors", "error", err)
			writeError(w, http.StatusInternalServerError, "status page updated but failed to set monitors")
			return
		}
	}

	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}
	h.audit(r, "update", "status_page", id, "")
	writeJSON(w, http.StatusOK, sp)
}

func (h *Handler) DeleteStatusPage(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	_, err = h.store.GetStatusPage(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get status page")
		return
	}

	if err := h.store.DeleteStatusPage(ctx, id); err != nil {
		h.logger.Error("delete status page", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete status page")
		return
	}

	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}
	h.audit(r, "delete", "status_page", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) PublicStatusPage(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	sp, err := h.store.GetStatusPage(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get status page")
		return
	}

	if !sp.Enabled && !sp.APIEnabled {
		writeError(w, http.StatusNotFound, "status page is not enabled")
		return
	}

	if sp.PasswordHash != "" && !statusPageAuthValid(r, sp.ID, sp.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "status page is password protected")
		return
	}

	monitors, _, err := h.store.ListStatusPageMonitorsWithStatus(ctx, sp.ID)
	if err != nil {
		h.logger.Error("public status page: list monitors", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load monitors")
		return
	}

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -90)

	type safeMonitor struct {
		ID          int64                  `json:"id"`
		Name        string                 `json:"name"`
		Type        string                 `json:"type"`
		Status      string                 `json:"status"`
		Uptime90d   float64                `json:"uptime_90d"`
		DailyUptime []*storage.DailyUptime `json:"daily_uptime"`
	}

	result := make([]safeMonitor, 0, len(monitors))
	for _, m := range monitors {
		daily, err := h.store.GetDailyUptime(ctx, m.ID, from, now)
		if err != nil {
			daily = []*storage.DailyUptime{}
		}
		uptime, err := h.store.GetUptimePercent(ctx, m.ID, from, now)
		if err != nil {
			uptime = 100
		}
		result = append(result, safeMonitor{
			ID:          m.ID,
			Name:        m.Name,
			Type:        m.Type,
			Status:      m.Status,
			Uptime90d:   uptime,
			DailyUptime: daily,
		})
	}

	overall := httputil.OverallStatus(monitors)
	incidents := httputil.PublicIncidentsForPage(ctx, h.store, sp, monitors, now)

	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, map[string]any{
		"page": map[string]string{
			"title":       sp.Title,
			"description": sp.Description,
		},
		"overall_status": overall,
		"monitors":       result,
		"incidents":      incidents,
	})
}

// statusPageAuthValid reports whether the request carries a valid status-page
// auth cookie, matching the value set by the web auth handler.
func statusPageAuthValid(r *http.Request, pageID int64, passwordHash string) bool {
	c, err := r.Cookie(fmt.Sprintf("sp_auth_%d", pageID))
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(passwordHash + ":sp-auth"))
	expected := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(expected)) == 1
}
