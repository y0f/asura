package web

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/incident"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	const perPage = 10

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	typeFilter := r.URL.Query().Get("type")
	if !validate.ValidMonitorTypes[typeFilter] {
		typeFilter = ""
	}

	up, down, degraded, paused, err := h.store.CountMonitorsByStatus(ctx, httputil.MonitorAccess(ctx, "monitors.read")...)
	if err != nil {
		h.logger.Error("web: count monitors by status", "error", err)
		http.Error(w, "Failed to load dashboard", http.StatusInternalServerError)
		return
	}
	total := up + down + degraded + paused

	displayMonitors, page, totalPages, err := h.loadMonitorPage(ctx, typeFilter, page, perPage)
	if err != nil {
		h.logger.Error("web: list dashboard monitors", "error", err)
		http.Error(w, "Failed to load dashboard", http.StatusInternalServerError)
		return
	}

	var incidentList []*storage.Incident
	if httputil.GetAPIKey(ctx).HasCapability("incidents.read") {
		incidentList, err = h.loadOpenIncidents(ctx)
		if err != nil {
			h.logger.Error("web: list dashboard incidents", "error", err)
			http.Error(w, "Failed to load dashboard", http.StatusInternalServerError)
			return
		}
	}

	responseTimes, err := h.store.GetLatestResponseTimes(ctx, httputil.MonitorAccess(ctx, "monitors.read")...)
	if err != nil {
		h.logger.Error("web: dashboard response times", "error", err)
		http.Error(w, "Failed to load dashboard", http.StatusInternalServerError)
		return
	}
	if responseTimes == nil {
		responseTimes = make(map[int64]int64)
	}

	monitorIDs := make([]int64, len(displayMonitors))
	for i, m := range displayMonitors {
		monitorIDs[i] = m.ID
	}
	sparklines, err := h.store.GetMonitorSparklines(ctx, monitorIDs, 20, httputil.MonitorAccess(ctx, "monitors.read")...)
	if err != nil {
		h.logger.Error("web: dashboard sparklines", "error", err)
		http.Error(w, "Failed to load dashboard", http.StatusInternalServerError)
		return
	}
	if sparklines == nil {
		sparklines = make(map[int64][]*storage.SparklinePoint)
	}

	lp := h.newLayoutParams(r, "Dashboard", "dashboard")
	h.renderComponent(w, r, views.DashboardPage(views.DashboardParams{
		LayoutParams:  lp,
		Monitors:      displayMonitors,
		Incidents:     incidentList,
		ResponseTimes: responseTimes,
		Sparklines:    sparklines,
		Total:         int(total),
		Up:            int(up),
		Down:          int(down),
		Degraded:      int(degraded),
		Paused:        int(paused),
		OpenIncidents: len(incidentList),
		Page:          page,
		TotalPages:    totalPages,
		Filter:        typeFilter,
	}))
}

func (h *Handler) loadMonitorPage(ctx context.Context, typeFilter string, page, perPage int) ([]*storage.Monitor, int, int, error) {
	result, err := h.store.ListMonitors(ctx, storage.MonitorListFilter{Type: typeFilter, Sort: "status", Access: httputil.MonitorAccess(ctx, "monitors.read")}, storage.Pagination{Page: page, PerPage: perPage})
	if err != nil {
		return nil, page, 1, err
	}
	if result == nil {
		return nil, page, 1, fmt.Errorf("missing monitor result")
	}
	ml, ok := result.Data.([]*storage.Monitor)
	if !ok {
		return nil, page, 1, fmt.Errorf("unexpected monitor result")
	}
	totalPages := result.TotalPages
	if totalPages < 1 {
		totalPages = 1
	}
	return ml, result.Page, totalPages, nil
}

func (h *Handler) loadOpenIncidents(ctx context.Context) ([]*storage.Incident, error) {
	result, err := h.store.ListIncidentsFiltered(ctx, storage.IncidentListFilter{Status: incident.StatusOpen, Access: httputil.MonitorAccess(ctx, "incidents.read"), MonitorReadAccess: httputil.MonitorAccess(ctx, "monitors.read")}, storage.Pagination{Page: 1, PerPage: 10})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("missing incident result")
	}
	il, ok := result.Data.([]*storage.Incident)
	if !ok {
		return nil, fmt.Errorf("unexpected incident result")
	}
	return il, nil
}
