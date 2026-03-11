package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

// ExportData is the top-level export format.
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

type ImportStats struct {
	Groups      int `json:"groups_created"`
	Proxies     int `json:"proxies_created"`
	Channels    int `json:"channels_created"`
	Monitors    int `json:"monitors_created"`
	Maintenance int `json:"maintenance_created"`
	StatusPages int `json:"status_pages_created"`
	Skipped     int `json:"skipped"`
	Errors      int `json:"errors"`
}

// BuildExportData assembles a full configuration export from the store.
func BuildExportData(ctx context.Context, store storage.Store, redact bool) (*ExportData, error) {
	groups, err := store.ListMonitorGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	groupMap := make(map[int64]string, len(groups))
	for _, g := range groups {
		groupMap[g.ID] = g.Name
	}

	proxies, err := store.ListProxies(ctx)
	if err != nil {
		return nil, fmt.Errorf("list proxies: %w", err)
	}
	proxyMap := make(map[int64]string, len(proxies))
	for _, p := range proxies {
		proxyMap[p.ID] = p.Name
	}

	channels, err := store.ListNotificationChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	channelMap := make(map[int64]string, len(channels))
	for _, ch := range channels {
		channelMap[ch.ID] = ch.Name
	}

	result, err := store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10000})
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	monitors := result.Data.([]*storage.Monitor)

	exportMonitors := buildExportMonitors(ctx, store, monitors, groupMap, proxyMap, channelMap)
	exportPages := buildExportStatusPages(ctx, store, monitors)
	exportProxies := buildExportProxies(proxies, redact)
	exportChannels := buildExportChannels(channels, redact)
	exportGroups := buildExportGroups(groups)

	mw, _ := store.ListMaintenanceWindows(ctx)
	exportMW := buildExportMaintenance(mw)

	return &ExportData{
		Version:              1,
		ExportedAt:           time.Now().UTC(),
		Monitors:             exportMonitors,
		NotificationChannels: exportChannels,
		MonitorGroups:        exportGroups,
		MaintenanceWindows:   exportMW,
		Proxies:              exportProxies,
		StatusPages:          exportPages,
	}, nil
}

func buildExportMonitors(ctx context.Context, store storage.Store, monitors []*storage.Monitor,
	groupMap, proxyMap, channelMap map[int64]string) []ExportMonitor {
	monIDs := make([]int64, len(monitors))
	for i, m := range monitors {
		monIDs[i] = m.ID
	}
	tagMap, _ := store.GetMonitorTagsBatch(ctx, monIDs)

	var out []ExportMonitor
	for _, m := range monitors {
		em := ExportMonitor{
			Name:             m.Name,
			Description:      m.Description,
			Type:             m.Type,
			Target:           m.Target,
			Interval:         m.Interval,
			Timeout:          m.Timeout,
			Enabled:          m.Enabled,
			Settings:         m.Settings,
			Assertions:       m.Assertions,
			TrackChanges:     m.TrackChanges,
			FailureThreshold: m.FailureThreshold,
			SuccessThreshold: m.SuccessThreshold,
			UpsideDown:       m.UpsideDown,
			ResendInterval:   m.ResendInterval,
			SLATarget:        m.SLATarget,
		}
		if m.GroupID != nil {
			em.GroupName = groupMap[*m.GroupID]
		}
		if m.ProxyID != nil {
			em.ProxyName = proxyMap[*m.ProxyID]
		}
		chIDs, _ := store.GetMonitorNotificationChannelIDs(ctx, m.ID)
		for _, chID := range chIDs {
			if name, ok := channelMap[chID]; ok {
				em.NotificationChannelNames = append(em.NotificationChannelNames, name)
			}
		}
		for _, mt := range tagMap[m.ID] {
			em.Tags = append(em.Tags, ExportMonitorTag{
				TagName: mt.Name,
				Color:   mt.Color,
				Value:   mt.Value,
			})
		}
		out = append(out, em)
	}
	return out
}

