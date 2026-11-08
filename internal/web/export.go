package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/y0f/asura/v2/internal/api"
	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/web/views"
)

func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	key := httputil.GetAPIKey(r.Context())
	if !key.HasPermission("settings.read") {
		http.Error(w, "Settings access is required", http.StatusForbidden)
		return
	}
	var dbSize, monitorCount int64
	if key.HasPermission("database.read") {
		var err error
		dbSize, err = h.store.DBSize()
		if err != nil {
			h.logger.Error("web: settings database size", "error", err)
			http.Error(w, "Failed to read database information", http.StatusInternalServerError)
			return
		}
	}
	if key.HasCapability("monitors.read") {
		filter := storage.MonitorListFilter{Access: httputil.MonitorAccess(r.Context(), "monitors.read")}
		result, err := h.store.ListMonitors(r.Context(), filter, storage.Pagination{Page: 1, PerPage: 1})
		if err != nil {
			h.logger.Error("web: settings monitor count", "error", err)
			http.Error(w, "Failed to read monitor information", http.StatusInternalServerError)
			return
		}
		monitorCount = result.Total
	}
	lp := h.newLayoutParams(r, "Settings", "settings")
	uptime := time.Since(h.startTime).Truncate(time.Second).String()
	h.renderComponent(w, r, views.SettingsPage(views.SettingsParams{
		LayoutParams:  lp,
		DBSizeBytes:   dbSize,
		AppVersion:    h.version,
		Uptime:        uptime,
		MonitorCount:  monitorCount,
		WorkerCount:   h.cfg.Monitor.Workers,
		RetentionDays: h.cfg.Database.RetentionDays,
		EncryptionOn:  h.cfg.Database.EncryptionKey != "",
	}))
}

func (h *Handler) DBVacuum(w http.ResponseWriter, r *http.Request) {
	k := httputil.GetAPIKey(r.Context())
	if !k.HasPermission("database.maintain") {
		h.setError(w, "Vacuum requires database.maintain")
		h.redirect(w, r, "/settings")
		return
	}
	if err := h.store.Vacuum(r.Context()); err != nil {
		h.logger.Error("web: vacuum", "error", err)
		h.setError(w, "Vacuum failed. See the server log for details.")
	} else {
		h.setFlash(w, "Database vacuumed successfully")
	}
	h.redirect(w, r, "/settings")
}

func (h *Handler) ExportConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	k := httputil.GetAPIKey(r.Context())
	if k == nil || !k.SuperAdmin {
		http.Error(w, "Configuration export requires super admin access", http.StatusForbidden)
		return
	}
	redact := r.URL.Query().Get("redact_secrets") != "false"

	data, err := api.BuildExportData(r.Context(), h.store, redact)
	if err != nil {
		h.setError(w, "Failed to build export data")
		h.redirect(w, r, "/settings")
		return
	}
	body, err := api.EncodeConfigurationExport(data)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidConfiguration) {
			h.setError(w, err.Error())
		} else {
			h.setError(w, "Failed to encode the complete configuration")
		}
		h.redirect(w, r, "/settings")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="asura-export.json"`)
	w.Write(body)
}

func (h *Handler) ImportConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	k := httputil.GetAPIKey(r.Context())
	if k == nil || !k.SuperAdmin {
		http.Error(w, "Configuration import requires super admin access", http.StatusForbidden)
		return
	}

	// Bound the whole multipart request before parsing or spilling uploaded
	// files to private temporary storage. The file itself has a 32 MiB limit.
	r.Body = http.MaxBytesReader(w, r.Body, api.MaxConfigurationBytes+(1<<20))
	defer r.Body.Close()
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		h.setError(w, "Invalid configuration upload or upload exceeds the 32 MiB file limit")
		h.redirect(w, r, "/settings")
		return
	}
	defer r.MultipartForm.RemoveAll()
	if len(r.MultipartForm.Value["mode"]) > 1 || len(r.MultipartForm.Value["agent_mapping"]) > 1 || len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
		h.setError(w, "Upload exactly one configuration file with one set of import options")
		h.redirect(w, r, "/settings")
		return
	}
	mode := r.PostForm.Get("mode")
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "replace" {
		h.setError(w, "Invalid import mode")
		h.redirect(w, r, "/settings")
		return
	}

	agentIDs, err := api.ParseConfigurationAgentMapping(r.PostForm.Get("agent_mapping"))
	if err != nil {
		h.setError(w, err.Error())
		h.redirect(w, r, "/settings")
		return
	}
	file, err := r.MultipartForm.File["file"][0].Open()
	if err != nil {
		h.setError(w, "No file uploaded")
		h.redirect(w, r, "/settings")
		return
	}
	defer file.Close()

	data, err := api.DecodeConfiguration(file)
	if err != nil {
		h.setError(w, err.Error())
		h.redirect(w, r, "/settings")
		return
	}

	stats, err := api.RunImport(r.Context(), h.store, data, mode, agentIDs)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidConfiguration) {
			h.setError(w, err.Error())
		} else {
			h.logger.Error("web: import configuration rolled back", "error", err)
			h.setError(w, "Configuration import failed; no changes were saved")
		}
		h.redirect(w, r, "/settings")
		return
	}

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}
	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}

	result := views.ImportResultParams{LayoutParams: h.newLayoutParams(r, "Configuration imported", "settings"), Stats: *stats}
	result.Stats.Heartbeats = nil
	for _, heartbeat := range stats.Heartbeats {
		result.Heartbeats = append(result.Heartbeats, views.ImportedHeartbeatEnrollment{
			MonitorID: heartbeat.MonitorID, MonitorName: heartbeat.MonitorName,
			PingURL: h.cfg.ResolvedExternalURL() + "/api/v1/heartbeat/" + heartbeat.Token,
		})
	}
	h.renderComponent(w, r, views.ImportResultPage(result))
}
