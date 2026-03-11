package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/sla"
)

func (h *Handler) MonitorSLA(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	mon, err := h.store.GetMonitor(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "monitor not found")
		return
	}

	if mon.SLATarget <= 0 {
		writeError(w, http.StatusBadRequest, "monitor has no SLA target configured")
		return
	}

	status, err := sla.Compute(r.Context(), h.store, mon.ID, mon.SLATarget)
	if err != nil {
		h.logger.Error("compute sla", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute SLA")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"monitor_id":   mon.ID,
		"monitor_name": mon.Name,
		"sla":          status,
	})
}

func (h *Handler) SLAReport(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	year := now.Year()
	month := now.Month()

	if p := r.URL.Query().Get("period"); p != "" {
		t, err := time.Parse("2006-01", p)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid period format, use YYYY-MM")
			return
		}
		year = t.Year()
		month = t.Month()
	}

	entries, err := sla.ComputeReport(r.Context(), h.store, year, month)
	if err != nil {
		h.logger.Error("compute sla report", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute SLA report")
		return
	}

	if entries == nil {
		entries = []*sla.ReportEntry{}
	}

	breached := 0
	for _, e := range entries {
		if e.Breached {
			breached++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"period":   time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).Format("2006-01"),
		"monitors": len(entries),
		"breached": breached,
		"entries":  entries,
	})
}

func (h *Handler) SLAReportExport(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	year := now.Year()
	month := now.Month()

	if p := r.URL.Query().Get("period"); p != "" {
		t, err := time.Parse("2006-01", p)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid period format, use YYYY-MM")
			return
		}
		year = t.Year()
		month = t.Month()
	}

	entries, err := sla.ComputeReport(r.Context(), h.store, year, month)
	if err != nil {
		h.logger.Error("export sla report", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute SLA report")
		return
	}

	if entries == nil {
		entries = []*sla.ReportEntry{}
	}

	period := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
	w.Header().Set("Content-Disposition", "attachment; filename=sla-report-"+period+".json")
	writeJSON(w, http.StatusOK, map[string]any{
		"period":  period,
		"entries": entries,
	})
}

func parsePeriod(r *http.Request) (int, time.Month) {
	now := time.Now().UTC()
	if p := r.URL.Query().Get("period"); p != "" {
		t, err := time.Parse("2006-01", p)
		if err == nil {
			return t.Year(), t.Month()
		}
	}
	return now.Year(), now.Month()
}

func parseYear(r *http.Request) int {
	if y := r.URL.Query().Get("year"); y != "" {
		if v, err := strconv.Atoi(y); err == nil && v >= 2000 && v <= 2100 {
			return v
		}
	}
	return time.Now().UTC().Year()
}
