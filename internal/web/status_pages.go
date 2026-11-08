package web

import (
	"cmp"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) StatusPages(w http.ResponseWriter, r *http.Request) {
	pages, err := h.store.ListStatusPages(r.Context())
	if err != nil {
		h.logger.Error("web: list status pages", "error", err)
		http.Error(w, "Failed to load status pages", http.StatusInternalServerError)
		return
	}

	lp := h.newLayoutParams(r, "Status Pages", "status-pages")
	h.renderComponent(w, r, views.StatusPageListPage(views.StatusPageListParams{
		LayoutParams: lp,
		Pages:        pages,
	}))
}

func (h *Handler) StatusPageForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var sp *storage.StatusPage
	var pageMonitors []storage.StatusPageMonitor

	idStr := r.PathValue("id")
	if idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			h.setError(w, "Invalid status page ID")
			h.redirect(w, r, "/status-pages")
			return
		}
		sp, err = h.store.GetStatusPage(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				h.setError(w, "Status page not found")
				h.redirect(w, r, "/status-pages")
				return
			}
			h.logger.Error("web: get status page", "error", err)
			h.setError(w, "Failed to load status page")
			h.redirect(w, r, "/status-pages")
			return
		}
		pageMonitors, err = h.store.ListStatusPageMonitors(ctx, id)
		if err != nil {
			h.logger.Error("web: list status page monitors", "error", err)
			http.Error(w, "Failed to load status page monitors", http.StatusInternalServerError)
			return
		}
	}

	allMonitors, err := h.store.ListMonitors(ctx, storage.MonitorListFilter{Access: httputil.MonitorAccess(ctx, "monitors.read")}, storage.Pagination{Page: 1, PerPage: 10000})
	if err != nil {
		h.logger.Error("web: list monitors for status page form", "error", err)
		http.Error(w, "Failed to load monitor choices", http.StatusInternalServerError)
		return
	}

	var monitors []*storage.Monitor
	if allMonitors != nil {
		monitors, _ = allMonitors.Data.([]*storage.Monitor)
	}
	if monitors == nil {
		monitors = []*storage.Monitor{}
	}

	visible := make(map[int64]bool, len(monitors))
	key := httputil.GetAPIKey(r.Context())
	for _, m := range monitors {
		visible[m.ID] = sp == nil || !sp.ShowIncidents || (key != nil && key.AllowsMonitor("incidents.read", m.ID, m.GroupID))
	}
	canManageMonitors := true
	for _, pm := range pageMonitors {
		if !visible[pm.MonitorID] {
			canManageMonitors = false
			break
		}
	}
	if !canManageMonitors {
		monitors = nil
		pageMonitors = nil
	}
	assignedSet := make(map[int64]bool, len(pageMonitors))
	assignedData := make(map[int64]storage.StatusPageMonitor, len(pageMonitors))
	for _, pm := range pageMonitors {
		assignedSet[pm.MonitorID] = true
		assignedData[pm.MonitorID] = pm
	}

	lp := h.newLayoutParams(r, "Status Page", "status-pages")
	h.renderComponent(w, r, views.StatusPageFormPage(views.StatusPageFormParams{
		LayoutParams:      lp,
		StatusPage:        sp,
		Monitors:          monitors,
		Assigned:          assignedSet,
		AssignedData:      assignedData,
		CanManageMonitors: canManageMonitors,
	}))
}

func (h *Handler) StatusPageCreate(w http.ResponseWriter, r *http.Request) {
	h.saveStatusPage(w, r, 0)
}

func (h *Handler) StatusPageUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		http.Error(w, "Invalid status page ID", http.StatusBadRequest)
		return
	}
	h.saveStatusPage(w, r, id)
}

