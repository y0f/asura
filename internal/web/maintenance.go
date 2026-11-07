package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/notifier"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) Maintenance(w http.ResponseWriter, r *http.Request) {
	windows, err := h.store.ListMaintenanceWindows(r.Context())
	if err != nil {
		h.logger.Error("web: list maintenance", "error", err)
	}

	lp := h.newLayoutParams(r, "Maintenance", "maintenance")
	h.renderComponent(w, r, views.MaintenanceListPage(views.MaintenanceListParams{
		LayoutParams: lp,
		Windows:      windows,
	}))
}

func (h *Handler) MaintenanceCreate(w http.ResponseWriter, r *http.Request) {
	mw, err := h.parseMaintenanceForm(r)
	if err != nil {
		h.setError(w, err.Error())
		h.redirect(w, r, "/maintenance")
		return
	}
	if err := validate.ValidateMaintenanceWindow(mw); err != nil {
		h.setError(w, err.Error())
		h.redirect(w, r, "/maintenance")
		return
	}
	if err := h.store.CreateMaintenanceWindow(r.Context(), mw); err != nil {
		h.logger.Error("web: create maintenance", "error", err)
		h.setError(w, "Failed to create maintenance window")
		h.redirect(w, r, "/maintenance")
		return
	}
	h.setFlash(w, "Maintenance window created")
	h.redirect(w, r, "/maintenance")
}

func (h *Handler) MaintenanceUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/maintenance")
		return
	}
	mw, err := h.parseMaintenanceForm(r)
	if err != nil {
		h.setError(w, err.Error())
		h.redirect(w, r, "/maintenance")
		return
	}
	mw.ID = id
	if err := validate.ValidateMaintenanceWindow(mw); err != nil {
		h.setError(w, err.Error())
		h.redirect(w, r, "/maintenance")
		return
	}
	if err := h.store.UpdateMaintenanceWindow(r.Context(), mw); err != nil {
		h.logger.Error("web: update maintenance", "error", err)
		h.setError(w, "Failed to update maintenance window")
		h.redirect(w, r, "/maintenance")
		return
	}
	h.setFlash(w, "Maintenance window updated")
	h.redirect(w, r, "/maintenance")
}

func (h *Handler) MaintenanceDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/maintenance")
		return
	}
	if err := h.store.DeleteMaintenanceWindow(r.Context(), id); err != nil {
		h.logger.Error("web: delete maintenance", "error", err)
	}
	h.setFlash(w, "Maintenance window deleted")
	h.redirect(w, r, "/maintenance")
}

func (h *Handler) MaintenanceToggle(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/maintenance")
		return
	}
	mw, err := h.store.GetMaintenanceWindow(r.Context(), id)
	if err != nil {
		h.setError(w, "Maintenance window not found")
		h.redirect(w, r, "/maintenance")
		return
	}
	newActive := !mw.Active
	if err := h.store.ToggleMaintenanceWindow(r.Context(), id, newActive); err != nil {
		h.logger.Error("web: toggle maintenance", "error", err)
		h.setError(w, "Failed to toggle maintenance window")
	} else {
		if newActive {
			h.setFlash(w, "Maintenance started")
			h.notifyMaintenance(r.Context(), mw, "started")
		} else {
			h.setFlash(w, "Maintenance stopped")
			h.notifyMaintenance(r.Context(), mw, "ended")
		}
	}
	h.redirect(w, r, "/maintenance")
}

func (h *Handler) parseMaintenanceForm(r *http.Request) (*storage.MaintenanceWindow, error) {
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("invalid maintenance form")
	}

	recurring := r.FormValue("recurring")

	startTime, _ := time.Parse("2006-01-02T15:04", r.FormValue("start_time"))
	endTime, _ := time.Parse("2006-01-02T15:04", r.FormValue("end_time"))

	if recurring == "manual" {
		now := time.Now().UTC()
		startTime = now
		endTime = now.Add(time.Hour)
	}

	mw := &storage.MaintenanceWindow{
		Name:      r.FormValue("name"),
		StartTime: startTime,
		EndTime:   endTime,
		Recurring: recurring,
		CronExpr:  strings.TrimSpace(r.FormValue("cron_expr")),
		Active:    r.FormValue("active") == "on",
	}

	for _, ids := range r.Form["monitor_ids"] {
		if strings.TrimSpace(ids) == "" {
			continue
		}
		for _, idStr := range strings.Split(ids, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
			if err != nil || id <= 0 {
				return nil, fmt.Errorf("monitor_ids must contain positive monitor IDs")
			}
			mw.MonitorIDs = append(mw.MonitorIDs, id)
		}
	}
	if r.FormValue("all_monitors") != "" && len(mw.MonitorIDs) != 0 {
		return nil, fmt.Errorf("choose either all monitors or selected monitor IDs")
	}

	return mw, nil
}

func (h *Handler) notifyMaintenance(ctx context.Context, mw *storage.MaintenanceWindow, action string) {
	if h.notifier == nil {
		return
	}
	msg := fmt.Sprintf("[MAINTENANCE] %s %s: %s", mw.Name, action, mw.Name)
	if err := h.notifier.NotifyWithPayload(ctx, &notifier.Payload{
		EventType: "maintenance." + action,
		Monitor:   &storage.Monitor{Name: msg},
	}); err != nil {
		h.logger.Error("queue maintenance notification", "error", err)
	}
}
