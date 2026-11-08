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
	"github.com/y0f/asura/internal/pageauth"
	"github.com/y0f/asura/internal/storage"
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

	monitors, err := h.store.ListStatusPageMonitors(r.Context(), id, httputil.MonitorAccess(r.Context(), "monitors.read")...)
	if err != nil {
		h.logger.Error("get status page monitors", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get status page monitors")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status_page": sp,
		"monitors":    monitors,
	})
}

func (h *Handler) CreateStatusPage(w http.ResponseWriter, r *http.Request) {
	h.saveStatusPage(w, r, 0)
}

func (h *Handler) UpdateStatusPage(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.saveStatusPage(w, r, id)
}

func (h *Handler) saveStatusPage(w http.ResponseWriter, r *http.Request, id int64) {
	var input httputil.StatusPageInput
	if err := h.readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	key := httputil.GetAPIKey(r.Context())
	if err := input.PreparePassword(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sp, err := h.store.SaveStatusPage(r.Context(), id, func(current *storage.StatusPage) (*storage.StatusPage, *[]storage.StatusPageMonitor, error) {
		return input.Apply(current, key != nil && key.SuperAdmin)
	}, storage.StatusPageAccess{Monitors: httputil.MonitorAccess(r.Context(), "monitors.read"), Incidents: httputil.MonitorAccess(r.Context(), "incidents.read")})
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "status page not found")
		case errors.Is(err, storage.ErrStatusPageSlugConflict):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, storage.ErrStatusPageMonitorAccess), errors.Is(err, storage.ErrStatusPageIncidentAccess), errors.Is(err, httputil.ErrStatusPageAnalytics):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, storage.ErrInvalidStatusPageMonitors), errors.Is(err, httputil.ErrInvalidStatusPage):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			h.logger.Error("save status page", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to save status page")
		}
		return
	}
	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}
	status, action := http.StatusOK, "update"
	if id == 0 {
		status, action = http.StatusCreated, "create"
	}
	h.audit(r, action, "status_page", sp.ID, "")
	if key == nil || !key.HasPermission("status_pages.read") {
		writeJSON(w, status, map[string]int64{"id": sp.ID})
		return
	}
	writeJSON(w, status, sp)
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
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
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

	// The JSON is served for every public (enabled) page; api_enabled
	// ("Public JSON API") additionally serves it while the page is not
	// public. SubscribeAPI requires both.
	if !sp.Enabled && !sp.APIEnabled {
		writeError(w, http.StatusNotFound, "status page is not enabled")
		return
	}

	if !h.requireStatusPageAuth(w, r, sp) {
		return
	}

	now := time.Now().UTC()
	snapshot, err := h.store.GetPublicStatusPageSnapshot(ctx, sp.ID, storage.PublicStatusPageAccess{
		ExpectedPasswordHash: sp.PasswordHash, Audience: storage.PublicStatusPageJSON,
	}, now, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "status page not found")
			return
		}
		h.logger.Error("public status page snapshot", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load status page")
		return
	}
	sp, monitors := snapshot.Page, snapshot.Monitors

	type safeMonitor struct {
		ID          int64                  `json:"id"`
		Name        string                 `json:"name"`
		Type        string                 `json:"type"`
		Status      string                 `json:"status"`
		Uptime90d   *float64               `json:"uptime_90d"`
		TotalChecks int64                  `json:"total_checks"`
		DailyUptime []*storage.DailyUptime `json:"daily_uptime"`
	}

	result := make([]safeMonitor, 0, len(monitors))
	for _, m := range monitors {
		daily := snapshot.DailyUptime[m.ID]
		uptime, total := storage.SummarizeUptime(daily)
		var measured *float64
		if total > 0 {
			measured = &uptime
		}
		result = append(result, safeMonitor{
			ID:          m.ID,
			Name:        m.Name,
			Type:        m.Type,
			Status:      m.Status,
			Uptime90d:   measured,
			TotalChecks: total,
			DailyUptime: daily,
		})
	}

	overall := httputil.OverallStatus(monitors)

	if sp.PasswordHash == "" {
		w.Header().Set("Cache-Control", "public, max-age=30")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page": map[string]string{
			"title":       sp.Title,
			"description": sp.Description,
		},
		"overall_status": overall,
		"monitors":       result,
		"incidents":      snapshot.Incidents,
	})
}

func (h *Handler) requireStatusPageAuth(w http.ResponseWriter, r *http.Request, sp *storage.StatusPage) bool {
	if sp.PasswordHash == "" {
		return true
	}
	valid, limited := false, false
	if r.Header.Get(statusPagePasswordHeader) != "" {
		// Failed header passwords share the web login attempt budget.
		valid, limited = h.loginRL.LimitFailures(httputil.ExtractIP(r, h.cfg.TrustedNets()), func() bool {
			return statusPageAuthValid(r, sp.ID, sp.PasswordHash)
		})
	} else {
		valid = statusPageAuthValid(r, sp.ID, sp.PasswordHash)
	}
	if limited {
		writeError(w, http.StatusTooManyRequests, "too many password attempts")
		return false
	}
	if !valid {
		writeError(w, http.StatusUnauthorized, "status page is password protected")
		return false
	}
	return true
}

// statusPagePasswordHeader lets API clients without the web UI's auth cookie
// read a password-protected status page.
const statusPagePasswordHeader = "X-Status-Page-Password"

// statusPageAuthValid reports whether the request carries a valid status-page
// auth cookie (as set by the web auth handler) or the page password in the
// X-Status-Page-Password header.
func statusPageAuthValid(r *http.Request, pageID int64, passwordHash string) bool {
	if pw := r.Header.Get(statusPagePasswordHeader); pw != "" {
		return pageauth.Verify(passwordHash, pw)
	}
	c, err := r.Cookie(fmt.Sprintf("sp_auth_%d", pageID))
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(passwordHash + ":sp-auth"))
	expected := hex.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(expected)) == 1
}
