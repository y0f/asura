package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

func (h *Handler) ListProxies(w http.ResponseWriter, r *http.Request) {
	proxies, err := h.store.ListProxies(r.Context())
	if err != nil {
		h.logger.Error("list proxies", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list proxies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": proxies})
}

func (h *Handler) GetProxy(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := h.store.GetProxy(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "proxy not found")
			return
		}
		h.logger.Error("get proxy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get proxy")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) CreateProxy(w http.ResponseWriter, r *http.Request) {
	var p storage.Proxy
	if err := h.readJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := validate.ValidateProxy(&p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.CreateProxy(r.Context(), &p); err != nil {
		h.logger.Error("create proxy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create proxy")
		return
	}

	h.audit(r, "create", "proxy", p.ID, "")
	writeJSON(w, http.StatusCreated, p)
}

func (h *Handler) UpdateProxy(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_, err = h.store.GetProxy(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "proxy not found")
			return
		}
		h.logger.Error("get proxy for update", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get proxy")
		return
	}

	var p storage.Proxy
	if err := h.readJSON(r, &p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p.ID = id

	if err := validate.ValidateProxy(&p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.UpdateProxy(r.Context(), &p); err != nil {
		h.logger.Error("update proxy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update proxy")
		return
	}

	updated, _ := h.store.GetProxy(r.Context(), id)
	if updated == nil {
		updated = &p
	}

	h.audit(r, "update", "proxy", p.ID, "")
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) DeleteProxy(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	_, err = h.store.GetProxy(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "proxy not found")
			return
		}
		h.logger.Error("get proxy for delete", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to get proxy")
		return
	}

	if err := h.store.DeleteProxy(r.Context(), id); err != nil {
		h.logger.Error("delete proxy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete proxy")
		return
	}

	h.audit(r, "delete", "proxy", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
