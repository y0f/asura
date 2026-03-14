package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/api"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	lp := h.newLayoutParams(r, "Settings", "settings")
	dbSize, _ := h.store.DBSize()
	up, _, _, _, _ := h.store.CountMonitorsByStatus(r.Context())
	total := up
	if result, err := h.store.ListMonitors(r.Context(), storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 1}); err == nil {
		total = result.Total
	}
	uptime := time.Since(h.startTime).Truncate(time.Second).String()
	h.renderComponent(w, r, views.SettingsPage(views.SettingsParams{
		LayoutParams:  lp,
		DBSizeBytes:   dbSize,
		AppVersion:    h.version,
		Uptime:        uptime,
		MonitorCount:  total,
		WorkerCount:   h.cfg.Monitor.Workers,
		RetentionDays: h.cfg.Database.RetentionDays,
		EncryptionOn:  h.cfg.Database.EncryptionKey != "",
	}))
}

func (h *Handler) DBVacuum(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Vacuum(r.Context()); err != nil {
		h.logger.Error("web: vacuum", "error", err)
		h.setFlash(w, "Vacuum failed: "+err.Error())
	} else {
		h.setFlash(w, "Database vacuumed successfully")
	}
	h.redirect(w, r, "/settings")
}

func (h *Handler) ExportConfig(w http.ResponseWriter, r *http.Request) {
	redact := r.URL.Query().Get("redact_secrets") == "true"

	data, err := api.BuildExportData(r.Context(), h.store, redact)
	if err != nil {
		h.setFlash(w, "Failed to build export data")
		h.redirect(w, r, "/settings")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="asura-export.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(data)
}

func (h *Handler) ImportConfig(w http.ResponseWriter, r *http.Request) {
	k := httputil.GetAPIKey(r.Context())
	if k == nil || !k.SuperAdmin {
		h.setFlash(w, "Import requires admin access")
		h.redirect(w, r, "/settings")
		return
	}

	mode := r.FormValue("mode")
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "replace" {
		h.setFlash(w, "Invalid import mode")
		h.redirect(w, r, "/settings")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		h.setFlash(w, "No file uploaded")
		h.redirect(w, r, "/settings")
		return
	}
	defer file.Close()

	body, err := io.ReadAll(io.LimitReader(file, 10<<20))
	if err != nil {
		h.setFlash(w, "Failed to read file")
		h.redirect(w, r, "/settings")
		return
	}

	var data api.ExportData
	if err := json.Unmarshal(body, &data); err != nil {
		h.setFlash(w, "Invalid JSON file")
		h.redirect(w, r, "/settings")
		return
	}
	if data.Version != 1 {
		h.setFlash(w, "Unsupported export version")
		h.redirect(w, r, "/settings")
		return
	}

	stats := api.RunImport(r.Context(), h.store, h.logger, &data, mode)

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}
	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}

	h.setFlash(w, fmt.Sprintf("Imported: %d monitors, %d channels, %d groups, %d proxies, %d maintenance, %d status pages (%d skipped, %d errors)",
		stats.Monitors, stats.Channels, stats.Groups, stats.Proxies, stats.Maintenance, stats.StatusPages, stats.Skipped, stats.Errors))
	h.redirect(w, r, "/settings")
}
