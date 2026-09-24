package web

import (
	"net/http"
	"strconv"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) Groups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.store.ListMonitorGroups(r.Context())
	if err != nil {
		h.logger.Error("web: list groups", "error", err)
	}

	lp := h.newLayoutParams(r, "Groups", "groups")
	h.renderComponent(w, r, views.GroupListPage(views.GroupListParams{
		LayoutParams: lp,
		Groups:       groups,
	}))
}

func (h *Handler) GroupDetail(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/groups")
		return
	}

	ctx := r.Context()
	group, err := h.store.GetMonitorGroup(ctx, id)
	if err != nil {
		h.redirect(w, r, "/groups")
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	result, err := h.store.ListMonitors(ctx, storage.MonitorListFilter{GroupID: &id}, storage.Pagination{Page: page, PerPage: 50})
	if err != nil {
		h.logger.Error("web: list group monitors", "error", err)
	}

	lp := h.newLayoutParams(r, group.Name, "groups")
	h.renderComponent(w, r, views.GroupDetailPage(views.GroupDetailParams{
		LayoutParams: lp,
		Group:        group,
		Monitors:     result,
	}))
}

func (h *Handler) GroupCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	g := &storage.MonitorGroup{
		Name: r.FormValue("name"),
	}
	if v := r.FormValue("sort_order"); v != "" {
		g.SortOrder, _ = strconv.Atoi(v)
	}

	if err := validate.ValidateMonitorGroup(g); err != nil {
		h.setError(w, err.Error())
		h.redirect(w, r, "/groups")
		return
	}

	if err := h.store.CreateMonitorGroup(r.Context(), g); err != nil {
		h.logger.Error("web: create group", "error", err)
		h.setError(w, "Failed to create group")
		h.redirect(w, r, "/groups")
		return
	}

	h.setFlash(w, "Group created")
	h.redirect(w, r, "/groups")
}

func (h *Handler) GroupUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/groups")
		return
	}
	r.ParseForm()
	g := &storage.MonitorGroup{
		ID:   id,
		Name: r.FormValue("name"),
	}
	if v := r.FormValue("sort_order"); v != "" {
		g.SortOrder, _ = strconv.Atoi(v)
	}

	if err := validate.ValidateMonitorGroup(g); err != nil {
		h.setError(w, err.Error())
		h.redirect(w, r, "/groups")
		return
	}

	if err := h.store.UpdateMonitorGroup(r.Context(), g); err != nil {
		h.logger.Error("web: update group", "error", err)
		h.setError(w, "Failed to update group")
		h.redirect(w, r, "/groups")
		return
	}

	h.setFlash(w, "Group updated")
	h.redirect(w, r, "/groups")
}

func (h *Handler) GroupDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/groups")
		return
	}
	if err := h.store.DeleteMonitorGroup(r.Context(), id); err != nil {
		h.logger.Error("web: delete group", "error", err)
	}
	h.setFlash(w, "Group deleted")
	h.redirect(w, r, "/groups")
}
