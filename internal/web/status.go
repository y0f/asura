package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/pageauth"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

func (h *Handler) checkStatusPageAuth(r *http.Request, pageID int64, passwordHash string) bool {
	c, err := r.Cookie(fmt.Sprintf("sp_auth_%d", pageID))
	if err != nil {
		return false
	}
	return pageauth.VerifyCookie(c.Value, pageID, passwordHash, time.Now())
}

// requireStatusPageAuth redirects to the status page's password form and
// returns false when sp is password-protected and the request is not
// authenticated for it.
func (h *Handler) requireStatusPageAuth(w http.ResponseWriter, r *http.Request, sp *storage.StatusPage) bool {
	if sp.PasswordHash == "" || h.checkStatusPageAuth(r, sp.ID, sp.PasswordHash) {
		return true
	}
	http.Redirect(w, r, h.cfg.Server.BasePath+"/"+sp.Slug+"/auth", http.StatusFound)
	return false
}

func (h *Handler) StatusPageByID(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()

	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.logger.Error("web: get public status page", "error", err)
		http.Error(w, "Failed to load status page", http.StatusInternalServerError)
		return
	}
	if err != nil || sp == nil || !sp.Enabled {
		http.NotFound(w, r)
		return
	}

	if !h.requireStatusPageAuth(w, r, sp) {
		return
	}

	now := time.Now().UTC()
	snapshot, err := h.store.GetPublicStatusPageSnapshot(ctx, sp.ID, storage.PublicStatusPageAccess{
		ExpectedPasswordHash: sp.PasswordHash, Audience: storage.PublicStatusPageWeb,
	}, now, true)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		h.logger.Error("web: public status snapshot", "error", err)
		http.Error(w, "Failed to load status page", http.StatusInternalServerError)
		return
	}
	sp, monitors, spms := snapshot.Page, snapshot.Monitors, snapshot.Members

	groupNameMap := make(map[int64]string, len(spms))
	for _, spm := range spms {
		groupNameMap[spm.MonitorID] = spm.GroupName
	}

	var monitorData []views.MonitorWithUptime
	for _, m := range monitors {
		daily := snapshot.DailyUptime[m.ID]
		bars := buildDailyBars(daily, now)
		uptime, total := storage.SummarizeUptime(daily)
		label := "\u2014"
		if total > 0 {
			label = views.UptimeFmt(uptime)
		}

		monitorData = append(monitorData, views.MonitorWithUptime{
			Monitor:     m,
			DailyBars:   bars,
			Uptime90d:   uptime,
			UptimeLabel: label,
			GroupName:   groupNameMap[m.ID],
		})
	}

	var groups []views.MonitorGroup
	groupIdx := make(map[string]int)
	for _, md := range monitorData {
		gn := md.GroupName
		if idx, ok := groupIdx[gn]; ok {
			groups[idx].Monitors = append(groups[idx].Monitors, md)
		} else {
			groupIdx[gn] = len(groups)
			groups = append(groups, views.MonitorGroup{Name: gn, Monitors: []views.MonitorWithUptime{md}})
		}
	}

	overall := httputil.OverallStatus(monitors)
	incidents := snapshot.Incidents

	msg := r.URL.Query().Get("msg")

	branding, policy := h.publicStatusBranding(sp)
	w.Header().Set("Referrer-Policy", "no-referrer")
	if sp.PasswordHash != "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	h.renderComponentWithCSP(w, r, views.PublicStatusPage(views.PublicStatusPageParams{
		Title:                sp.Title,
		BasePath:             h.cfg.Server.BasePath,
		Config:               branding,
		Monitors:             monitorData,
		Groups:               groups,
		HasGroups:            len(groups) > 1 || (len(groups) == 1 && groups[0].Name != ""),
		Overall:              overall,
		Incidents:            incidents,
		HasIncidents:         len(incidents) > 0,
		SubscriptionsEnabled: h.cfg.Subscriptions.Enabled,
		Message:              msg,
	}), policy)
}

func (h *Handler) StatusPageAuthGet(w http.ResponseWriter, r *http.Request, pageID int64) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.logger.Error("web: get public status page", "error", err)
		http.Error(w, "Failed to load status page", http.StatusInternalServerError)
		return
	}
	if err != nil || sp == nil || !sp.Enabled {
		http.NotFound(w, r)
		return
	}
	if sp.PasswordHash == "" {
		http.Redirect(w, r, h.cfg.Server.BasePath+"/"+sp.Slug, http.StatusFound)
		return
	}
	if h.checkStatusPageAuth(r, pageID, sp.PasswordHash) {
		http.Redirect(w, r, h.cfg.Server.BasePath+"/"+sp.Slug, http.StatusFound)
		return
	}
	h.renderComponent(w, r, views.StatusPageAuthPage(views.StatusPageAuthParams{
		Title:    "Protected status page",
		BasePath: h.cfg.Server.BasePath,
		Slug:     sp.Slug,
		Error:    r.URL.Query().Get("error"),
	}))
}

func (h *Handler) StatusPageAuthPost(w http.ResponseWriter, r *http.Request, pageID int64, slug string) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		h.logger.Error("web: get public status page", "error", err)
		http.Error(w, "Failed to load status page", http.StatusInternalServerError)
		return
	}
	if err != nil || sp == nil || !sp.Enabled || sp.PasswordHash == "" {
		http.Redirect(w, r, h.cfg.Server.BasePath+"/"+slug, http.StatusFound)
		return
	}

	ip := httputil.ExtractIP(r, h.cfg.TrustedNets())
	if !h.loginRL.Allow(ip) {
		http.Redirect(w, r, h.cfg.Server.BasePath+"/"+slug+"/auth?error=1", http.StatusSeeOther)
		return
	}

	if !pageauth.Verify(sp.PasswordHash, r.FormValue("password")) {
		http.Redirect(w, r, h.cfg.Server.BasePath+"/"+slug+"/auth?error=1", http.StatusSeeOther)
		return
	}

	value, err := pageauth.IssueCookie(pageID, sp.PasswordHash, time.Now())
	if err != nil {
		h.logger.Error("web: issue status page cookie", "error", err)
		http.Error(w, "Failed to authenticate status page", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     fmt.Sprintf("sp_auth_%d", pageID),
		Value:    value,
		Path:     h.cfg.Server.BasePath + "/",
		MaxAge:   int(pageauth.CookieLifetime / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   h.cfg.Auth.Session.CookieSecure,
	})
	http.Redirect(w, r, h.cfg.Server.BasePath+"/"+slug, http.StatusSeeOther)
}

func buildDailyBars(daily []*storage.DailyUptime, now time.Time) []views.DailyBar {
	dayMap := make(map[string]*storage.DailyUptime)
	for _, d := range daily {
		dayMap[d.Date] = d
	}

	bars := make([]views.DailyBar, 0, 90)
	for i := 89; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		dateStr := day.Format("2006-01-02")
		label := day.Format("Jan 2, 2006")
		if d, ok := dayMap[dateStr]; ok && d.TotalChecks > 0 {
			bars = append(bars, views.DailyBar{
				Date:      dateStr,
				UptimePct: d.UptimePct,
				HasData:   true,
				Label:     label,
			})
		} else {
			bars = append(bars, views.DailyBar{
				Date:    dateStr,
				HasData: false,
				Label:   label,
			})
		}
	}
	return bars
}
