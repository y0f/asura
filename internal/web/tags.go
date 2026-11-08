package web

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/v2/internal/httputil"
	"github.com/y0f/asura/v2/internal/storage"
	"github.com/y0f/asura/v2/internal/validate"
	"github.com/y0f/asura/v2/internal/web/views"
)

func (h *Handler) Tags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.store.ListTags(r.Context())
	if err != nil {
		h.logger.Error("web: list tags", "error", err)
		http.Error(w, "Failed to load tags", http.StatusInternalServerError)
		return
	}

	lp := h.newLayoutParams(r, "Tags", "tags")
	h.renderComponent(w, r, views.TagListPage(views.TagListParams{
		LayoutParams: lp,
		Tags:         tags,
	}))
}

func (h *Handler) TagCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid tag form", http.StatusBadRequest)
		return
	}
	t := &storage.Tag{
		Name:  r.FormValue("name"),
		Color: r.FormValue("color"),
	}

	if err := validate.ValidateTag(t); err != nil {
		h.setError(w, err.Error())
		h.tagRedirect(w, r)
		return
	}

	if err := h.store.CreateTag(r.Context(), t); err != nil {
		h.tagWriteError(w, r, "create", err)
		return
	}

	h.audit(r, "create", "tag", t.ID, "")
	h.setFlash(w, "Tag created")
	h.tagRedirect(w, r)
}

func (h *Handler) TagUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid tag form", http.StatusBadRequest)
		return
	}
	t := &storage.Tag{
		ID:    id,
		Name:  r.FormValue("name"),
		Color: r.FormValue("color"),
	}
	if t.Color == "" {
		t.Color = "#005bbf"
	}

	if err := validate.ValidateTag(t); err != nil {
		h.setError(w, err.Error())
		h.tagRedirect(w, r)
		return
	}

	if err := h.store.UpdateTag(r.Context(), t); err != nil {
		h.tagWriteError(w, r, "update", err)
		return
	}

	h.audit(r, "update", "tag", t.ID, "")
	h.setFlash(w, "Tag updated")
	h.tagRedirect(w, r)
}

func (h *Handler) TagDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.store.DeleteTag(r.Context(), id); err != nil {
		h.tagWriteError(w, r, "delete", err)
		return
	}
	h.audit(r, "delete", "tag", id, "")
	h.setFlash(w, "Tag deleted")
	h.tagRedirect(w, r)
}

func (h *Handler) tagWriteError(w http.ResponseWriter, r *http.Request, action string, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case errors.Is(err, storage.ErrTagNameConflict):
		h.setError(w, storage.ErrTagNameConflict.Error())
		h.tagRedirect(w, r)
	default:
		h.logger.Error("web: "+action+" tag", "error", err)
		http.Error(w, "Failed to "+action+" tag", http.StatusInternalServerError)
	}
}

func (h *Handler) tagRedirect(w http.ResponseWriter, r *http.Request) {
	lp := h.newLayoutParams(r, "", "")
	if lp.Can("tags.read") {
		h.redirect(w, r, "/tags")
		return
	}
	lp.BasePath = ""
	h.redirect(w, r, lp.HomePath())
}