func buildExportStatusPages(ctx context.Context, store storage.Store, monitors []*storage.Monitor) []ExportStatusPage {
	monIDToName := make(map[int64]string, len(monitors))
	for _, m := range monitors {
		monIDToName[m.ID] = m.Name
	}

	statusPages, _ := store.ListStatusPages(ctx)
	var out []ExportStatusPage
	for _, sp := range statusPages {
		ep := ExportStatusPage{
			Slug:          sp.Slug,
			Title:         sp.Title,
			Description:   sp.Description,
			CustomCSS:     sp.CustomCSS,
			ShowIncidents: sp.ShowIncidents,
			Enabled:       sp.Enabled,
			APIEnabled:    sp.APIEnabled,
			SortOrder:     sp.SortOrder,
		}
		spMons, _ := store.ListStatusPageMonitors(ctx, sp.ID)
		for _, spm := range spMons {
			ep.Monitors = append(ep.Monitors, ExportStatusPageMon{
				MonitorName: monIDToName[spm.MonitorID],
				SortOrder:   spm.SortOrder,
				GroupName:   spm.GroupName,
			})
		}
		out = append(out, ep)
	}
	return out
}

func buildExportProxies(proxies []*storage.Proxy, redact bool) []ExportProxy {
	var out []ExportProxy
	for _, p := range proxies {
		ep := ExportProxy{
			Name:     p.Name,
			Protocol: p.Protocol,
			Host:     p.Host,
			Port:     p.Port,
			Enabled:  p.Enabled,
		}
		if !redact {
			ep.AuthUser = p.AuthUser
			ep.AuthPass = p.AuthPass
		}
		out = append(out, ep)
	}
	return out
}

func buildExportChannels(channels []*storage.NotificationChannel, redact bool) []*storage.NotificationChannel {
	out := make([]*storage.NotificationChannel, len(channels))
	for i, ch := range channels {
		settings := ch.Settings
		if redact {
			settings = json.RawMessage(`{}`)
		}
		out[i] = &storage.NotificationChannel{
			Name:     ch.Name,
			Type:     ch.Type,
			Enabled:  ch.Enabled,
			Settings: settings,
			Events:   ch.Events,
		}
	}
	return out
}

func buildExportGroups(groups []*storage.MonitorGroup) []*storage.MonitorGroup {
	out := make([]*storage.MonitorGroup, len(groups))
	for i, g := range groups {
		out[i] = &storage.MonitorGroup{
			Name:      g.Name,
			SortOrder: g.SortOrder,
		}
	}
	return out
}

func buildExportMaintenance(mw []*storage.MaintenanceWindow) []*storage.MaintenanceWindow {
	out := make([]*storage.MaintenanceWindow, len(mw))
	for i, m := range mw {
		out[i] = &storage.MaintenanceWindow{
			Name:       m.Name,
			MonitorIDs: m.MonitorIDs,
			StartTime:  m.StartTime,
			EndTime:    m.EndTime,
			Recurring:  m.Recurring,
		}
	}
	return out
}

// importCtx holds name-to-ID maps used during import to resolve references.
type importCtx struct {
	store           storage.Store
	logger          *slog.Logger
	mode            string
	groupNameToID   map[string]int64
	proxyNameToID   map[string]int64
	channelNameToID map[string]int64
	monitorNameToID map[string]int64
}

// RunImport imports configuration data into the store.
func RunImport(ctx context.Context, store storage.Store, logger *slog.Logger, data *ExportData, mode string) *ImportStats {
	stats := &ImportStats{}
	ic := &importCtx{
		store:           store,
		logger:          logger,
		mode:            mode,
		groupNameToID:   make(map[string]int64),
		proxyNameToID:   make(map[string]int64),
		channelNameToID: make(map[string]int64),
		monitorNameToID: make(map[string]int64),
	}

	importGroups(ctx, ic, data.MonitorGroups, stats)
	importProxies(ctx, ic, data.Proxies, stats)
	importChannels(ctx, ic, data.NotificationChannels, stats)
	importMonitors(ctx, ic, data.Monitors, stats)
	importMaintenance(ctx, ic, data.MaintenanceWindows, stats)
	importStatusPages(ctx, ic, data.StatusPages, stats)

	return stats
}

func importGroups(ctx context.Context, ic *importCtx, groups []*storage.MonitorGroup, stats *ImportStats) {
	existing, err := ic.store.ListMonitorGroups(ctx)
	if err != nil {
		ic.logger.Error("import: list groups", "error", err)
		stats.Errors += len(groups)
		return
	}
	for _, g := range existing {
		ic.groupNameToID[g.Name] = g.ID
	}
	for _, g := range groups {
		if _, exists := ic.groupNameToID[g.Name]; exists && ic.mode == "merge" {
			stats.Skipped++
			continue
		}
		ng := &storage.MonitorGroup{Name: g.Name, SortOrder: g.SortOrder}
		if err := ic.store.CreateMonitorGroup(ctx, ng); err != nil {
			stats.Errors++
			continue
		}
		ic.groupNameToID[ng.Name] = ng.ID
		stats.Groups++
	}
}

