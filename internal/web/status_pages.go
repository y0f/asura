package web

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) StatusPages(w http.ResponseWriter, r *http.Request) {
	pages, err := h.store.ListStatusPages(r.Context())
	if err != nil {
		h.logger.Error("web: list status pages", "error", err)
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
		}
	}

	allMonitors, err := h.store.ListMonitors(ctx, storage.MonitorListFilter{}, storage.Pagination{Page: 1, PerPage: 10000})
	if err != nil {
		h.logger.Error("web: list monitors for status page form", "error", err)
	}

	var monitors []*storage.Monitor
	if allMonitors != nil {
		monitors, _ = allMonitors.Data.([]*storage.Monitor)
	}
	if monitors == nil {
		monitors = []*storage.Monitor{}
	}

	assignedSet := make(map[int64]bool, len(pageMonitors))
	assignedData := make(map[int64]storage.StatusPageMonitor, len(pageMonitors))
	for _, pm := range pageMonitors {
		assignedSet[pm.MonitorID] = true
		assignedData[pm.MonitorID] = pm
	}

	lp := h.newLayoutParams(r, "Status Page", "status-pages")
	h.renderComponent(w, r, views.StatusPageFormPage(views.StatusPageFormParams{
		LayoutParams: lp,
		StatusPage:   sp,
		Monitors:     monitors,
		Assigned:     assignedSet,
		AssignedData: assignedData,
	}))
}

func hashPassword(password string) string {
	h := sha256.New()
	h.Write([]byte(password))
	return hex.EncodeToString(h.Sum(nil))
}

func (h *Handler) StatusPageCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	sp := &storage.StatusPage{
		Title:            strings.TrimSpace(r.FormValue("title")),
		Slug:             r.FormValue("slug"),
		Description:      strings.TrimSpace(r.FormValue("description")),
		Enabled:          r.FormValue("enabled") == "on",
		APIEnabled:       r.FormValue("api_enabled") == "on",
		ShowIncidents:    r.FormValue("show_incidents") == "on",
		CustomCSS:        r.FormValue("custom_css"),
		LogoURL:          strings.TrimSpace(r.FormValue("logo_url")),
		FaviconURL:       strings.TrimSpace(r.FormValue("favicon_url")),
		CustomHeaderHTML: r.FormValue("custom_header_html"),
		AnalyticsScript:  r.FormValue("analytics_script"),
	}
	if v := r.FormValue("sort_order"); v != "" {
		sp.SortOrder, _ = strconv.Atoi(v)
	}
	if pw := r.FormValue("password"); pw != "" {
		sp.PasswordHash = hashPassword(pw)
	}

	if err := validate.ValidateStatusPage(sp); err != nil {
		h.setError(w, err.Error())
		h.redirect(w, r, "/status-pages/new")
		return
	}

	ctx := r.Context()

	existing, err := h.store.GetStatusPageBySlug(ctx, sp.Slug)
	if err == nil && existing != nil {
		h.setFlash(w, "Slug already in use")
		h.redirect(w, r, "/status-pages/new")
		return
	}

	if err := h.store.CreateStatusPage(ctx, sp); err != nil {
		h.logger.Error("web: create status page", "error", err)
		h.setError(w, "Failed to create status page")
		h.redirect(w, r, "/status-pages/new")
		return
	}

	monitors := parseStatusPageMonitors(r, sp.ID)
	if len(monitors) > 0 {
		if err := h.store.SetStatusPageMonitors(ctx, sp.ID, monitors); err != nil {
			h.logger.Error("web: set status page monitors", "error", err)
		}
	}

	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}
	h.setFlash(w, "Status page created")
	h.redirect(w, r, "/status-pages")
}

