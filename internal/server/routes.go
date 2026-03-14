package server

import (
	"io/fs"
	"net/http"

	webfs "github.com/y0f/asura/web"
)

func (s *Server) registerRoutes(mux *http.ServeMux) {
	monRead := s.api.Auth("monitors.read")
	monWrite := s.api.Auth("monitors.write")
	incRead := s.api.Auth("incidents.read")
	incWrite := s.api.Auth("incidents.write")
	notifRead := s.api.Auth("notifications.read")
	notifWrite := s.api.Auth("notifications.write")
	maintRead := s.api.Auth("maintenance.read")
	maintWrite := s.api.Auth("maintenance.write")
	metricsRead := s.api.Auth("metrics.read")

	if s.cfg.Server.BasePath != "" {
		mux.HandleFunc("GET "+s.cfg.Server.BasePath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, s.cfg.Server.BasePath+"/", http.StatusMovedPermanently)
		})
	}

	if s.cfg.IsWebUIEnabled() && s.web != nil {
		staticFS, _ := fs.Sub(webfs.FS, "static")
		staticPrefix := s.p("/static/")
		staticHandler := http.StripPrefix(staticPrefix, http.FileServer(http.FS(staticFS)))
		mux.Handle("GET "+staticPrefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=604800")
			staticHandler.ServeHTTP(w, r)
		}))

		mux.HandleFunc("GET "+s.p("/login"), s.web.Login)
		mux.HandleFunc("POST "+s.p("/login"), s.web.LoginPost)
		mux.HandleFunc("GET "+s.p("/login/totp"), s.web.TOTPLogin)
		mux.HandleFunc("POST "+s.p("/login/totp"), s.web.TOTPLoginPost)
		webAuth := s.web.RequireAuth
		webPerm := func(perm string, h http.HandlerFunc) http.Handler {
			return webAuth(s.web.RequirePerm(perm, http.HandlerFunc(h)))
		}

		mux.Handle("POST "+s.p("/logout"), webAuth(http.HandlerFunc(s.web.Logout)))

		mux.Handle("GET "+s.p("/{$}"), webAuth(http.HandlerFunc(s.web.Dashboard)))
		mux.Handle("GET "+s.p("/monitors"), webAuth(http.HandlerFunc(s.web.Monitors)))
		mux.Handle("GET "+s.p("/monitors/new"), webAuth(http.HandlerFunc(s.web.MonitorForm)))
		mux.Handle("GET "+s.p("/monitors/{id}"), webAuth(http.HandlerFunc(s.web.MonitorDetail)))
		mux.Handle("GET "+s.p("/monitors/{id}/edit"), webAuth(http.HandlerFunc(s.web.MonitorForm)))
		mux.Handle("POST "+s.p("/monitors"), webPerm("monitors.write", s.web.MonitorCreate))
		mux.Handle("POST "+s.p("/monitors/{id}"), webPerm("monitors.write", s.web.MonitorUpdate))
		mux.Handle("POST "+s.p("/monitors/{id}/delete"), webPerm("monitors.write", s.web.MonitorDelete))
		mux.Handle("POST "+s.p("/monitors/{id}/pause"), webPerm("monitors.write", s.web.MonitorPause))
		mux.Handle("POST "+s.p("/monitors/{id}/resume"), webPerm("monitors.write", s.web.MonitorResume))
		mux.Handle("POST "+s.p("/monitors/{id}/clone"), webPerm("monitors.write", s.web.MonitorClone))
		mux.Handle("POST "+s.p("/monitors/{id}/status"), webPerm("monitors.write", s.web.MonitorSetManualStatus))
		mux.Handle("POST "+s.p("/monitors/bulk"), webPerm("monitors.write", s.web.MonitorBulk))
		mux.Handle("GET "+s.p("/monitors/{id}/chart"), webAuth(http.HandlerFunc(s.api.MonitorChart)))

		mux.Handle("GET "+s.p("/incidents"), webAuth(http.HandlerFunc(s.web.Incidents)))
		mux.Handle("GET "+s.p("/incidents/{id}"), webAuth(http.HandlerFunc(s.web.IncidentDetail)))
		mux.Handle("POST "+s.p("/incidents/{id}/ack"), webPerm("incidents.write", s.web.IncidentAck))
		mux.Handle("POST "+s.p("/incidents/{id}/resolve"), webPerm("incidents.write", s.web.IncidentResolve))
		mux.Handle("POST "+s.p("/incidents/{id}/delete"), webPerm("incidents.write", s.web.IncidentDelete))

		mux.Handle("GET "+s.p("/groups"), webAuth(http.HandlerFunc(s.web.Groups)))
		mux.Handle("GET "+s.p("/groups/{id}"), webAuth(http.HandlerFunc(s.web.GroupDetail)))
		mux.Handle("POST "+s.p("/groups"), webPerm("monitors.write", s.web.GroupCreate))
		mux.Handle("POST "+s.p("/groups/{id}"), webPerm("monitors.write", s.web.GroupUpdate))
		mux.Handle("POST "+s.p("/groups/{id}/delete"), webPerm("monitors.write", s.web.GroupDelete))

		mux.Handle("GET "+s.p("/tags"), webAuth(http.HandlerFunc(s.web.Tags)))
		mux.Handle("POST "+s.p("/tags"), webPerm("monitors.write", s.web.TagCreate))
		mux.Handle("POST "+s.p("/tags/{id}"), webPerm("monitors.write", s.web.TagUpdate))
		mux.Handle("POST "+s.p("/tags/{id}/delete"), webPerm("monitors.write", s.web.TagDelete))

		mux.Handle("GET "+s.p("/notifications"), webAuth(http.HandlerFunc(s.web.Notifications)))
		mux.Handle("GET "+s.p("/notifications/history"), webAuth(http.HandlerFunc(s.web.NotificationHistory)))
		mux.Handle("POST "+s.p("/notifications"), webPerm("notifications.write", s.web.NotificationCreate))
		mux.Handle("POST "+s.p("/notifications/{id}"), webPerm("notifications.write", s.web.NotificationUpdate))
		mux.Handle("POST "+s.p("/notifications/{id}/delete"), webPerm("notifications.write", s.web.NotificationDelete))
		mux.Handle("POST "+s.p("/notifications/{id}/test"), webPerm("notifications.write", s.web.NotificationTest))

		mux.Handle("GET "+s.p("/escalation-policies"), webAuth(http.HandlerFunc(s.web.EscalationPolicies)))
		mux.Handle("POST "+s.p("/escalation-policies"), webPerm("escalation_policies.write", s.web.EscalationPolicyCreate))
		mux.Handle("POST "+s.p("/escalation-policies/{id}"), webPerm("escalation_policies.write", s.web.EscalationPolicyUpdate))
		mux.Handle("POST "+s.p("/escalation-policies/{id}/delete"), webPerm("escalation_policies.write", s.web.EscalationPolicyDelete))

		mux.Handle("GET "+s.p("/sla"), webAuth(http.HandlerFunc(s.web.SLAReport)))
		mux.Handle("GET "+s.p("/sla/export"), webAuth(http.HandlerFunc(s.web.SLAExport)))

		mux.Handle("GET "+s.p("/maintenance"), webAuth(http.HandlerFunc(s.web.Maintenance)))
		mux.Handle("POST "+s.p("/maintenance"), webPerm("maintenance.write", s.web.MaintenanceCreate))
		mux.Handle("POST "+s.p("/maintenance/{id}"), webPerm("maintenance.write", s.web.MaintenanceUpdate))
		mux.Handle("POST "+s.p("/maintenance/{id}/delete"), webPerm("maintenance.write", s.web.MaintenanceDelete))

		mux.Handle("GET "+s.p("/logs"), webAuth(http.HandlerFunc(s.web.RequestLogs)))
		mux.Handle("GET "+s.p("/audit"), webAuth(http.HandlerFunc(s.web.AuditLog)))

		mux.Handle("GET "+s.p("/proxies"), webAuth(http.HandlerFunc(s.web.Proxies)))
		mux.Handle("GET "+s.p("/proxies/new"), webAuth(http.HandlerFunc(s.web.ProxyForm)))
		mux.Handle("GET "+s.p("/proxies/{id}/edit"), webAuth(http.HandlerFunc(s.web.ProxyForm)))
		mux.Handle("POST "+s.p("/proxies"), webPerm("monitors.write", s.web.ProxyCreate))
		mux.Handle("POST "+s.p("/proxies/{id}"), webPerm("monitors.write", s.web.ProxyUpdate))
		mux.Handle("POST "+s.p("/proxies/{id}/delete"), webPerm("monitors.write", s.web.ProxyDelete))

		mux.Handle("GET "+s.p("/status-pages"), webAuth(http.HandlerFunc(s.web.StatusPages)))
		mux.Handle("GET "+s.p("/status-pages/new"), webAuth(http.HandlerFunc(s.web.StatusPageForm)))
		mux.Handle("GET "+s.p("/status-pages/{id}/edit"), webAuth(http.HandlerFunc(s.web.StatusPageForm)))
		mux.Handle("POST "+s.p("/status-pages"), webPerm("monitors.write", s.web.StatusPageCreate))
		mux.Handle("POST "+s.p("/status-pages/{id}"), webPerm("monitors.write", s.web.StatusPageUpdate))
		mux.Handle("POST "+s.p("/status-pages/{id}/delete"), webPerm("monitors.write", s.web.StatusPageDelete))

		mux.Handle("GET "+s.p("/settings"), webAuth(http.HandlerFunc(s.web.Settings)))
		mux.Handle("GET "+s.p("/settings/export"), webAuth(http.HandlerFunc(s.web.ExportConfig)))
		mux.Handle("POST "+s.p("/settings/import"), webPerm("monitors.write", s.web.ImportConfig))
		mux.Handle("POST "+s.p("/settings/vacuum"), webPerm("monitors.write", s.web.DBVacuum))
	}

	mux.HandleFunc("GET "+s.p("/api/v1/health"), s.api.Health)
	mux.Handle("GET "+s.p("/metrics"), metricsRead(http.HandlerFunc(s.api.Metrics)))
	mux.HandleFunc("POST "+s.p("/api/v1/heartbeat/{token}"), s.api.HeartbeatPing)
	mux.HandleFunc("GET "+s.p("/api/v1/heartbeat/{token}"), s.api.HeartbeatPing)
	mux.HandleFunc("GET "+s.p("/api/v1/badge/{id}/status"), s.api.BadgeStatus)
	mux.HandleFunc("GET "+s.p("/api/v1/badge/{id}/uptime"), s.api.BadgeUptime)
	mux.HandleFunc("GET "+s.p("/api/v1/badge/{id}/response"), s.api.BadgeResponseTime)
	mux.HandleFunc("GET "+s.p("/api/v1/badge/{id}/cert"), s.api.BadgeCert)

	mux.Handle("GET "+s.p("/api/v1/monitors"), monRead(http.HandlerFunc(s.api.ListMonitors)))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}"), monRead(http.HandlerFunc(s.api.GetMonitor)))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}/checks"), monRead(http.HandlerFunc(s.api.ListChecks)))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}/metrics"), monRead(http.HandlerFunc(s.api.MonitorMetrics)))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}/changes"), monRead(http.HandlerFunc(s.api.ListChanges)))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}/chart"), monRead(http.HandlerFunc(s.api.MonitorChart)))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}/sla"), monRead(http.HandlerFunc(s.api.MonitorSLA)))
	mux.Handle("GET "+s.p("/api/v1/reports/sla"), monRead(http.HandlerFunc(s.api.SLAReport)))
	mux.Handle("GET "+s.p("/api/v1/reports/sla/export"), monRead(http.HandlerFunc(s.api.SLAReportExport)))

	mux.Handle("GET "+s.p("/api/v1/incidents"), incRead(http.HandlerFunc(s.api.ListIncidents)))
	mux.Handle("GET "+s.p("/api/v1/incidents/{id}"), incRead(http.HandlerFunc(s.api.GetIncident)))

	mux.Handle("GET "+s.p("/api/v1/notifications"), notifRead(http.HandlerFunc(s.api.ListNotifications)))
	mux.Handle("GET "+s.p("/api/v1/notifications/history"), notifRead(http.HandlerFunc(s.api.ListNotificationHistory)))
	mux.Handle("GET "+s.p("/api/v1/maintenance"), maintRead(http.HandlerFunc(s.api.ListMaintenance)))
	mux.Handle("GET "+s.p("/api/v1/groups"), monRead(http.HandlerFunc(s.api.ListGroups)))
	mux.Handle("POST "+s.p("/api/v1/groups"), monWrite(http.HandlerFunc(s.api.CreateGroup)))
	mux.Handle("PUT "+s.p("/api/v1/groups/{id}"), monWrite(http.HandlerFunc(s.api.UpdateGroup)))
	mux.Handle("DELETE "+s.p("/api/v1/groups/{id}"), monWrite(http.HandlerFunc(s.api.DeleteGroup)))
	mux.Handle("GET "+s.p("/api/v1/overview"), monRead(http.HandlerFunc(s.api.Overview)))
	mux.Handle("GET "+s.p("/api/v1/tags"), monRead(http.HandlerFunc(s.api.ListTags)))
	mux.Handle("POST "+s.p("/api/v1/tags"), monWrite(http.HandlerFunc(s.api.CreateTag)))
	mux.Handle("PUT "+s.p("/api/v1/tags/{id}"), monWrite(http.HandlerFunc(s.api.UpdateTag)))
	mux.Handle("DELETE "+s.p("/api/v1/tags/{id}"), monWrite(http.HandlerFunc(s.api.DeleteTag)))

	mux.Handle("POST "+s.p("/api/v1/monitors"), monWrite(http.HandlerFunc(s.api.CreateMonitor)))
	mux.Handle("PUT "+s.p("/api/v1/monitors/{id}"), monWrite(http.HandlerFunc(s.api.UpdateMonitor)))
	mux.Handle("DELETE "+s.p("/api/v1/monitors/{id}"), monWrite(http.HandlerFunc(s.api.DeleteMonitor)))
	mux.Handle("POST "+s.p("/api/v1/monitors/{id}/pause"), monWrite(http.HandlerFunc(s.api.PauseMonitor)))
	mux.Handle("POST "+s.p("/api/v1/monitors/{id}/resume"), monWrite(http.HandlerFunc(s.api.ResumeMonitor)))
	mux.Handle("POST "+s.p("/api/v1/monitors/{id}/clone"), monWrite(http.HandlerFunc(s.api.CloneMonitor)))
	mux.Handle("POST "+s.p("/api/v1/monitors/{id}/status"), monWrite(http.HandlerFunc(s.api.SetManualStatus)))
	mux.Handle("POST "+s.p("/api/v1/monitors/bulk"), monWrite(http.HandlerFunc(s.api.BulkMonitors)))

	mux.Handle("POST "+s.p("/api/v1/incidents/{id}/ack"), incWrite(http.HandlerFunc(s.api.AckIncident)))
	mux.Handle("POST "+s.p("/api/v1/incidents/{id}/resolve"), incWrite(http.HandlerFunc(s.api.ResolveIncident)))
	mux.Handle("DELETE "+s.p("/api/v1/incidents/{id}"), incWrite(http.HandlerFunc(s.api.DeleteIncident)))

	mux.Handle("POST "+s.p("/api/v1/notifications"), notifWrite(http.HandlerFunc(s.api.CreateNotification)))
	mux.Handle("PUT "+s.p("/api/v1/notifications/{id}"), notifWrite(http.HandlerFunc(s.api.UpdateNotification)))
	mux.Handle("DELETE "+s.p("/api/v1/notifications/{id}"), notifWrite(http.HandlerFunc(s.api.DeleteNotification)))
	mux.Handle("POST "+s.p("/api/v1/notifications/{id}/test"), notifWrite(http.HandlerFunc(s.api.TestNotification)))

	mux.Handle("POST "+s.p("/api/v1/maintenance"), maintWrite(http.HandlerFunc(s.api.CreateMaintenance)))
	mux.Handle("PUT "+s.p("/api/v1/maintenance/{id}"), maintWrite(http.HandlerFunc(s.api.UpdateMaintenance)))
	mux.Handle("DELETE "+s.p("/api/v1/maintenance/{id}"), maintWrite(http.HandlerFunc(s.api.DeleteMaintenance)))

	epRead := s.api.Auth("escalation_policies.read")
	epWrite := s.api.Auth("escalation_policies.write")
	mux.Handle("GET "+s.p("/api/v1/escalation-policies"), epRead(http.HandlerFunc(s.api.ListEscalationPolicies)))
	mux.Handle("GET "+s.p("/api/v1/escalation-policies/{id}"), epRead(http.HandlerFunc(s.api.GetEscalationPolicy)))
	mux.Handle("POST "+s.p("/api/v1/escalation-policies"), epWrite(http.HandlerFunc(s.api.CreateEscalationPolicy)))
	mux.Handle("PUT "+s.p("/api/v1/escalation-policies/{id}"), epWrite(http.HandlerFunc(s.api.UpdateEscalationPolicy)))
	mux.Handle("DELETE "+s.p("/api/v1/escalation-policies/{id}"), epWrite(http.HandlerFunc(s.api.DeleteEscalationPolicy)))

	mux.Handle("GET "+s.p("/api/v1/proxies"), monRead(http.HandlerFunc(s.api.ListProxies)))
	mux.Handle("GET "+s.p("/api/v1/proxies/{id}"), monRead(http.HandlerFunc(s.api.GetProxy)))
	mux.Handle("POST "+s.p("/api/v1/proxies"), monWrite(http.HandlerFunc(s.api.CreateProxy)))
	mux.Handle("PUT "+s.p("/api/v1/proxies/{id}"), monWrite(http.HandlerFunc(s.api.UpdateProxy)))
	mux.Handle("DELETE "+s.p("/api/v1/proxies/{id}"), monWrite(http.HandlerFunc(s.api.DeleteProxy)))

	mux.Handle("GET "+s.p("/api/v1/status-pages"), monRead(http.HandlerFunc(s.api.ListStatusPages)))
	mux.Handle("GET "+s.p("/api/v1/status-pages/{id}"), monRead(http.HandlerFunc(s.api.GetStatusPage)))
	mux.Handle("POST "+s.p("/api/v1/status-pages"), monWrite(http.HandlerFunc(s.api.CreateStatusPage)))
	mux.Handle("PUT "+s.p("/api/v1/status-pages/{id}"), monWrite(http.HandlerFunc(s.api.UpdateStatusPage)))
	mux.Handle("DELETE "+s.p("/api/v1/status-pages/{id}"), monWrite(http.HandlerFunc(s.api.DeleteStatusPage)))
	mux.HandleFunc("GET "+s.p("/api/v1/status-pages/{id}/public"), s.api.PublicStatusPage)
	mux.HandleFunc("POST "+s.p("/api/v1/status-pages/{id}/subscribe"), s.api.SubscribeAPI)
	mux.Handle("GET "+s.p("/api/v1/status-pages/{id}/subscribers"), monRead(http.HandlerFunc(s.api.CountSubscribers)))
	mux.Handle("DELETE "+s.p("/api/v1/status-pages/{id}/subscribers/{subId}"), monWrite(http.HandlerFunc(s.api.DeleteSubscriber)))

	mux.Handle("GET "+s.p("/api/v1/request-logs"), metricsRead(http.HandlerFunc(s.api.ListRequestLogs)))
	mux.Handle("GET "+s.p("/api/v1/request-logs/stats"), metricsRead(http.HandlerFunc(s.api.RequestLogStats)))

	mux.Handle("GET "+s.p("/api/v1/audit"), metricsRead(http.HandlerFunc(s.api.ListAuditLog)))

	mux.Handle("GET "+s.p("/api/v1/db/size"), metricsRead(http.HandlerFunc(s.api.DBSize)))
	mux.Handle("POST "+s.p("/api/v1/db/vacuum"), monWrite(http.HandlerFunc(s.api.DBVacuum)))

	mux.Handle("GET "+s.p("/api/v1/export"), monRead(http.HandlerFunc(s.api.Export)))
	mux.Handle("POST "+s.p("/api/v1/import"), monWrite(http.HandlerFunc(s.api.Import)))
}