func importProxies(ctx context.Context, ic *importCtx, proxies []ExportProxy, stats *ImportStats) {
	existing, err := ic.store.ListProxies(ctx)
	if err != nil {
		ic.logger.Error("import: list proxies", "error", err)
		stats.Errors += len(proxies)
		return
	}
	for _, p := range existing {
		ic.proxyNameToID[p.Name] = p.ID
	}
	for _, p := range proxies {
		if _, exists := ic.proxyNameToID[p.Name]; exists && ic.mode == "merge" {
			stats.Skipped++
			continue
		}
		np := &storage.Proxy{
			Name: p.Name, Protocol: p.Protocol, Host: p.Host, Port: p.Port,
			AuthUser: p.AuthUser, AuthPass: p.AuthPass, Enabled: p.Enabled,
		}
		if err := validate.ValidateProxy(np); err != nil {
			stats.Errors++
			continue
		}
		if err := ic.store.CreateProxy(ctx, np); err != nil {
			stats.Errors++
			continue
		}
		ic.proxyNameToID[np.Name] = np.ID
		stats.Proxies++
	}
}

func importChannels(ctx context.Context, ic *importCtx, channels []*storage.NotificationChannel, stats *ImportStats) {
	existing, err := ic.store.ListNotificationChannels(ctx)
	if err != nil {
		ic.logger.Error("import: list channels", "error", err)
		stats.Errors += len(channels)
		return
	}
	for _, ch := range existing {
		ic.channelNameToID[ch.Name] = ch.ID
	}
	for _, ch := range channels {
		if _, exists := ic.channelNameToID[ch.Name]; exists && ic.mode == "merge" {
			stats.Skipped++
			continue
		}
		nch := &storage.NotificationChannel{
			Name: ch.Name, Type: ch.Type, Enabled: ch.Enabled,
			Settings: ch.Settings, Events: ch.Events,
		}
		if err := ic.store.CreateNotificationChannel(ctx, nch); err != nil {
			stats.Errors++
			continue
		}
		ic.channelNameToID[nch.Name] = nch.ID
		stats.Channels++
	}
}

func importMonitors(ctx context.Context, ic *importCtx, monitors []ExportMonitor, stats *ImportStats) {
	existing, err := ic.store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10000})
	if err != nil {
		ic.logger.Error("import: list monitors", "error", err)
		stats.Errors += len(monitors)
		return
	}
	if existing != nil {
		for _, m := range existing.Data.([]*storage.Monitor) {
			ic.monitorNameToID[m.Name] = m.ID
		}
	}
	for _, em := range monitors {
		if _, exists := ic.monitorNameToID[em.Name]; exists && ic.mode == "merge" {
			stats.Skipped++
			continue
		}
		if importSingleMonitor(ctx, ic, &em) {
			stats.Monitors++
		} else {
			stats.Errors++
		}
	}
}

func importSingleMonitor(ctx context.Context, ic *importCtx, em *ExportMonitor) bool {
	m := &storage.Monitor{
		Name: em.Name, Description: em.Description, Type: em.Type, Target: em.Target,
		Interval: em.Interval, Timeout: em.Timeout, Enabled: em.Enabled,
		Settings: em.Settings, Assertions: em.Assertions,
		TrackChanges: em.TrackChanges, FailureThreshold: em.FailureThreshold,
		SuccessThreshold: em.SuccessThreshold, UpsideDown: em.UpsideDown,
		ResendInterval: em.ResendInterval, SLATarget: em.SLATarget,
	}
	if em.GroupName != "" {
		if gid, ok := ic.groupNameToID[em.GroupName]; ok {
			m.GroupID = &gid
		}
	}
	if em.ProxyName != "" {
		if pid, ok := ic.proxyNameToID[em.ProxyName]; ok {
			m.ProxyID = &pid
		}
	}
	if err := validate.ValidateMonitor(m); err != nil {
		return false
	}
	if err := ic.store.CreateMonitor(ctx, m); err != nil {
		return false
	}
	ic.monitorNameToID[m.Name] = m.ID

	var chIDs []int64
	for _, chName := range em.NotificationChannelNames {
		if chID, ok := ic.channelNameToID[chName]; ok {
			chIDs = append(chIDs, chID)
		}
	}
	if len(chIDs) > 0 {
		if err := ic.store.SetMonitorNotificationChannels(ctx, m.ID, chIDs); err != nil {
			ic.logger.Error("import: set monitor channels", "monitor", m.Name, "error", err)
		}
	}

	if len(em.Tags) > 0 {
		importMonitorTags(ctx, ic, m.ID, em.Tags)
	}
	return true
}