func (h *Handler) StatusPageUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.setError(w, "Invalid ID")
		h.redirect(w, r, "/status-pages")
		return
	}

	r.ParseForm()

	existing, err := h.store.GetStatusPage(r.Context(), id)
	if err != nil {
		h.logger.Error("web: get status page for update", "error", err)
		h.setError(w, "Failed to load status page")
		h.redirect(w, r, "/status-pages")
		return
	}

	sp := &storage.StatusPage{
		ID:               id,
		Title:            strings.TrimSpace(r.FormValue("title")),
		Slug:             r.FormValue("slug"),
		Description:      strings.TrimSpace(r.FormValue("description")),
		Enabled:          r.FormValue("enabled") == "on",
		APIEnabled:       r.FormValue("api_enabled") == "on",
		ShowIncidents:    r.FormValue("show_incidents") == "on",
		CustomCSS:        r.FormValue("custom_css"),
		LogoURL:          strings.TrimSpace(r.FormValue("logo_url")),
		FaviconURL:       strings.TrimSpace(r.FormValue("favicon_url")),
		CustomHeaderHTML: r.FormValue("custom_header_html"),
		AnalyticsScript:  r.FormValue("analytics_script"),
		PasswordHash:     existing.PasswordHash,
	}
	if v := r.FormValue("sort_order"); v != "" {
		sp.SortOrder, _ = strconv.Atoi(v)
	}
	if pw := r.FormValue("password"); pw != "" {
		sp.PasswordHash = hashPassword(pw)
	} else if r.FormValue("clear_password") == "on" {
		sp.PasswordHash = ""
	}

	if err := validate.ValidateStatusPage(sp); err != nil {
		h.setError(w, err.Error())
		h.redirect(w, r, "/status-pages/"+strconv.FormatInt(id, 10)+"/edit")
		return
	}

	ctx := r.Context()

	slugOwner, err := h.store.GetStatusPageBySlug(ctx, sp.Slug)
	if err == nil && slugOwner != nil && slugOwner.ID != id {
		h.setFlash(w, "Slug already in use")
		h.redirect(w, r, "/status-pages/"+strconv.FormatInt(id, 10)+"/edit")
		return
	}

	if err := h.store.UpdateStatusPage(ctx, sp); err != nil {
		h.logger.Error("web: update status page", "error", err)
		h.setError(w, "Failed to update status page")
		h.redirect(w, r, "/status-pages/"+strconv.FormatInt(id, 10)+"/edit")
		return
	}

	monitors := parseStatusPageMonitors(r, id)
	if err := h.store.SetStatusPageMonitors(ctx, id, monitors); err != nil {
		h.logger.Error("web: set status page monitors", "error", err)
	}

	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}
	h.setFlash(w, "Status page updated")
	h.redirect(w, r, "/status-pages")
}

func (h *Handler) StatusPageDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/status-pages")
		return
	}
	if err := h.store.DeleteStatusPage(r.Context(), id); err != nil {
		h.logger.Error("web: delete status page", "error", err)
	}
	if h.OnStatusPageChange != nil {
		h.OnStatusPageChange()
	}
	h.setFlash(w, "Status page deleted")
	h.redirect(w, r, "/status-pages")
}

func parseStatusPageMonitors(r *http.Request, pageID int64) []storage.StatusPageMonitor {
	var result []storage.StatusPageMonitor
	for key, vals := range r.Form {
		if !strings.HasPrefix(key, "monitor_") || !strings.HasSuffix(key, "_enabled") {
			continue
		}
		if len(vals) == 0 || vals[0] != "on" {
			continue
		}
		idStr := strings.TrimSuffix(strings.TrimPrefix(key, "monitor_"), "_enabled")
		monitorID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		sortOrder := 0
		if v := r.FormValue("monitor_" + idStr + "_sort"); v != "" {
			sortOrder, _ = strconv.Atoi(v)
		}
		groupName := strings.TrimSpace(r.FormValue("monitor_" + idStr + "_group"))

		result = append(result, storage.StatusPageMonitor{
			PageID:    pageID,
			MonitorID: monitorID,
			SortOrder: sortOrder,
			GroupName: groupName,
		})
	}
	return result
}
