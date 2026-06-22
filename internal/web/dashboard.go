package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

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

	up, down, degraded, paused, err := h.store.CountMonitorsByStatus(ctx)
	if err != nil {
		h.logger.Error("web: count monitors by status", "error", err)
	}
	total := up + down + degraded + paused

	displayMonitors, page, totalPages := h.loadMonitorPage(ctx, typeFilter, page, perPage)

	incidentList := h.loadOpenIncidents(ctx)

	responseTimes, _ := h.store.GetLatestResponseTimes(ctx)
	if responseTimes == nil {
		responseTimes = make(map[int64]int64)
	}

	monitorIDs := make([]int64, len(displayMonitors))
	for i, m := range displayMonitors {
		monitorIDs[i] = m.ID
	}
	sparklines, _ := h.store.GetMonitorSparklines(ctx, monitorIDs, 20)
	if sparklines == nil {
		sparklines = make(map[int64][]*storage.SparklinePoint)
	}

	now := time.Now().UTC()
	requests24h, visitors24h := h.loadRequestStats(ctx, now)

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
		Requests24h:   requests24h,
		Visitors24h:   visitors24h,
		Page:          page,
		TotalPages:    totalPages,
		Filter:        typeFilter,
	}))
}

func (h *Handler) loadMonitorPage(ctx context.Context, typeFilter string, page, perPage int) ([]*storage.Monitor, int, int) {
	result, err := h.store.ListMonitors(ctx, storage.MonitorListFilter{Type: typeFilter}, storage.Pagination{Page: page, PerPage: perPage})
	if err != nil {
		h.logger.Error("web: list monitors", "error", err)
		return nil, page, 1
	}
	if result == nil {
		return nil, page, 1
	}
	ml, _ := result.Data.([]*storage.Monitor)
	totalPages := result.TotalPages
	if totalPages < 1 {
		totalPages = 1
	}
	return ml, result.Page, totalPages
}

func (h *Handler) loadOpenIncidents(ctx context.Context) []*storage.Incident {
	result, err := h.store.ListIncidents(ctx, 0, incident.StatusOpen, "", storage.Pagination{Page: 1, PerPage: 10})
	if err != nil {
		h.logger.Error("web: list incidents", "error", err)
		return nil
	}
	if result == nil {
		return nil
	}
	il, _ := result.Data.([]*storage.Incident)
	return il
}

func (h *Handler) loadRequestStats(ctx context.Context, now time.Time) (requests, visitors int64) {
	stats, err := h.store.GetRequestLogStats(ctx, now.Add(-24*time.Hour), now)
	if err != nil {
		h.logger.Error("web: request log stats", "error", err)
		return 0, 0
	}
	if stats == nil {
		return 0, 0
	}
	return stats.TotalRequests, stats.UniqueVisitors
}
