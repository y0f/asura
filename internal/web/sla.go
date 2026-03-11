package web

import (
	"net/http"
	"time"

	"github.com/y0f/asura/internal/sla"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) SLAReport(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	year := now.Year()
	month := now.Month()

	if p := r.URL.Query().Get("period"); p != "" {
		t, err := time.Parse("2006-01", p)
		if err == nil {
			year = t.Year()
			month = t.Month()
		}
	}

	entries, err := sla.ComputeReport(r.Context(), h.store, year, month)
	if err != nil {
		h.logger.Error("web: compute sla report", "error", err)
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
