package server

import (
	"io/fs"
	"net/http"

	webfs "github.com/y0f/asura/v3/web"
)

func (s *Server) registerRoutes(mux *http.ServeMux) {
	apiPerm := func(h http.HandlerFunc, scopes ...string) http.Handler {
		return s.api.Auth(scopes...)(h)
	}

	// The redirect target is the dashboard, which only exists with the web UI.
	if s.cfg.Server.BasePath != "" && s.cfg.IsWebUIEnabled() {
		mux.HandleFunc("GET "+s.cfg.Server.BasePath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, s.cfg.Server.BasePath+"/", http.StatusMovedPermanently)
		})
	}

	if !s.cfg.IsWebUIEnabled() {
		// With the web UI enabled, statusPageRouter serves these token links
		// without introducing ambiguous patterns alongside /static/.
		mux.HandleFunc("GET "+s.p("/{slug}/confirm"), s.subscriberLink(s.api.ConfirmSubscription))
		mux.HandleFunc("GET "+s.p("/{slug}/unsubscribe"), s.subscriberLink(s.api.Unsubscribe))
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
		webPerm := func(h http.HandlerFunc, scopes ...string) http.Handler {
			return webAuth(s.web.RequirePermissions(h, scopes...))
		}

		mux.Handle("POST "+s.p("/logout"), webAuth(http.HandlerFunc(s.web.Logout)))

		mux.Handle("GET "+s.p("/{$}"), webAuth(http.HandlerFunc(s.web.Home)))
		mux.Handle("GET "+s.p("/monitors"), webPerm(s.web.Monitors, "monitors.read"))
		mux.Handle("GET "+s.p("/monitors/new"), webPerm(s.web.MonitorForm, "monitors.create"))
		mux.Handle("GET "+s.p("/monitors/{id}"), webPerm(s.web.MonitorDetail, "monitors.read"))
		mux.Handle("GET "+s.p("/monitors/{id}/edit"), webPerm(s.web.MonitorForm, "monitors.read", "monitors.update"))
		mux.Handle("POST "+s.p("/monitors"), webPerm(s.web.MonitorCreate, "monitors.create"))
		mux.Handle("POST "+s.p("/monitors/{id}"), webPerm(s.web.MonitorUpdate, "monitors.update"))
		mux.Handle("POST "+s.p("/monitors/{id}/delete"), webPerm(s.web.MonitorDelete, "monitors.delete"))
		mux.Handle("POST "+s.p("/monitors/{id}/pause"), webPerm(s.web.MonitorPause, "monitors.pause"))
		mux.Handle("POST "+s.p("/monitors/{id}/resume"), webPerm(s.web.MonitorResume, "monitors.resume"))
		mux.Handle("POST "+s.p("/monitors/{id}/clone"), webPerm(s.web.MonitorClone, "monitors.read", "monitors.clone"))
		mux.Handle("POST "+s.p("/monitors/{id}/status"), webPerm(s.web.MonitorSetManualStatus, "monitors.status"))
		mux.Handle("POST "+s.p("/monitors/bulk"), webPerm(s.web.MonitorBulk))
		mux.Handle("GET "+s.p("/monitors/{id}/chart"), webPerm(s.api.MonitorChart, "monitors.read"))

		mux.Handle("GET "+s.p("/incidents"), webPerm(s.web.Incidents, "incidents.read"))
		mux.Handle("GET "+s.p("/incidents/{id}"), webPerm(s.web.IncidentDetail, "incidents.read"))
		mux.Handle("POST "+s.p("/incidents/{id}/ack"), webPerm(s.web.IncidentAck, "incidents.acknowledge"))
		mux.Handle("POST "+s.p("/incidents/{id}/resolve"), webPerm(s.web.IncidentResolve, "incidents.resolve"))
		mux.Handle("POST "+s.p("/incidents/{id}/delete"), webPerm(s.web.IncidentDelete, "incidents.delete"))

		mux.Handle("GET "+s.p("/groups"), webPerm(s.web.Groups, "groups.read"))
		mux.Handle("GET "+s.p("/groups/{id}"), webPerm(s.web.GroupDetail, "groups.read"))
		mux.Handle("POST "+s.p("/groups"), webPerm(s.web.GroupCreate, "groups.create"))
		mux.Handle("POST "+s.p("/groups/{id}"), webPerm(s.web.GroupUpdate, "groups.update"))
		mux.Handle("POST "+s.p("/groups/{id}/delete"), webPerm(s.web.GroupDelete, "groups.delete"))

		mux.Handle("GET "+s.p("/tags"), webPerm(s.web.Tags, "tags.read"))
		mux.Handle("POST "+s.p("/tags"), webPerm(s.web.TagCreate, "tags.create"))
		mux.Handle("POST "+s.p("/tags/{id}"), webPerm(s.web.TagUpdate, "tags.update"))
		mux.Handle("POST "+s.p("/tags/{id}/delete"), webPerm(s.web.TagDelete, "tags.delete"))

		mux.Handle("GET "+s.p("/notifications"), webPerm(s.web.Notifications, "notifications.read"))
		mux.Handle("GET "+s.p("/notifications/history"), webPerm(s.web.NotificationHistory, "notification_history.read"))
		mux.Handle("POST "+s.p("/notifications"), webPerm(s.web.NotificationCreate, "notifications.create"))
		mux.Handle("POST "+s.p("/notifications/{id}"), webPerm(s.web.NotificationUpdate, "notifications.update"))
		mux.Handle("POST "+s.p("/notifications/{id}/delete"), webPerm(s.web.NotificationDelete, "notifications.delete"))
		mux.Handle("POST "+s.p("/notifications/{id}/test"), webPerm(s.web.NotificationTest, "notifications.test"))

		mux.Handle("GET "+s.p("/escalation-policies"), webPerm(s.web.EscalationPolicies, "escalation_policies.read"))
		mux.Handle("POST "+s.p("/escalation-policies"), webPerm(s.web.EscalationPolicyCreate, "escalation_policies.create", "notifications.read"))
		mux.Handle("POST "+s.p("/escalation-policies/{id}"), webPerm(s.web.EscalationPolicyUpdate, "escalation_policies.update"))
		mux.Handle("POST "+s.p("/escalation-policies/{id}/delete"), webPerm(s.web.EscalationPolicyDelete, "escalation_policies.delete"))

		mux.Handle("GET "+s.p("/sla"), webPerm(s.web.SLAReport, "sla.read"))
		mux.Handle("GET "+s.p("/sla/export"), webPerm(s.web.SLAExport, "sla.read"))

		mux.Handle("GET "+s.p("/maintenance"), webPerm(s.web.Maintenance, "maintenance.read"))
		mux.Handle("POST "+s.p("/maintenance"), webPerm(s.web.MaintenanceCreate, "maintenance.create"))
		mux.Handle("POST "+s.p("/maintenance/{id}"), webPerm(s.web.MaintenanceUpdate, "maintenance.update"))
		mux.Handle("POST "+s.p("/maintenance/{id}/delete"), webPerm(s.web.MaintenanceDelete, "maintenance.delete"))
		mux.Handle("POST "+s.p("/maintenance/{id}/toggle"), webPerm(s.web.MaintenanceToggle, "maintenance.toggle"))

		mux.Handle("GET "+s.p("/logs"), webPerm(s.web.RequestLogs, "request_logs.read"))
		mux.Handle("GET "+s.p("/audit"), webPerm(s.web.AuditLog, "audit.read"))

		mux.Handle("GET "+s.p("/proxies"), webPerm(s.web.Proxies, "proxies.read"))
		mux.Handle("GET "+s.p("/proxies/new"), webPerm(s.web.ProxyForm, "proxies.create"))
		mux.Handle("GET "+s.p("/proxies/{id}/edit"), webPerm(s.web.ProxyForm, "proxies.read", "proxies.update"))
		mux.Handle("POST "+s.p("/proxies"), webPerm(s.web.ProxyCreate, "proxies.create"))
		mux.Handle("POST "+s.p("/proxies/{id}"), webPerm(s.web.ProxyUpdate, "proxies.update"))
		mux.Handle("POST "+s.p("/proxies/{id}/delete"), webPerm(s.web.ProxyDelete, "proxies.delete"))

		mux.Handle("GET "+s.p("/on-call"), webPerm(s.web.OnCallRotations, "on_call.read"))
		mux.Handle("POST "+s.p("/on-call"), webPerm(s.web.OnCallCreate, "on_call.create"))
		mux.Handle("POST "+s.p("/on-call/{id}/delete"), webPerm(s.web.OnCallDelete, "on_call.delete"))
		mux.Handle("POST "+s.p("/on-call/{id}/override"), webPerm(s.web.OnCallOverride, "on_call.override"))

		mux.Handle("GET "+s.p("/agents"), webPerm(s.web.Agents, "agents.read"))
		mux.Handle("POST "+s.p("/agents"), webPerm(s.web.AgentCreate, "agents.create"))
		mux.Handle("POST "+s.p("/agents/{id}/delete"), webPerm(s.web.AgentDelete, "agents.delete"))

		mux.Handle("GET "+s.p("/status-pages"), webPerm(s.web.StatusPages, "status_pages.read"))
		mux.Handle("GET "+s.p("/status-pages/new"), webPerm(s.web.StatusPageForm, "status_pages.create"))
		mux.Handle("GET "+s.p("/status-pages/{id}/edit"), webPerm(s.web.StatusPageForm, "status_pages.read", "status_pages.update"))
		mux.Handle("POST "+s.p("/status-pages"), webPerm(s.web.StatusPageCreate, "status_pages.create"))
		mux.Handle("POST "+s.p("/status-pages/{id}"), webPerm(s.web.StatusPageUpdate, "status_pages.update"))
		mux.Handle("POST "+s.p("/status-pages/{id}/delete"), webPerm(s.web.StatusPageDelete, "status_pages.delete"))

		mux.Handle("GET "+s.p("/settings"), webPerm(s.web.Settings, "settings.read"))
		mux.Handle("GET "+s.p("/settings/export"), webPerm(s.web.ExportConfig))
		mux.Handle("POST "+s.p("/settings/import"), webPerm(s.web.ImportConfig))
		mux.Handle("POST "+s.p("/settings/vacuum"), webPerm(s.web.DBVacuum, "database.maintain"))
		mux.Handle("GET "+s.p("/events"), webPerm(s.serveEvents))
	}

	mux.HandleFunc("GET "+s.p("/api/v1/health"), s.api.Health)
	mux.Handle("GET "+s.p("/api/v1/events"), apiPerm(s.serveEvents))
	mux.Handle("GET "+s.p("/metrics"), apiPerm(s.api.Metrics, "metrics.read"))
	mux.HandleFunc("POST "+s.p("/api/v1/heartbeat/{token}"), s.api.HeartbeatPing)
	mux.HandleFunc("GET "+s.p("/api/v1/heartbeat/{token}"), s.api.HeartbeatPing)
	mux.HandleFunc("GET "+s.p("/api/v1/badge/{id}/status"), s.api.BadgeStatus)
	mux.HandleFunc("GET "+s.p("/api/v1/badge/{id}/uptime"), s.api.BadgeUptime)
	mux.HandleFunc("GET "+s.p("/api/v1/badge/{id}/response"), s.api.BadgeResponseTime)
	mux.HandleFunc("GET "+s.p("/api/v1/badge/{id}/cert"), s.api.BadgeCert)

	mux.Handle("GET "+s.p("/api/v1/monitors"), apiPerm(s.api.ListMonitors, "monitors.read"))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}"), apiPerm(s.api.GetMonitor, "monitors.read"))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}/checks"), apiPerm(s.api.ListChecks, "monitors.read"))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}/metrics"), apiPerm(s.api.MonitorMetrics, "monitors.read"))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}/changes"), apiPerm(s.api.ListChanges, "monitors.read"))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}/chart"), apiPerm(s.api.MonitorChart, "monitors.read"))
	mux.Handle("GET "+s.p("/api/v1/monitors/{id}/sla"), apiPerm(s.api.MonitorSLA, "sla.read"))
	mux.Handle("GET "+s.p("/api/v1/reports/sla"), apiPerm(s.api.SLAReport, "sla.read"))
	mux.Handle("GET "+s.p("/api/v1/reports/sla/export"), apiPerm(s.api.SLAReportExport, "sla.read"))

	mux.Handle("GET "+s.p("/api/v1/incidents"), apiPerm(s.api.ListIncidents, "incidents.read"))
	mux.Handle("GET "+s.p("/api/v1/incidents/{id}"), apiPerm(s.api.GetIncident, "incidents.read"))

	mux.Handle("GET "+s.p("/api/v1/notifications"), apiPerm(s.api.ListNotifications, "notifications.read"))
	mux.Handle("GET "+s.p("/api/v1/notifications/history"), apiPerm(s.api.ListNotificationHistory, "notification_history.read"))
	mux.Handle("GET "+s.p("/api/v1/maintenance"), apiPerm(s.api.ListMaintenance, "maintenance.read"))
	mux.Handle("GET "+s.p("/api/v1/groups"), apiPerm(s.api.ListGroups, "groups.read"))
	mux.Handle("POST "+s.p("/api/v1/groups"), apiPerm(s.api.CreateGroup, "groups.create"))
	mux.Handle("PUT "+s.p("/api/v1/groups/{id}"), apiPerm(s.api.UpdateGroup, "groups.update"))
	mux.Handle("DELETE "+s.p("/api/v1/groups/{id}"), apiPerm(s.api.DeleteGroup, "groups.delete"))
	mux.Handle("GET "+s.p("/api/v1/overview"), apiPerm(s.api.Overview, "monitors.read"))
	mux.Handle("GET "+s.p("/api/v1/tags"), apiPerm(s.api.ListTags, "tags.read"))
	mux.Handle("POST "+s.p("/api/v1/tags"), apiPerm(s.api.CreateTag, "tags.create"))
	mux.Handle("PUT "+s.p("/api/v1/tags/{id}"), apiPerm(s.api.UpdateTag, "tags.update"))
	mux.Handle("DELETE "+s.p("/api/v1/tags/{id}"), apiPerm(s.api.DeleteTag, "tags.delete"))

	mux.Handle("POST "+s.p("/api/v1/monitors"), apiPerm(s.api.CreateMonitor, "monitors.create"))
	mux.Handle("PUT "+s.p("/api/v1/monitors/{id}"), apiPerm(s.api.UpdateMonitor, "monitors.update"))
	mux.Handle("DELETE "+s.p("/api/v1/monitors/{id}"), apiPerm(s.api.DeleteMonitor, "monitors.delete"))
	mux.Handle("POST "+s.p("/api/v1/monitors/{id}/pause"), apiPerm(s.api.PauseMonitor, "monitors.pause"))
	mux.Handle("POST "+s.p("/api/v1/monitors/{id}/resume"), apiPerm(s.api.ResumeMonitor, "monitors.resume"))
	mux.Handle("POST "+s.p("/api/v1/monitors/{id}/clone"), apiPerm(s.api.CloneMonitor, "monitors.read", "monitors.clone"))
	mux.Handle("POST "+s.p("/api/v1/monitors/{id}/status"), apiPerm(s.api.SetManualStatus, "monitors.status"))
	mux.Handle("POST "+s.p("/api/v1/monitors/bulk"), apiPerm(s.api.BulkMonitors))

	mux.Handle("POST "+s.p("/api/v1/incidents/{id}/ack"), apiPerm(s.api.AckIncident, "incidents.acknowledge"))
	mux.Handle("POST "+s.p("/api/v1/incidents/{id}/resolve"), apiPerm(s.api.ResolveIncident, "incidents.resolve"))
	mux.Handle("POST "+s.p("/api/v1/incidents/{id}/severity"), apiPerm(s.api.SetIncidentSeverity, "incidents.severity"))
	mux.Handle("DELETE "+s.p("/api/v1/incidents/{id}"), apiPerm(s.api.DeleteIncident, "incidents.delete"))

	mux.Handle("POST "+s.p("/api/v1/notifications"), apiPerm(s.api.CreateNotification, "notifications.create"))
	mux.Handle("PUT "+s.p("/api/v1/notifications/{id}"), apiPerm(s.api.UpdateNotification, "notifications.update"))
	mux.Handle("DELETE "+s.p("/api/v1/notifications/{id}"), apiPerm(s.api.DeleteNotification, "notifications.delete"))
	mux.Handle("POST "+s.p("/api/v1/notifications/{id}/test"), apiPerm(s.api.TestNotification, "notifications.test"))

	mux.Handle("POST "+s.p("/api/v1/maintenance"), apiPerm(s.api.CreateMaintenance, "maintenance.create"))
	mux.Handle("PUT "+s.p("/api/v1/maintenance/{id}"), apiPerm(s.api.UpdateMaintenance, "maintenance.update"))
	mux.Handle("POST "+s.p("/api/v1/maintenance/{id}/toggle"), apiPerm(s.api.ToggleMaintenance, "maintenance.toggle"))
	mux.Handle("DELETE "+s.p("/api/v1/maintenance/{id}"), apiPerm(s.api.DeleteMaintenance, "maintenance.delete"))

	mux.Handle("GET "+s.p("/api/v1/escalation-policies"), apiPerm(s.api.ListEscalationPolicies, "escalation_policies.read"))
	mux.Handle("GET "+s.p("/api/v1/escalation-policies/{id}"), apiPerm(s.api.GetEscalationPolicy, "escalation_policies.read"))
	mux.Handle("POST "+s.p("/api/v1/escalation-policies"), apiPerm(s.api.CreateEscalationPolicy, "escalation_policies.create", "notifications.read"))
	mux.Handle("PUT "+s.p("/api/v1/escalation-policies/{id}"), apiPerm(s.api.UpdateEscalationPolicy, "escalation_policies.update"))
	mux.Handle("DELETE "+s.p("/api/v1/escalation-policies/{id}"), apiPerm(s.api.DeleteEscalationPolicy, "escalation_policies.delete"))

	mux.Handle("GET "+s.p("/api/v1/proxies"), apiPerm(s.api.ListProxies, "proxies.read"))
	mux.Handle("GET "+s.p("/api/v1/proxies/{id}"), apiPerm(s.api.GetProxy, "proxies.read"))
	mux.Handle("POST "+s.p("/api/v1/proxies"), apiPerm(s.api.CreateProxy, "proxies.create"))
	mux.Handle("PUT "+s.p("/api/v1/proxies/{id}"), apiPerm(s.api.UpdateProxy, "proxies.update"))
	mux.Handle("DELETE "+s.p("/api/v1/proxies/{id}"), apiPerm(s.api.DeleteProxy, "proxies.delete"))

	mux.Handle("GET "+s.p("/api/v1/status-pages"), apiPerm(s.api.ListStatusPages, "status_pages.read"))
	mux.Handle("GET "+s.p("/api/v1/status-pages/{id}"), apiPerm(s.api.GetStatusPage, "status_pages.read"))
	mux.Handle("POST "+s.p("/api/v1/status-pages"), apiPerm(s.api.CreateStatusPage, "status_pages.create"))
	mux.Handle("PUT "+s.p("/api/v1/status-pages/{id}"), apiPerm(s.api.UpdateStatusPage, "status_pages.update"))
	mux.Handle("DELETE "+s.p("/api/v1/status-pages/{id}"), apiPerm(s.api.DeleteStatusPage, "status_pages.delete"))
	mux.HandleFunc("GET "+s.p("/api/v1/status-pages/{id}/public"), s.api.PublicStatusPage)
	mux.HandleFunc("POST "+s.p("/api/v1/status-pages/{id}/subscribe"), s.api.SubscribeAPI)
	mux.Handle("GET "+s.p("/api/v1/status-pages/{id}/subscribers"), apiPerm(s.api.ListSubscribers, "subscribers.read"))
	mux.Handle("DELETE "+s.p("/api/v1/status-pages/{id}/subscribers/{subId}"), apiPerm(s.api.DeleteSubscriber, "subscribers.delete"))

	mux.Handle("GET "+s.p("/api/v1/request-logs"), apiPerm(s.api.ListRequestLogs, "request_logs.read"))
	mux.Handle("GET "+s.p("/api/v1/request-logs/stats"), apiPerm(s.api.RequestLogStats, "request_logs.read"))

	mux.Handle("GET "+s.p("/api/v1/audit"), apiPerm(s.api.ListAuditLog, "audit.read"))

	mux.Handle("GET "+s.p("/api/v1/db/size"), apiPerm(s.api.DBSize, "database.read"))
	mux.Handle("POST "+s.p("/api/v1/db/vacuum"), apiPerm(s.api.DBVacuum, "database.maintain"))

	mux.Handle("GET "+s.p("/api/v1/export"), apiPerm(s.api.Export))
	mux.Handle("POST "+s.p("/api/v1/import"), apiPerm(s.api.Import))

	// On-call rotations
	mux.Handle("GET "+s.p("/api/v1/on-call"), apiPerm(s.api.ListOnCallRotations, "on_call.read"))
	mux.Handle("GET "+s.p("/api/v1/on-call/current"), apiPerm(s.api.GetCurrentOnCall, "on_call.read"))
	mux.Handle("POST "+s.p("/api/v1/on-call"), apiPerm(s.api.CreateOnCallRotation, "on_call.create"))
	mux.Handle("PUT "+s.p("/api/v1/on-call/{id}"), apiPerm(s.api.UpdateOnCallRotation, "on_call.update"))
	mux.Handle("POST "+s.p("/api/v1/on-call/{id}/override"), apiPerm(s.api.OnCallOverride, "on_call.override"))
	mux.Handle("DELETE "+s.p("/api/v1/on-call/{id}"), apiPerm(s.api.DeleteOnCallRotation, "on_call.delete"))

	// Agent admin endpoints (API key auth)
	mux.Handle("GET "+s.p("/api/v1/agents"), apiPerm(s.api.ListAgents, "agents.read"))
	mux.Handle("POST "+s.p("/api/v1/agents"), apiPerm(s.api.CreateAgent, "agents.create"))
	mux.Handle("DELETE "+s.p("/api/v1/agents/{id}"), apiPerm(s.api.DeleteAgentAPI, "agents.delete"))
	mux.Handle("GET "+s.p("/api/v1/agents/status"), apiPerm(s.api.AgentHealthAPI, "agents.read"))

	// Agent communication endpoints (agent token auth)
	agentAuth := s.api.AgentAuth
	mux.Handle("GET "+s.p("/api/v1/agent/jobs"), agentAuth(http.HandlerFunc(s.api.AgentGetJobs)))
	mux.Handle("POST "+s.p("/api/v1/agent/results"), agentAuth(http.HandlerFunc(s.api.AgentPostResults)))
}
