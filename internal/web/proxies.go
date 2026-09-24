package web

import (
	"net/http"
	"strconv"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) Proxies(w http.ResponseWriter, r *http.Request) {
	proxies, err := h.store.ListProxies(r.Context())
	if err != nil {
		h.logger.Error("web: list proxies", "error", err)
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
			h.redirect(w, r, "/proxies")
			return
		}
		proxy, err = h.store.GetProxy(r.Context(), id)
		if err != nil {
			h.redirect(w, r, "/proxies")
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
	r.ParseForm()

	p := parseProxyForm(r)

	if err := validate.ValidateProxy(p); err != nil {
		lp := h.newLayoutParams(r, "New Proxy", "proxies")
		lp.Error = err.Error()
		h.renderComponent(w, r, views.ProxyFormPage(views.ProxyFormParams{
			LayoutParams: lp,
			Proxy:        p,
		}))
		return
	}

	if err := h.store.CreateProxy(r.Context(), p); err != nil {
		h.logger.Error("web: create proxy", "error", err)
		h.setError(w, "Failed to create proxy")
		h.redirect(w, r, "/proxies")
		return
	}

	h.audit(r, "create", "proxy", p.ID, "")
	h.setFlash(w, "Proxy created")
	h.redirect(w, r, "/proxies")
}

func (h *Handler) ProxyUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/proxies")
		return
	}
	r.ParseForm()

	p := parseProxyForm(r)
	p.ID = id
	var storedPass string
	if existing, err := h.store.GetProxy(r.Context(), id); err == nil && existing != nil {
		storedPass = existing.AuthPass
	}

	if err := validate.ValidateProxy(p); err != nil {
		lp := h.newLayoutParams(r, "Edit Proxy", "proxies")
		lp.Error = err.Error()
		h.renderComponent(w, r, views.ProxyFormPage(views.ProxyFormParams{
			LayoutParams:   lp,
			Proxy:          p,
			PasswordStored: storedPass != "",
		}))
		return
	}

	// The password field is rendered blank: blank keeps the stored password,
	// "Remove saved value" clears it. Applied after validation so a re-shown
	// form never contains the stored password.
	if p.AuthPass == "" && r.FormValue("clear_auth_pass") != "on" {
		p.AuthPass = storedPass
	}

	if err := h.store.UpdateProxy(r.Context(), p); err != nil {
		h.logger.Error("web: update proxy", "error", err)
		h.setError(w, "Failed to update proxy")
		h.redirect(w, r, "/proxies")
		return
	}

	h.audit(r, "update", "proxy", p.ID, "")
	h.setFlash(w, "Proxy updated")
	h.redirect(w, r, "/proxies")
}

func (h *Handler) ProxyDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/proxies")
		return
	}

	if err := h.store.DeleteProxy(r.Context(), id); err != nil {
		h.logger.Error("web: delete proxy", "error", err)
		h.setError(w, "Failed to delete proxy")
		h.redirect(w, r, "/proxies")
		return
	}

	h.audit(r, "delete", "proxy", id, "")
	h.setFlash(w, "Proxy deleted")
	h.redirect(w, r, "/proxies")
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