func importMonitorTags(ctx context.Context, ic *importCtx, monitorID int64, tags []ExportMonitorTag) {
	allTags, err := ic.store.ListTags(ctx)
	if err != nil {
		ic.logger.Error("import: list tags", "error", err)
		return
	}
	tagNameToID := make(map[string]int64, len(allTags))
	for _, t := range allTags {
		tagNameToID[t.Name] = t.ID
	}

	var monTags []storage.MonitorTag
	for _, et := range tags {
		tid, ok := tagNameToID[et.TagName]
		if !ok {
			newTag := &storage.Tag{Name: et.TagName, Color: et.Color}
			if newTag.Color == "" {
				newTag.Color = "#808080"
			}
			if err := ic.store.CreateTag(ctx, newTag); err != nil {
				continue
			}
			tid = newTag.ID
			tagNameToID[et.TagName] = tid
		}
		monTags = append(monTags, storage.MonitorTag{TagID: tid, Value: et.Value})
	}
	if len(monTags) > 0 {
		if err := ic.store.SetMonitorTags(ctx, monitorID, monTags); err != nil {
			ic.logger.Error("import: set monitor tags", "monitor_id", monitorID, "error", err)
		}
	}
}

func importMaintenance(ctx context.Context, ic *importCtx, windows []*storage.MaintenanceWindow, stats *ImportStats) {
	for _, mw := range windows {
		nmw := &storage.MaintenanceWindow{
			Name: mw.Name, MonitorIDs: mw.MonitorIDs,
			StartTime: mw.StartTime, EndTime: mw.EndTime, Recurring: mw.Recurring,
		}
		if err := ic.store.CreateMaintenanceWindow(ctx, nmw); err != nil {
			stats.Errors++
			continue
		}
		stats.Maintenance++
	}
}

func importStatusPages(ctx context.Context, ic *importCtx, pages []ExportStatusPage, stats *ImportStats) {
	for _, esp := range pages {
		nsp := &storage.StatusPage{
			Slug: esp.Slug, Title: esp.Title, Description: esp.Description,
			CustomCSS: esp.CustomCSS, ShowIncidents: esp.ShowIncidents,
			Enabled: esp.Enabled, APIEnabled: esp.APIEnabled, SortOrder: esp.SortOrder,
		}
		if err := validate.ValidateStatusPage(nsp); err != nil {
			stats.Errors++
			continue
		}
		if err := ic.store.CreateStatusPage(ctx, nsp); err != nil {
			stats.Errors++
			continue
		}
		var spMons []storage.StatusPageMonitor
		for _, spm := range esp.Monitors {
			if mid, ok := ic.monitorNameToID[spm.MonitorName]; ok {
				spMons = append(spMons, storage.StatusPageMonitor{
					PageID: nsp.ID, MonitorID: mid,
					SortOrder: spm.SortOrder, GroupName: spm.GroupName,
				})
			}
		}
		if len(spMons) > 0 {
			if err := ic.store.SetStatusPageMonitors(ctx, nsp.ID, spMons); err != nil {
				ic.logger.Error("import: set status page monitors", "page", nsp.Slug, "error", err)
			}
		}
		stats.StatusPages++
	}
}

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	redact := r.URL.Query().Get("redact_secrets") == "true"

	data, err := BuildExportData(r.Context(), h.store, redact)
	if err != nil {
		writeError(w, 500, "failed to build export data")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="asura-export.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(data)

	h.audit(r, "export", "config", 0, "")
}

func (h *Handler) Import(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "replace" {
		writeError(w, 400, "mode must be 'merge' or 'replace'")
		return
	}

	var data ExportData
	if err := readJSON(r, &data); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if data.Version != 1 {
		writeError(w, 400, "unsupported export version")
		return
	}

	stats := RunImport(r.Context(), h.store, h.logger, &data, mode)

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}
	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}

	h.audit(r, "import", "config", 0, fmt.Sprintf("mode=%s", mode))
	writeJSON(w, 200, stats)
}
