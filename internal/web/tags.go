package web

import (
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) Tags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.store.ListTags(r.Context())
	if err != nil {
		h.logger.Error("web: list tags", "error", err)
	}

	lp := h.newLayoutParams(r, "Tags", "tags")
	h.renderComponent(w, r, views.TagListPage(views.TagListParams{
		LayoutParams: lp,
		Tags:         tags,
	}))
}

func (h *Handler) TagCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	t := &storage.Tag{
		Name:  r.FormValue("name"),
		Color: r.FormValue("color"),
	}

	if err := validate.ValidateTag(t); err != nil {
		h.setFlash(w, err.Error())
		h.redirect(w, r, "/tags")
		return
	}

	if existing, err := h.store.GetTagByName(r.Context(), t.Name); err == nil && existing != nil {
		h.setFlash(w, "Tag name already in use")
		h.redirect(w, r, "/tags")
		return
	}

	if err := h.store.CreateTag(r.Context(), t); err != nil {
		h.logger.Error("web: create tag", "error", err)
		h.setFlash(w, "Failed to create tag")
		h.redirect(w, r, "/tags")
		return
	}

	h.audit(r, "create", "tag", t.ID, "")
	h.setFlash(w, "Tag created")
	h.redirect(w, r, "/tags")
}

func (h *Handler) TagUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/tags")
		return
	}
	r.ParseForm()
	t := &storage.Tag{
		ID:    id,
		Name:  r.FormValue("name"),
		Color: r.FormValue("color"),
	}
	if t.Color == "" {
		t.Color = "#6366f1"
	}

	if err := validate.ValidateTag(t); err != nil {
		h.setFlash(w, err.Error())
		h.redirect(w, r, "/tags")
		return
	}

	if existing, err := h.store.GetTagByName(r.Context(), t.Name); err == nil && existing != nil && existing.ID != id {
		h.setFlash(w, "Tag name already in use")
		h.redirect(w, r, "/tags")
		return
	}

	if err := h.store.UpdateTag(r.Context(), t); err != nil {
		h.logger.Error("web: update tag", "error", err)
		h.setFlash(w, "Failed to update tag")
		h.redirect(w, r, "/tags")
		return
	}

	h.audit(r, "update", "tag", t.ID, "")
	h.setFlash(w, "Tag updated")
	h.redirect(w, r, "/tags")
}

func (h *Handler) TagDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/tags")
		return
	}
	if err := h.store.DeleteTag(r.Context(), id); err != nil {
		h.logger.Error("web: delete tag", "error", err)
		h.setFlash(w, "Failed to delete tag")
		h.redirect(w, r, "/tags")
		return
	}
	h.audit(r, "delete", "tag", id, "")
	h.setFlash(w, "Tag deleted")
	h.redirect(w, r, "/tags")
}
