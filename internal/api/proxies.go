package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
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
	var input httputil.ProxyInput
	if err := h.readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	p, err := input.Apply(nil, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.CreateProxy(r.Context(), p); err != nil {
		if proxyMutationError(w, err) {
			return
		}
		h.logger.Error("create proxy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create proxy")
		return
	}

	h.audit(r, "create", "proxy", p.ID, "")
	writeJSON(w, http.StatusCreated, proxyResponse(r, p))
}

func (h *Handler) UpdateProxy(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var input httputil.ProxyInput
	if err := h.readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := h.store.UpdateProxyWith(r.Context(), id, func(current *storage.Proxy) (*storage.Proxy, error) {
		return input.Apply(current, httputil.GetAPIKey(r.Context()).SuperAdmin)
	}, httputil.MonitorAccess(r.Context(), "monitors.update", "monitors.assign")...)
	if err != nil {
		if proxyMutationError(w, err) {
			return
		}
		h.logger.Error("update proxy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update proxy")
		return
	}
	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	h.audit(r, "update", "proxy", p.ID, "")
	writeJSON(w, http.StatusOK, proxyResponse(r, p))
}

func (h *Handler) DeleteProxy(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.DeleteProxy(r.Context(), id); err != nil {
		if proxyMutationError(w, err) {
			return
		}
		h.logger.Error("delete proxy", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete proxy")
		return
	}

	h.audit(r, "delete", "proxy", id, "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func proxyResponse(r *http.Request, p *storage.Proxy) any {
	if !httputil.GetAPIKey(r.Context()).HasPermission("proxies.read") {
		return map[string]int64{"id": p.ID}
	}
	return p
}

func proxyMutationError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, storage.ErrProxyRoutingForbidden):
		writeError(w, http.StatusForbidden, storage.ErrProxyRoutingForbidden.Error())
	case errors.Is(err, storage.ErrProxyInUse):
		writeError(w, http.StatusConflict, storage.ErrProxyInUse.Error())
	case errors.Is(err, storage.ErrInvalidMonitorProxy):
		writeError(w, http.StatusBadRequest, storage.ErrInvalidMonitorProxy.Error())
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "proxy not found")
	case errors.Is(err, httputil.ErrInvalidProxy), errors.Is(err, storage.ErrProxyPasswordStorage):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		return false
	}
	return true
}
