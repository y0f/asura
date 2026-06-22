package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

func (h *Handler) ListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.store.ListTags(r.Context())
	if err != nil {
		h.logger.Error("list tags", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list tags")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": tags})
}

func (h *Handler) CreateTag(w http.ResponseWriter, r *http.Request) {
	var t storage.Tag
	if err := h.readJSON(r, &t); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validate.ValidateTag(&t); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if existing, err := h.store.GetTagByName(r.Context(), t.Name); err == nil && existing != nil {
		writeError(w, http.StatusConflict, "tag name already in use")
		return
	}
	if err := h.store.CreateTag(r.Context(), &t); err != nil {
		h.logger.Error("create tag", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create tag")
		return
	}
	h.audit(r, "create", "tag", t.ID, "")
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handler) UpdateTag(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, err = h.store.GetTag(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "tag not found")
			return
		}
		h.logger.Error("get tag for update", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get tag")
		return
	}

	var t storage.Tag
	if err := h.readJSON(r, &t); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	t.ID = id
	if err := validate.ValidateTag(&t); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if existing, err := h.store.GetTagByName(r.Context(), t.Name); err == nil && existing != nil && existing.ID != id {
		writeError(w, http.StatusConflict, "tag name already in use")
		return
	}
	if err := h.store.UpdateTag(r.Context(), &t); err != nil {
		h.logger.Error("update tag", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update tag")
		return
	}
	h.audit(r, "update", "tag", t.ID, "")
	updated, _ := h.store.GetTag(r.Context(), id)
	if updated != nil {
		writeJSON(w, http.StatusOK, updated)
	} else {
		writeJSON(w, http.StatusOK, t)
	}
}

func (h *Handler) DeleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, err = h.store.GetTag(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "tag not found")
			return
		}
		h.logger.Error("get tag for delete", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get tag")
		return
	}
	if err := h.store.DeleteTag(r.Context(), id); err != nil {
		h.logger.Error("delete tag", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete tag")
		return
	}
	h.audit(r, "delete", "tag", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
