package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

// ExportData is the legacy v1 format, retained only for importing old files.
type ExportData struct {
	Version              int                            `json:"version"`
	ExportedAt           time.Time                      `json:"exported_at"`
	Monitors             []ExportMonitor                `json:"monitors"`
	NotificationChannels []*storage.NotificationChannel `json:"notification_channels"`
	MonitorGroups        []*storage.MonitorGroup        `json:"monitor_groups"`
	MaintenanceWindows   []*storage.MaintenanceWindow   `json:"maintenance_windows"`
	Proxies              []ExportProxy                  `json:"proxies"`
	StatusPages          []ExportStatusPage             `json:"status_pages"`
}

type ExportMonitor struct {
	Name                     string             `json:"name"`
	Description              string             `json:"description,omitempty"`
	Type                     string             `json:"type"`
	Target                   string             `json:"target"`
	Interval                 int                `json:"interval"`
	Timeout                  int                `json:"timeout"`
	Enabled                  bool               `json:"enabled"`
	Tags                     []ExportMonitorTag `json:"tags,omitempty"`
	Settings                 json.RawMessage    `json:"settings,omitempty"`
	Assertions               json.RawMessage    `json:"assertions,omitempty"`
	TrackChanges             bool               `json:"track_changes,omitempty"`
	FailureThreshold         int                `json:"failure_threshold"`
	SuccessThreshold         int                `json:"success_threshold"`
	UpsideDown               bool               `json:"upside_down,omitempty"`
	ResendInterval           int                `json:"resend_interval,omitempty"`
	SLATarget                float64            `json:"sla_target,omitempty"`
	GroupName                string             `json:"group_name,omitempty"`
	ProxyName                string             `json:"proxy_name,omitempty"`
	NotificationChannelNames []string           `json:"notification_channel_names,omitempty"`
	AgentEnabled             bool               `json:"agent_enabled,omitempty"`
	// Legacy files used an empty list for implicit delegation. Importing into
	// v2 preserves an empty assignment; it never grants jobs to all agents.
	AgentNames []string `json:"agent_names,omitempty"`
}

type ExportMonitorTag struct {
	TagName string `json:"tag_name"`
	Color   string `json:"color,omitempty"`
	Value   string `json:"value,omitempty"`
}

type ExportProxy struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	AuthUser string `json:"auth_user,omitempty"`
	AuthPass string `json:"auth_pass,omitempty"`
	Enabled  bool   `json:"enabled"`
}

type ExportStatusPage struct {
	Slug          string                `json:"slug"`
	Title         string                `json:"title"`
	Description   string                `json:"description,omitempty"`
	CustomCSS     string                `json:"custom_css,omitempty"`
	ShowIncidents bool                  `json:"show_incidents"`
	Enabled       bool                  `json:"enabled"`
	APIEnabled    bool                  `json:"api_enabled"`
	SortOrder     int                   `json:"sort_order"`
	Monitors      []ExportStatusPageMon `json:"monitors,omitempty"`
}

type ExportStatusPageMon struct {
	MonitorName string `json:"monitor_name"`
	SortOrder   int    `json:"sort_order"`
	GroupName   string `json:"group_name,omitempty"`
}

type ImportStats = storage.ConfigurationImportResult

// BuildExportData reads a single consistent configuration snapshot without UI
// pagination limits. Complete JSON encoding happens before response headers.
func BuildExportData(ctx context.Context, store storage.Store, redact bool) (*ConfigurationFile, error) {
	snapshot, err := store.SnapshotConfiguration(ctx)
	if err != nil {
		return nil, err
	}
	return configurationFile(snapshot, redact), nil
}

func EncodeConfigurationExport(data *ConfigurationFile) ([]byte, error) {
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode configuration: %w", err)
	}
	if int64(len(body)) > MaxConfigurationBytes {
		return nil, fmt.Errorf("%w: portable configuration exceeds 32 MiB; use a full database backup", storage.ErrInvalidConfiguration)
	}
	return body, nil
}

func RunImport(ctx context.Context, store storage.Store, data *storage.ConfigurationSnapshot, mode string, agentIDs map[int64]int64) (*ImportStats, error) {
	return store.ApplyConfiguration(ctx, data, storage.ConfigurationImportOptions{
		Mode: mode, AgentIDs: agentIDs, Validate: validateImportedConfiguration,
	})
}

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	k := httputil.GetAPIKey(r.Context())
	if k == nil || !k.SuperAdmin {
		writeError(w, http.StatusForbidden, "configuration transfer requires super admin access")
		return
	}
	data, err := BuildExportData(r.Context(), h.store, r.URL.Query().Get("redact_secrets") == "true")
	if err != nil {
		h.logger.Error("snapshot configuration", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read complete configuration")
		return
	}
	body, err := EncodeConfigurationExport(data)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidConfiguration) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		} else {
			h.logger.Error("encode configuration", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to encode complete configuration")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="asura-export.json"`)
	if _, err := w.Write(body); err != nil {
		return
	}
	h.audit(r, "export", "config", 0, "")
}

func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	k := httputil.GetAPIKey(r.Context())
	if k == nil || !k.SuperAdmin {
		writeError(w, http.StatusForbidden, "configuration transfer requires super admin access")
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "replace" {
		writeError(w, http.StatusBadRequest, "mode must be merge or replace")
		return
	}
	agentIDs, err := ParseConfigurationAgentMapping(r.URL.Query().Get("agent_mapping"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxConfigurationBytes)
	defer r.Body.Close()
	data, err := DecodeConfiguration(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stats, err := RunImport(r.Context(), h.store, data, mode, agentIDs)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidConfiguration) {
			writeError(w, http.StatusBadRequest, err.Error())
		} else {
			h.logger.Error("import configuration rolled back", "error", err)
			writeError(w, http.StatusInternalServerError, "configuration import failed; no changes were saved")
		}
		return
	}
	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}
	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}
	h.audit(r, "import", "config", 0, fmt.Sprintf("mode=%s", mode))
	writeJSON(w, http.StatusOK, stats)
}