func (h *Handler) saveStatusPage(w http.ResponseWriter, r *http.Request, id int64) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	input, err := parseStatusPageForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := httputil.GetAPIKey(r.Context())
	_, err = h.store.SaveStatusPage(r.Context(), id, func(current *storage.StatusPage) (*storage.StatusPage, *[]storage.StatusPageMonitor, error) {
		return input.Apply(current, key != nil && key.SuperAdmin)
	}, storage.StatusPageAccess{Monitors: httputil.MonitorAccess(r.Context(), "monitors.read"), Incidents: httputil.MonitorAccess(r.Context(), "incidents.read")})
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
		case errors.Is(err, storage.ErrStatusPageSlugConflict):
			http.Error(w, err.Error(), http.StatusConflict)
		case errors.Is(err, storage.ErrStatusPageMonitorAccess), errors.Is(err, storage.ErrStatusPageIncidentAccess), errors.Is(err, httputil.ErrStatusPageAnalytics):
			http.Error(w, err.Error(), http.StatusForbidden)
		case errors.Is(err, storage.ErrInvalidStatusPageMonitors), errors.Is(err, httputil.ErrInvalidStatusPage):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			h.logger.Error("web: save status page", "error", err)
			http.Error(w, "Failed to save status page", http.StatusInternalServerError)
		}
		return
	}
	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}
	if id == 0 {
		h.setFlash(w, "Status page created")
	} else {
		h.setFlash(w, "Status page updated")
	}
	h.redirect(w, r, h.statusPageReturnPath(r))
}

func parseStatusPageForm(r *http.Request) (httputil.StatusPageInput, error) {
	text := func(name string) *string { value := strings.TrimSpace(r.FormValue(name)); return &value }
	flag := func(name string) *bool { value := r.FormValue(name) == "on"; return &value }
	input := httputil.StatusPageInput{
		Title: text("title"), Slug: text("slug"), Description: text("description"),
		Enabled: flag("enabled"), APIEnabled: flag("api_enabled"), ShowIncidents: flag("show_incidents"),
		CustomCSS: text("custom_css"), LogoURL: text("logo_url"), FaviconURL: text("favicon_url"), CustomHeaderHTML: text("custom_header_html"),
	}
	order := 0
	if value := r.FormValue("sort_order"); value != "" {
		var err error
		order, err = strconv.Atoi(value)
		if err != nil || order < 0 {
			return input, fmt.Errorf("sort order must be a nonnegative number")
		}
	}
	input.SortOrder = &order
	if _, present := r.Form["analytics_script"]; present {
		input.AnalyticsScript = text("analytics_script")
	}
	if password := r.FormValue("password"); password != "" {
		input.Password = &password
	} else if r.FormValue("clear_password") == "on" {
		empty := ""
		input.Password = &empty
	}
	members, err := parseStatusPageMonitors(r)
	input.Monitors = members
	return input, err
}

func (h *Handler) StatusPageDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/status-pages")
		return
	}
	if err := h.store.DeleteStatusPage(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("web: delete status page", "error", err)
		http.Error(w, "Failed to delete status page", http.StatusInternalServerError)
		return
	}
	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}
	h.setFlash(w, "Status page deleted")
	h.redirect(w, r, h.statusPageReturnPath(r))
}

func parseStatusPageMonitors(r *http.Request) (*[]storage.StatusPageMonitor, error) {
	present := r.FormValue("monitors_present") == "1"
	result := []storage.StatusPageMonitor{}
	for key, vals := range r.Form {
		if !strings.HasPrefix(key, "monitor_") || !strings.HasSuffix(key, "_enabled") {
			continue
		}
		present = true
		if len(vals) != 1 || vals[0] != "on" {
			return nil, fmt.Errorf("invalid monitor selection")
		}
		idStr := strings.TrimSuffix(strings.TrimPrefix(key, "monitor_"), "_enabled")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("invalid monitor selection")
		}
		order := 0
		if value := r.FormValue("monitor_" + idStr + "_sort"); value != "" {
			order, err = strconv.Atoi(value)
			if err != nil || order < 0 {
				return nil, fmt.Errorf("monitor sort order must be a nonnegative number")
			}
		}
		result = append(result, storage.StatusPageMonitor{MonitorID: id, SortOrder: order, GroupName: strings.TrimSpace(r.FormValue("monitor_" + idStr + "_group"))})
	}
	if !present {
		return nil, nil
	}
	slices.SortFunc(result, func(a, b storage.StatusPageMonitor) int { return cmp.Compare(a.MonitorID, b.MonitorID) })
	return &result, nil
}

func (h *Handler) statusPageReturnPath(r *http.Request) string {
	lp := h.newLayoutParams(r, "", "")
	if lp.Can("status_pages.read") {
		return "/status-pages"
	}
	lp.BasePath = ""
	return lp.HomePath()
}
