package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/sla"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) SLAReport(w http.ResponseWriter, r *http.Request) {
	year, month, err := sla.ParsePeriod(r.URL.Query().Get("period"), time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	entries, err := sla.ComputeReport(r.Context(), h.store, year, month)
	if err != nil {
		h.logger.Error("web: compute sla report", "error", err)
		http.Error(w, "failed to compute SLA report", http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []*sla.ReportEntry{}
	}

	period := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)

	lp := h.newLayoutParams(r, "SLA Report", "sla")
	h.renderComponent(w, r, views.SLAReportPage(views.SLAReportParams{
		LayoutParams: lp,
		Entries:      entries,
		Period:       period,
	}))
}

func (h *Handler) SLAExport(w http.ResponseWriter, r *http.Request) {
	year, month, err := sla.ParsePeriod(r.URL.Query().Get("period"), time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	entries, err := sla.ComputeReport(r.Context(), h.store, year, month)
	if err != nil {
		h.logger.Error("web: export sla report", "error", err)
		http.Error(w, "failed to compute SLA report", http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []*sla.ReportEntry{}
	}

	period := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=sla-report-"+period+".json")
	json.NewEncoder(w).Encode(map[string]any{
		"period":  period,
		"entries": entries,
	})
}
