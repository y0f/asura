package web

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) Proxies(w http.ResponseWriter, r *http.Request) {
	proxies, err := h.store.ListProxies(r.Context())
	if err != nil {
		h.logger.Error("web: list proxies", "error", err)
		http.Error(w, "Failed to load proxies", http.StatusInternalServerError)
		return
	}

	lp := h.newLayoutParams(r, "Proxies", "proxies")
	h.renderComponent(w, r, views.ProxyListPage(views.ProxyListParams{
		LayoutParams: lp,
		Proxies:      proxies,
	}))
}

func (h *Handler) ProxyForm(w http.ResponseWriter, r *http.Request) {
	var proxy *storage.Proxy

	if idStr := r.PathValue("id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		proxy, err = h.store.GetProxy(r.Context(), id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			h.logger.Error("web: load proxy form", "error", err)
			http.Error(w, "Failed to load proxy", http.StatusInternalServerError)
			return
		}
	}

	title := "New Proxy"
	passwordStored := false
	if proxy != nil {
		title = "Edit Proxy"
		// Never write the stored password into the page.
		passwordStored = proxy.AuthPass != ""
		proxy.AuthPass = ""
	} else {
		proxy = &storage.Proxy{
			Protocol: "http",
			Port:     8080,
			Enabled:  true,
		}
	}

	lp := h.newLayoutParams(r, title, "proxies")
	h.renderComponent(w, r, views.ProxyFormPage(views.ProxyFormParams{
		LayoutParams:   lp,
		Proxy:          proxy,
		PasswordStored: passwordStored,
	}))
}

func (h *Handler) ProxyCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid proxy form", http.StatusBadRequest)
		return
	}

	p := parseProxyForm(r)

	if _, err := proxyFormInput(p, false).Apply(nil, false); err != nil {
		h.proxyFormError(w, r, p, false, err)
		return
	}

	if err := h.store.CreateProxy(r.Context(), p); err != nil {
		if errors.Is(err, storage.ErrProxyPasswordStorage) {
			h.proxyFormError(w, r, p, false, err)
			return
		}
		h.logger.Error("web: create proxy", "error", err)
		http.Error(w, "Failed to create proxy", http.StatusInternalServerError)
		return
	}

	h.audit(r, "create", "proxy", p.ID, "")
	h.setFlash(w, "Proxy created")
	h.proxyRedirect(w, r)
}

func (h *Handler) ProxyUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid proxy form", http.StatusBadRequest)
		return
	}

	p := parseProxyForm(r)
	p.ID = id
	input := proxyFormInput(p, r.FormValue("clear_auth_pass") == "on")
	passwordStored := false
	_, err = h.store.UpdateProxyWith(r.Context(), id, func(current *storage.Proxy) (*storage.Proxy, error) {
		key := httputil.GetAPIKey(r.Context())
		passwordStored = key.HasPermission("proxies.read") && current.AuthPass != ""
		return input.Apply(current, key.SuperAdmin)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, httputil.ErrInvalidProxy) || errors.Is(err, storage.ErrProxyPasswordStorage) || errors.Is(err, storage.ErrInvalidMonitorProxy) {
			h.proxyFormError(w, r, p, passwordStored, err)
			return
		}
		h.logger.Error("web: update proxy", "error", err)
		http.Error(w, "Failed to update proxy", http.StatusInternalServerError)
		return
	}

	h.audit(r, "update", "proxy", p.ID, "")
	h.setFlash(w, "Proxy updated")
	h.proxyRedirect(w, r)
}

func (h *Handler) ProxyDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := h.store.DeleteProxy(r.Context(), id); err != nil {
		if errors.Is(err, storage.ErrProxyInUse) {
			http.Error(w, storage.ErrProxyInUse.Error(), http.StatusConflict)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("web: delete proxy", "error", err)
		http.Error(w, "Failed to delete proxy", http.StatusInternalServerError)
		return
	}

	h.audit(r, "delete", "proxy", id, "")
	h.setFlash(w, "Proxy deleted")
	h.proxyRedirect(w, r)
}

func (h *Handler) proxyFormError(w http.ResponseWriter, r *http.Request, submitted *storage.Proxy, passwordStored bool, err error) {
	title := "New Proxy"
	if submitted.ID > 0 {
		title = "Edit Proxy"
	}
	lp := h.newLayoutParams(r, title, "proxies")
	lp.Error = err.Error()
	h.renderComponent(w, r, views.ProxyFormPage(views.ProxyFormParams{LayoutParams: lp, Proxy: submitted, PasswordStored: passwordStored}))
}

func (h *Handler) proxyRedirect(w http.ResponseWriter, r *http.Request) {
	lp := h.newLayoutParams(r, "", "")
	if lp.Can("proxies.read") {
		h.redirect(w, r, "/proxies")
		return
	}
	lp.BasePath = ""
	h.redirect(w, r, lp.HomePath())
}

func proxyFormInput(p *storage.Proxy, clear bool) httputil.ProxyInput {
	return httputil.ProxyInput{Name: &p.Name, Protocol: &p.Protocol, Host: &p.Host, Port: &p.Port, AuthUser: &p.AuthUser, AuthPass: &p.AuthPass, Enabled: &p.Enabled, ClearAuthPass: clear}
}

func parseProxyForm(r *http.Request) *storage.Proxy {
	port, _ := strconv.Atoi(r.FormValue("port"))
	return &storage.Proxy{
		Name:     r.FormValue("name"),
		Protocol: r.FormValue("protocol"),
		Host:     r.FormValue("host"),
		Port:     port,
		AuthUser: r.FormValue("auth_user"),
		AuthPass: r.FormValue("auth_pass"),
		Enabled:  r.FormValue("enabled") == "on",
	}
}
