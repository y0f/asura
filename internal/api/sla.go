package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/y0f/asura/v3/internal/httputil"
	"github.com/y0f/asura/v3/internal/sla"
)

func (h *Handler) MonitorSLA(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	mon, ok := h.requireMonitor(w, r, id, "sla.read")
	if !ok {
		return
	}

	if !mon.HasSampledMetrics() {
		writeError(w, http.StatusBadRequest, "sample-based SLA is unavailable for manual and heartbeat monitors")
		return
	}
	if mon.SLATarget <= 0 {
		writeError(w, http.StatusBadRequest, "monitor has no SLA target configured")
		return
	}

	status, err := sla.Compute(r.Context(), h.store, mon.ID, httputil.MonitorAccess(r.Context(), "sla.read")...)
	if err != nil {
		if errors.Is(err, sla.ErrUnavailable) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if monitorMutationError(w, err) {
			return
		}
		h.logger.Error("compute sla", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute SLA")
		return
	}

	if status.Target <= 0 {
		writeError(w, http.StatusBadRequest, "monitor has no SLA target configured")
		return
	}
	name := mon.Name
	if !httputil.GetAPIKey(r.Context()).AllowsMonitor("monitors.read", mon.ID, mon.GroupID) {
		name = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"monitor_id":   mon.ID,
		"monitor_name": name,
		"sla":          status,
	})
}

func (h *Handler) SLAReport(w http.ResponseWriter, r *http.Request) {
	year, month, err := sla.ParsePeriod(r.URL.Query().Get("period"), time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, err := sla.ComputeReport(r.Context(), h.store, year, month, httputil.MonitorAccess(r.Context(), "sla.read")...)
	if err != nil {
		h.logger.Error("compute sla report", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute SLA report")
		return
	}

	httputil.RedactSLAMonitorNames(r.Context(), entries)
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
	year, month, err := sla.ParsePeriod(r.URL.Query().Get("period"), time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	entries, err := sla.ComputeReport(r.Context(), h.store, year, month, httputil.MonitorAccess(r.Context(), "sla.read")...)
	if err != nil {
		h.logger.Error("export sla report", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to compute SLA report")
		return
	}

	httputil.RedactSLAMonitorNames(r.Context(), entries)
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
