package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/web/views"
)

func spAuthCookieValue(passwordHash string) string {
	h := sha256.New()
	h.Write([]byte(passwordHash + ":sp-auth"))
	return hex.EncodeToString(h.Sum(nil))
}

func (h *Handler) checkStatusPageAuth(r *http.Request, pageID int64, passwordHash string) bool {
	c, err := r.Cookie(fmt.Sprintf("sp_auth_%d", pageID))
	if err != nil {
		return false
	}
	expected := spAuthCookieValue(passwordHash)
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(expected)) == 1
}

func (h *Handler) StatusPageByID(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()

	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil || sp == nil || !sp.Enabled {
		http.NotFound(w, r)
		return
	}

	if sp.PasswordHash != "" && !h.checkStatusPageAuth(r, pageID, sp.PasswordHash) {
		http.Redirect(w, r, h.cfg.Server.BasePath+"/"+sp.Slug+"/auth", http.StatusFound)
		return
	}

	monitors, spms, err := h.store.ListStatusPageMonitorsWithStatus(ctx, sp.ID)
	if err != nil {
		h.logger.Error("web: status page monitors", "error", err)
		monitors = []*storage.Monitor{}
		spms = []storage.StatusPageMonitor{}
	}

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -90)

	groupNameMap := make(map[int64]string, len(spms))
	for _, spm := range spms {
		groupNameMap[spm.MonitorID] = spm.GroupName
	}

	var monitorData []views.MonitorWithUptime
	for _, m := range monitors {
		bars := h.buildDailyBars(ctx, m.ID, from, now)
		uptime, err := h.store.GetUptimePercent(ctx, m.ID, from, now)
		if err != nil {
			h.logger.Error("web: status uptime percent", "monitor_id", m.ID, "error", err)
			uptime = 100
		}

		monitorData = append(monitorData, views.MonitorWithUptime{
			Monitor:     m,
			DailyBars:   bars,
			Uptime90d:   uptime,
			UptimeLabel: views.UptimeFmt(uptime),
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
	incidents := httputil.PublicIncidentsForPage(ctx, h.store, sp, monitors, now)

	msg := r.URL.Query().Get("msg")

	h.renderComponent(w, r, views.PublicStatusPage(views.PublicStatusPageParams{
		Title:                sp.Title,
		BasePath:             h.cfg.Server.BasePath,
		Config:               sp,
		Monitors:             monitorData,
		Groups:               groups,
		HasGroups:            len(groups) > 1 || (len(groups) == 1 && groups[0].Name != ""),
		Overall:              overall,
		Incidents:            incidents,
		HasIncidents:         len(incidents) > 0,
		SubscriptionsEnabled: h.cfg.Subscriptions.Enabled,
		Message:              msg,
	}))
}

func (h *Handler) StatusPageAuthGet(w http.ResponseWriter, r *http.Request, pageID int64) {
	ctx := r.Context()
	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil || !sp.Enabled {
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
		Title:    sp.Title,
		BasePath: h.cfg.Server.BasePath,
		Slug:     sp.Slug,
		Error:    r.URL.Query().Get("error"),
	}))
}

func (h *Handler) StatusPageAuthPost(w http.ResponseWriter, r *http.Request, pageID int64, slug string) {
	ctx := r.Context()
	sp, err := h.store.GetStatusPage(ctx, pageID)
	if err != nil || !sp.Enabled || sp.PasswordHash == "" {
		http.Redirect(w, r, h.cfg.Server.BasePath+"/"+slug, http.StatusFound)
		return
	}

	ip := httputil.ExtractIP(r, h.cfg.TrustedNets())
	if !h.loginRL.Allow(ip) {
		http.Redirect(w, r, h.cfg.Server.BasePath+"/"+slug+"/auth?error=1", http.StatusSeeOther)
		return
	}

	sh := sha256.New()
	sh.Write([]byte(r.FormValue("password")))
	inputHash := hex.EncodeToString(sh.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(inputHash), []byte(sp.PasswordHash)) != 1 {
		http.Redirect(w, r, h.cfg.Server.BasePath+"/"+slug+"/auth?error=1", http.StatusSeeOther)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     fmt.Sprintf("sp_auth_%d", pageID),
		Value:    spAuthCookieValue(sp.PasswordHash),
		Path:     h.cfg.Server.BasePath + "/",
		MaxAge:   86400 * 7,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   h.cfg.Auth.Session.CookieSecure,
	})
	http.Redirect(w, r, h.cfg.Server.BasePath+"/"+slug, http.StatusSeeOther)
}

func (h *Handler) buildDailyBars(ctx context.Context, monitorID int64, from, now time.Time) []views.DailyBar {
	daily, err := h.store.GetDailyUptime(ctx, monitorID, from, now)
	if err != nil {
		h.logger.Error("web: status daily uptime", "monitor_id", monitorID, "error", err)
	}

	dayMap := make(map[string]*storage.DailyUptime)
	for _, d := range daily {
		dayMap[d.Date] = d
	}

	bars := make([]views.DailyBar, 0, 90)
	for i := 89; i >= 0; i-- {
		day := now.AddDate(0, 0, -i)
		dateStr := day.Format("2006-01-02")
		label := day.Format("Jan 2, 2006")
		if d, ok := dayMap[dateStr]; ok {
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
