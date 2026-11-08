package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/redact"
	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/validate"
	"github.com/y0f/asura/v2/internal/web/views"
)

func (h *Handler) Groups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.store.ListMonitorGroups(r.Context())
	if err != nil {
		h.logger.Error("web: list groups", "error", err)
		http.Error(w, "Failed to load groups", http.StatusInternalServerError)
		return
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
		h.redirectToReadable(w, r, "/groups")
		return
	}

	ctx := r.Context()
	group, err := h.store.GetMonitorGroup(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			h.logger.Error("web: get group", "error", err)
			http.Error(w, "Failed to load group", http.StatusInternalServerError)
		}
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	result, err := h.store.ListMonitors(ctx, storage.MonitorListFilter{GroupID: &id, Access: httputil.MonitorAccess(ctx, "monitors.read")}, storage.Pagination{Page: page, PerPage: 50})
	if err != nil {
		h.logger.Error("web: list group monitors", "error", err)
		http.Error(w, "Failed to load group monitors", http.StatusInternalServerError)
		return
	}
	if monitors, ok := result.Data.([]*storage.Monitor); ok {
		result.Data = redact.MonitorSummaries(monitors)
	}

	lp := h.newLayoutParams(r, group.Name, "groups")
	h.renderComponent(w, r, views.GroupDetailPage(views.GroupDetailParams{
		LayoutParams: lp,
		Group:        group,
		Monitors:     result,
	}))
}

func (h *Handler) GroupCreate(w http.ResponseWriter, r *http.Request) {
	g, err := groupForm(r, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := validate.ValidateMonitorGroup(g); err != nil {
		h.setError(w, err.Error())
		h.redirectToReadable(w, r, "/groups")
		return
	}

	if err := h.store.CreateMonitorGroup(r.Context(), g); err != nil {
		h.logger.Error("web: create group", "error", err)
		h.setError(w, "Failed to create group")
		h.redirectToReadable(w, r, "/groups")
		return
	}

	h.setFlash(w, "Group created")
	h.redirectToReadable(w, r, "/groups")
}

func (h *Handler) GroupUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirectToReadable(w, r, "/groups")
		return
	}
	g, err := groupForm(r, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := validate.ValidateMonitorGroup(g); err != nil {
		h.setError(w, err.Error())
		h.redirectToReadable(w, r, "/groups")
		return
	}

	if err := h.store.UpdateMonitorGroup(r.Context(), g); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("web: update group", "error", err)
		h.setError(w, "Failed to update group")
		h.redirectToReadable(w, r, "/groups")
		return
	}

	h.setFlash(w, "Group updated")
	h.redirectToReadable(w, r, "/groups")
}

func groupForm(r *http.Request, id int64) (*storage.MonitorGroup, error) {
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("invalid group form")
	}
	g := &storage.MonitorGroup{ID: id, Name: r.FormValue("name")}
	if value := r.FormValue("sort_order"); value != "" {
		order, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("sort order must be an integer")
		}
		g.SortOrder = order
	}
	return g, nil
}

func (h *Handler) GroupDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirectToReadable(w, r, "/groups")
		return
	}
	if err := h.store.DeleteEmptyMonitorGroup(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrGroupNotEmpty) {
			http.Error(w, "Move this group's monitors before deleting it", http.StatusConflict)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("web: delete group", "error", err)
		http.Error(w, "Failed to delete group", http.StatusInternalServerError)
		return
	}
	h.setFlash(w, "Group deleted")
	h.redirectToReadable(w, r, "/groups")
}
