package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/notifier"
	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/validate"
	"github.com/y0f/asura/v2/internal/web/views"
)

func (h *Handler) Maintenance(w http.ResponseWriter, r *http.Request) {
	windows, err := h.store.ListMaintenanceWindows(r.Context(), httputil.MonitorAccess(r.Context(), "maintenance.read")...)
	if err != nil {
		h.logger.Error("web: list maintenance", "error", err)
		http.Error(w, "Failed to load maintenance", http.StatusInternalServerError)
		return
	}
	key := httputil.GetAPIKey(r.Context())
	actions := make(map[int64]map[string]bool)
	for _, action := range []string{"maintenance.update", "maintenance.delete", "maintenance.toggle"} {
		if key.HasPermission(action) || !key.HasCapability(action) {
			continue
		}
		allowed, err := h.store.ListMaintenanceWindows(r.Context(), httputil.MonitorAccess(r.Context(), action)...)
		if err != nil {
			h.logger.Error("web: maintenance action coverage", "error", err)
			http.Error(w, "Failed to load maintenance", http.StatusInternalServerError)
			return
		}
		for _, window := range allowed {
			if actions[window.ID] == nil {
				actions[window.ID] = make(map[string]bool)
			}
			actions[window.ID][action] = true
		}
	}
	var choices []views.MaintenanceMonitorChoice
	if key.HasCapability("maintenance.create") {
		monitors, err := h.store.ListMonitors(r.Context(), storage.MonitorListFilter{Access: httputil.MonitorAccess(r.Context(), "maintenance.create")}, storage.Pagination{Page: 1, PerPage: 10000})
		if err != nil {
			h.logger.Error("web: maintenance monitor choices", "error", err)
			http.Error(w, "Failed to load maintenance", http.StatusInternalServerError)
			return
		} else if rows, ok := monitors.Data.([]*storage.Monitor); ok {
			for _, monitor := range rows {
				label := fmt.Sprintf("Monitor #%d", monitor.ID)
				if key.AllowsMonitor("monitors.read", monitor.ID, monitor.GroupID) {
					label = monitor.Name
				}
				choices = append(choices, views.MaintenanceMonitorChoice{ID: monitor.ID, Label: label})
			}
		}
	}

	lp := h.newLayoutParams(r, "Maintenance", "maintenance")
	h.renderComponent(w, r, views.MaintenanceListPage(views.MaintenanceListParams{
		LayoutParams:   lp,
		Windows:        windows,
		AllowedActions: actions,
		MonitorChoices: choices,
	}))
}

func (h *Handler) MaintenanceCreate(w http.ResponseWriter, r *http.Request) {
	mw, err := h.parseMaintenanceForm(r)
	if err != nil {
		h.setError(w, err.Error())
		h.maintenanceRedirect(w, r)
		return
	}
	if err := validate.ValidateMaintenanceWindow(mw); err != nil {
		h.setError(w, err.Error())
		h.maintenanceRedirect(w, r)
		return
	}
	if err := h.store.CreateMaintenanceWindow(r.Context(), mw, httputil.MonitorAccess(r.Context(), "maintenance.create")...); err != nil {
		if maintenanceMutationMissing(w, r, err) {
			return
		}
		h.logger.Error("web: create maintenance", "error", err)
		http.Error(w, "Failed to create maintenance window", http.StatusInternalServerError)
		return
	}
	h.setFlash(w, "Maintenance window created")
	h.maintenanceRedirect(w, r)
}

func (h *Handler) MaintenanceUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.maintenanceRedirect(w, r)
		return
	}
	mw, err := h.parseMaintenanceForm(r)
	if err != nil {
		h.setError(w, err.Error())
		h.maintenanceRedirect(w, r)
		return
	}
	mw.ID = id
	if err := validate.ValidateMaintenanceWindow(mw); err != nil {
		h.setError(w, err.Error())
		h.maintenanceRedirect(w, r)
		return
	}
	_, err = h.store.UpdateMaintenanceWindowWithActions(r.Context(), id, func(current *storage.MaintenanceWindow) (*storage.MaintenanceWindow, error) {
		next := *mw
		if !r.Form.Has("active") {
			next.Active = current.Active
		}
		if !r.Form.Has("monitor_ids") && !r.Form.Has("all_monitors") {
			next.MonitorIDs = current.MonitorIDs
		}
		return &next, nil
	}, storage.MaintenanceUpdateAccess{Update: httputil.MonitorAccess(r.Context(), "maintenance.update"), Toggle: httputil.MonitorAccess(r.Context(), "maintenance.toggle")})
	if err != nil {
		if maintenanceMutationMissing(w, r, err) {
			return
		}
		h.logger.Error("web: update maintenance", "error", err)
		http.Error(w, "Failed to update maintenance window", http.StatusInternalServerError)
		return
	}
	h.setFlash(w, "Maintenance window updated")
	h.maintenanceRedirect(w, r)
}

func (h *Handler) MaintenanceDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.maintenanceRedirect(w, r)
		return
	}
	if err := h.store.DeleteMaintenanceWindow(r.Context(), id, httputil.MonitorAccess(r.Context(), "maintenance.delete")...); err != nil {
		if maintenanceMutationMissing(w, r, err) {
			return
		}
		h.logger.Error("web: delete maintenance", "error", err)
		http.Error(w, "Failed to delete maintenance window", http.StatusInternalServerError)
		return
	}
	h.setFlash(w, "Maintenance window deleted")
	h.maintenanceRedirect(w, r)
}

func (h *Handler) MaintenanceToggle(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.maintenanceRedirect(w, r)
		return
	}
	mw, err := h.store.FlipMaintenanceWindow(r.Context(), id, httputil.MonitorAccess(r.Context(), "maintenance.toggle"))
	if err != nil {
		if maintenanceMutationMissing(w, r, err) {
			return
		}
		h.logger.Error("web: toggle maintenance", "error", err)
		http.Error(w, "Failed to toggle maintenance window", http.StatusInternalServerError)
		return
	}
	if mw.Active {
		h.setFlash(w, "Maintenance started")
		h.notifyMaintenance(r.Context(), mw, "started")
	} else {
		h.setFlash(w, "Maintenance stopped")
		h.notifyMaintenance(r.Context(), mw, "ended")
	}

	h.maintenanceRedirect(w, r)
}

func (h *Handler) maintenanceRedirect(w http.ResponseWriter, r *http.Request) {
	lp := h.newLayoutParams(r, "", "")
	if lp.HasCapability("maintenance.read") {
		h.redirect(w, r, "/maintenance")
		return
	}
	lp.BasePath = ""
	h.redirect(w, r, lp.HomePath())
}

func maintenanceMutationMissing(w http.ResponseWriter, r *http.Request, err error) bool {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return true
	}
	return false
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
