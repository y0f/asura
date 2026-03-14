package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/y0f/asura/internal/assertion"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/sla"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
	"github.com/y0f/asura/internal/web/views"
)

type headerPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func monitorToFormData(mon *storage.Monitor) *views.MonitorFormParams {
	fd := &views.MonitorFormParams{Monitor: mon}
	if mon == nil {
		fd.Monitor = &storage.Monitor{}
		fd.FollowRedirects = true
		fd.MaxRedirects = 10
		fd.HTTP.AuthMethod = "none"
		fd.HTTP.BodyEncoding = "json"
		fd.HeadersJSON = "[]"
		fd.WsHeadersJSON = "[]"
		fd.AssertionsJSON = `{"operator":"and","groups":[]}`
		fd.SettingsJSON = "{}"
		fd.AssertionsRaw = "{}"
		return fd
	}

	fd.SettingsJSON = "{}"
	if len(mon.Settings) > 0 {
		fd.SettingsJSON = string(mon.Settings)
	}
	fd.AssertionsRaw = "{}"
	if len(mon.Assertions) > 0 {
		fd.AssertionsRaw = string(mon.Assertions)
	}

	unmarshalMonitorSettings(fd, mon)
	applyHTTPDefaults(fd)

	fd.HeadersJSON = headersToJSON(fd.HTTP.Headers)
	fd.WsHeadersJSON = headersToJSON(fd.WS.Headers)
	fd.AssertionsJSON = assertionsToJSON(mon.Assertions)
	return fd
}

var _settingsTargets = map[string]func(*views.MonitorFormParams) any{
	"http":       func(fd *views.MonitorFormParams) any { return &fd.HTTP },
	"tcp":        func(fd *views.MonitorFormParams) any { return &fd.TCP },
	"dns":        func(fd *views.MonitorFormParams) any { return &fd.DNS },
	"tls":        func(fd *views.MonitorFormParams) any { return &fd.TLS },
	"websocket":  func(fd *views.MonitorFormParams) any { return &fd.WS },
	"command":    func(fd *views.MonitorFormParams) any { return &fd.Cmd },
	"docker":     func(fd *views.MonitorFormParams) any { return &fd.Docker },
	"domain":     func(fd *views.MonitorFormParams) any { return &fd.Domain },
	"grpc":       func(fd *views.MonitorFormParams) any { return &fd.GRPC },
	"mqtt":       func(fd *views.MonitorFormParams) any { return &fd.MQTT },
	"smtp":       func(fd *views.MonitorFormParams) any { return &fd.SMTP },
	"ssh":        func(fd *views.MonitorFormParams) any { return &fd.SSH },
	"redis":      func(fd *views.MonitorFormParams) any { return &fd.Redis },
	"postgresql": func(fd *views.MonitorFormParams) any { return &fd.PostgreSQL },
	"udp":        func(fd *views.MonitorFormParams) any { return &fd.UDP },
	"http_multi": func(fd *views.MonitorFormParams) any { return &fd.MultiStep },
}

func unmarshalMonitorSettings(fd *views.MonitorFormParams, mon *storage.Monitor) {
	if fn, ok := _settingsTargets[mon.Type]; ok {
		json.Unmarshal(mon.Settings, fn(fd))
	}
}

func applyHTTPDefaults(fd *views.MonitorFormParams) {
	fd.FollowRedirects = fd.HTTP.FollowRedirects == nil || *fd.HTTP.FollowRedirects
	fd.MaxRedirects = fd.HTTP.MaxRedirects
	if fd.MaxRedirects == 0 && fd.FollowRedirects {
		fd.MaxRedirects = 10
	}
	fd.HTTP.AuthMethod = inferHTTPAuthMethod(fd.HTTP)
	if fd.HTTP.BodyEncoding == "" {
		fd.HTTP.BodyEncoding = "json"
	}
}

func inferHTTPAuthMethod(h storage.HTTPSettings) string {
	if h.AuthMethod != "" {
		return h.AuthMethod
	}
	if h.BasicAuthUser != "" {
		return "basic"
	}
	if h.BearerToken != "" {
		return "bearer"
	}
	if h.OAuth2TokenURL != "" {
		return "oauth2"
	}
	return "none"
}

func headersToJSON(headers map[string]string) string {
	if len(headers) == 0 {
		return "[]"
	}
	pairs := make([]headerPair, 0, len(headers))
	for k, v := range headers {
		pairs = append(pairs, headerPair{Key: k, Value: v})
	}
	return views.ToJSON(pairs)
}

func assertionsToJSON(raw json.RawMessage) string {
	var cs assertion.ConditionSet
	if len(raw) > 0 {
		json.Unmarshal(raw, &cs)
	}
	if cs.Operator == "" {
		cs.Operator = "and"
	}
	if cs.Groups == nil {
		cs.Groups = []assertion.ConditionGroup{}
	}
	return views.ToJSON(cs)
}

var _settingsAssemblers = map[string]func(*http.Request) json.RawMessage{
	"http": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(assembleHTTPSettings(r))
		return b
	},
	"tcp": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(storage.TCPSettings{
			SendData:   r.FormValue("settings_send_data"),
			ExpectData: r.FormValue("settings_expect_data"),
		})
		return b
	},
	"dns": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(storage.DNSSettings{
			RecordType: r.FormValue("settings_record_type"),
			Server:     r.FormValue("settings_dns_server"),
		})
		return b
	},
	"tls": func(r *http.Request) json.RawMessage {
		s := storage.TLSSettings{}
		if v := r.FormValue("settings_warn_days_before"); v != "" {
			s.WarnDaysBefore, _ = strconv.Atoi(v)
		}
		b, _ := json.Marshal(s)
		return b
	},
	"websocket": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(storage.WebSocketSettings{
			SendMessage: r.FormValue("settings_send_message"),
			ExpectReply: r.FormValue("settings_expect_reply"),
			Headers:     assembleHeaders(r, "settings_ws_header_key", "settings_ws_header_value"),
		})
		return b
	},
	"command": assembleCommandSettings,
	"docker": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(storage.DockerSettings{
			ContainerName: r.FormValue("settings_container_name"),
			SocketPath:    r.FormValue("settings_socket_path"),
			CheckHealth:   r.FormValue("settings_check_health") == "on",
		})
		return b
	},
	"domain": func(r *http.Request) json.RawMessage {
		s := storage.DomainSettings{}
		if v := r.FormValue("settings_domain_warn_days"); v != "" {
			s.WarnDaysBefore, _ = strconv.Atoi(v)
		}
		b, _ := json.Marshal(s)
		return b
	},
	"grpc": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(storage.GRPCSettings{
			ServiceName:   r.FormValue("settings_grpc_service"),
			UseTLS:        r.FormValue("settings_grpc_tls") == "on",
			SkipTLSVerify: r.FormValue("settings_grpc_skip_verify") == "on",
		})
		return b
	},
	"mqtt": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(storage.MQTTSettings{
			ClientID:      r.FormValue("settings_mqtt_client_id"),
			Username:      r.FormValue("settings_mqtt_username"),
			Password:      r.FormValue("settings_mqtt_password"),
			Topic:         r.FormValue("settings_mqtt_topic"),
			ExpectMessage: r.FormValue("settings_mqtt_expect"),
			UseTLS:        r.FormValue("settings_mqtt_tls") == "on",
		})
		return b
	},
	"smtp": func(r *http.Request) json.RawMessage {
		s := storage.SMTPSettings{
			STARTTLS:     r.FormValue("settings_smtp_starttls") == "on",
			ExpectBanner: r.FormValue("settings_smtp_expect_banner"),
		}
		if v := r.FormValue("settings_smtp_port"); v != "" {
			s.Port, _ = strconv.Atoi(v)
		}
		b, _ := json.Marshal(s)
		return b
	},
	"ssh": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(storage.SSHSettings{
			ExpectedFingerprint: r.FormValue("settings_ssh_fingerprint"),
		})
		return b
	},
	"redis": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(storage.RedisSettings{
			Password: r.FormValue("settings_redis_password"),
		})
		return b
	},
	"postgresql": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(storage.PostgreSQLSettings{
			Username: r.FormValue("settings_pg_username"),
			Database: r.FormValue("settings_pg_database"),
		})
		return b
	},
	"udp": func(r *http.Request) json.RawMessage {
		b, _ := json.Marshal(storage.UDPSettings{
			SendData:   r.FormValue("settings_udp_send"),
			ExpectData: r.FormValue("settings_udp_expect"),
		})
		return b
	},
}

func assembleSettings(r *http.Request, monType string) json.RawMessage {
	if fn, ok := _settingsAssemblers[monType]; ok {
		return fn(r)
	}
	return nil
}

func assembleCommandSettings(r *http.Request) json.RawMessage {
	s := storage.CommandSettings{Command: r.FormValue("settings_command")}
	if argsStr := strings.TrimSpace(r.FormValue("settings_args")); argsStr != "" {
		for _, a := range strings.Split(argsStr, ",") {
			if trimmed := strings.TrimSpace(a); trimmed != "" {
				s.Args = append(s.Args, trimmed)
			}
		}
	}
	b, _ := json.Marshal(s)
	return b
}

func assembleHTTPSettings(r *http.Request) storage.HTTPSettings {
	s := storage.HTTPSettings{
		Method:       r.FormValue("settings_method"),
		Body:         r.FormValue("settings_body"),
		BodyEncoding: r.FormValue("settings_body_encoding"),
		AuthMethod:   r.FormValue("settings_auth_method"),
	}
	if v := r.FormValue("settings_expected_status"); v != "" {
		s.ExpectedStatus, _ = strconv.Atoi(v)
	}
	if v := r.FormValue("settings_max_redirects"); v != "" {
		s.MaxRedirects, _ = strconv.Atoi(v)
		if s.MaxRedirects == 0 {
			f := false
			s.FollowRedirects = &f
		}
	}
	s.SkipTLSVerify = r.FormValue("settings_skip_tls_verify") == "on"
	s.CacheBuster = r.FormValue("settings_cache_buster") == "on"
	s.Headers = assembleHeaders(r, "settings_header_key", "settings_header_value")
	switch s.AuthMethod {
	case "basic":
		s.BasicAuthUser = r.FormValue("settings_basic_auth_user")
		s.BasicAuthPass = r.FormValue("settings_basic_auth_pass")
	case "bearer":
		s.BearerToken = r.FormValue("settings_bearer_token")
	case "oauth2":
		s.OAuth2TokenURL = r.FormValue("settings_oauth2_token_url")
		s.OAuth2ClientID = r.FormValue("settings_oauth2_client_id")
		s.OAuth2ClientSecret = r.FormValue("settings_oauth2_client_secret")
		s.OAuth2Scopes = r.FormValue("settings_oauth2_scopes")
		s.OAuth2Audience = r.FormValue("settings_oauth2_audience")
	}
	s.MTLSEnabled = r.FormValue("settings_mtls_enabled") == "on"
	if s.MTLSEnabled {
		s.MTLSClientCert = r.FormValue("settings_mtls_client_cert")
		s.MTLSClientKey = r.FormValue("settings_mtls_client_key")
		s.MTLSCACert = r.FormValue("settings_mtls_ca_cert")
	}
	return s
}

func assembleHeaders(r *http.Request, keyField, valueField string) map[string]string {
	keys := r.Form[keyField+"[]"]
	values := r.Form[valueField+"[]"]
	if len(keys) == 0 {
		keys = r.Form[keyField]
		values = r.Form[valueField]
	}
	headers := make(map[string]string)
	for i, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || i >= len(values) {
			continue
		}
		headers[k] = values[i]
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func assembleAssertions(r *http.Request) json.RawMessage {
	return assembleConditionSet(r)
}

func assembleConditionSet(r *http.Request) json.RawMessage {
	groupCount, _ := strconv.Atoi(r.FormValue("group_count"))
	if groupCount == 0 {
		return nil
	}
	if groupCount > 20 {
		groupCount = 20
	}

	cs := assertion.ConditionSet{
		Operator: r.FormValue("condition_set_operator"),
	}
	if cs.Operator != "or" {
		cs.Operator = "and"
	}

	for g := 0; g < groupCount; g++ {
		gi := strconv.Itoa(g)
		condCount, _ := strconv.Atoi(r.FormValue("group_" + gi + "_count"))
		if condCount == 0 {
			continue
		}
		if condCount > 20 {
			condCount = 20
		}

		grp := assertion.ConditionGroup{
			Operator: r.FormValue("group_" + gi + "_operator"),
		}
		if grp.Operator != "or" {
			grp.Operator = "and"
		}

		for c := 0; c < condCount; c++ {
			ci := strconv.Itoa(c)
			aType := r.FormValue("group_" + gi + "_type_" + ci)
			if aType == "" {
				continue
			}
			grp.Conditions = append(grp.Conditions, assertion.Assertion{
				Type:     aType,
				Operator: r.FormValue("group_" + gi + "_operator_" + ci),
				Target:   r.FormValue("group_" + gi + "_target_" + ci),
				Value:    r.FormValue("group_" + gi + "_value_" + ci),
				Degraded: r.FormValue("group_"+gi+"_degraded_"+ci) == "on",
			})
		}

		if len(grp.Conditions) > 0 {
			cs.Groups = append(cs.Groups, grp)
		}
	}

	if len(cs.Groups) == 0 {
		return nil
	}
	b, _ := json.Marshal(cs)
	return b
}

func (h *Handler) Monitors(w http.ResponseWriter, r *http.Request) {
	p := httputil.ParsePagination(r)
	if p.PerPage == 20 {
		p.PerPage = 15
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	typeFilter := r.URL.Query().Get("type")
	if !validate.ValidMonitorTypes[typeFilter] {
		typeFilter = ""
	}

	var tagFilter *int64
	if v := r.URL.Query().Get("tag"); v != "" {
		if tid, err := strconv.ParseInt(v, 10, 64); err == nil {
			tagFilter = &tid
		}
	}

	var groupFilter *int64
	if v := r.URL.Query().Get("group"); v != "" {
		if gid, err := strconv.ParseInt(v, 10, 64); err == nil {
			groupFilter = &gid
		}
	}

	statusFilter := r.URL.Query().Get("status")
	validStatuses := map[string]bool{"up": true, "down": true, "degraded": true, "paused": true}
	if !validStatuses[statusFilter] {
		statusFilter = ""
	}

	sortParam := r.URL.Query().Get("sort")
	validSorts := map[string]bool{"name": true, "status": true, "last_check": true, "response_time": true}
	if !validSorts[sortParam] {
		sortParam = ""
	}

	f := storage.MonitorListFilter{Type: typeFilter, Search: q, TagID: tagFilter, GroupID: groupFilter, Status: statusFilter, Sort: sortParam}
	result, err := h.store.ListMonitors(r.Context(), f, p)
	if err != nil {
		h.logger.Error("web: list monitors", "error", err)
	}

	var tagMap map[int64][]storage.MonitorTag
	if result != nil {
		if monList, ok := result.Data.([]*storage.Monitor); ok && len(monList) > 0 {
			ids := make([]int64, len(monList))
			for i, m := range monList {
				ids[i] = m.ID
			}
			tagMap, _ = h.store.GetMonitorTagsBatch(r.Context(), ids)
		}
	}
	if tagMap == nil {
		tagMap = map[int64][]storage.MonitorTag{}
	}

	groups, _ := h.store.ListMonitorGroups(r.Context())
	allTags, _ := h.store.ListTags(r.Context())

	lp := h.newLayoutParams(r, "Monitors", "monitors")
	h.renderComponent(w, r, views.MonitorListPage(views.MonitorListParams{
		LayoutParams: lp,
		Result:       result,
		Search:       q,
		Type:         typeFilter,
		Groups:       groups,
		TagMap:       tagMap,
		AllTags:      allTags,
		TagFilter:    tagFilter,
		GroupFilter:  groupFilter,
		StatusFilter: statusFilter,
		SortParam:    sortParam,
	}))
}

func (h *Handler) MonitorDetail(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/monitors")
		return
	}

	ctx := r.Context()
	mon, err := h.store.GetMonitor(ctx, id)
	if err != nil {
		h.redirect(w, r, "/monitors")
		return
	}

	checksPage, _ := strconv.Atoi(r.URL.Query().Get("checks_page"))
	if checksPage < 1 {
		checksPage = 1
	}
	changesPage, _ := strconv.Atoi(r.URL.Query().Get("changes_page"))
	if changesPage < 1 {
		changesPage = 1
	}

	now := time.Now().UTC()
	checks, _ := h.store.ListCheckResults(ctx, id, storage.Pagination{Page: checksPage, PerPage: 10})
	if checks == nil {
		checks = &storage.PaginatedResult{}
	}
	changes, _ := h.store.ListContentChanges(ctx, id, storage.Pagination{Page: changesPage, PerPage: 10})
	if changes == nil {
		changes = &storage.PaginatedResult{}
	}

	uptime24h, _ := h.store.GetUptimePercent(ctx, id, now.Add(-24*time.Hour), now)
	uptime7d, _ := h.store.GetUptimePercent(ctx, id, now.Add(-7*24*time.Hour), now)
	uptime30d, _ := h.store.GetUptimePercent(ctx, id, now.Add(-30*24*time.Hour), now)
	p50, p95, p99, _ := h.store.GetResponseTimePercentiles(ctx, id, now.Add(-24*time.Hour), now)
	totalChecks, upChecks, downChecks, _, _ := h.store.GetCheckCounts(ctx, id, now.Add(-24*time.Hour), now)
	latestCheck, _ := h.store.GetLatestCheckResult(ctx, id)
	openIncident, _ := h.store.GetOpenIncident(ctx, id)
	monTags, _ := h.store.GetMonitorTags(ctx, id)

	var slaData *views.SLAData
	if mon.SLATarget > 0 {
		s, err := sla.Compute(ctx, h.store, mon.ID, mon.SLATarget)
		if err == nil {
			slaData = &views.SLAData{
				Target:            s.Target,
				UptimeMonth:       s.UptimePctMonth,
				BudgetTotalSecs:   s.BudgetTotalSecs,
				BudgetRemainSecs:  s.BudgetRemainSecs,
				BudgetRemainHuman: s.BudgetRemainHuman,
				Breached:          s.Breached,
			}
		}
	}

	heatmap := buildHeatmap(ctx, h.store, id, now)

	lp := h.newLayoutParams(r, mon.Name, "monitors")
	h.renderComponent(w, r, views.MonitorDetailPage(views.MonitorDetailParams{
		LayoutParams: lp,
		Monitor:      mon,
		Checks:       checks,
		Changes:      changes,
		ChecksPage:   checksPage,
		ChangesPage:  changesPage,
		Uptime24h:    uptime24h,
		Uptime7d:     uptime7d,
		Uptime30d:    uptime30d,
		P50:          p50,
		P95:          p95,
		P99:          p99,
		TotalChecks:  totalChecks,
		UpChecks:     upChecks,
		DownChecks:   downChecks,
		LatestCheck:  latestCheck,
		OpenIncident: openIncident,
		Tags:         monTags,
		SLA:          slaData,
		Heatmap:      heatmap,
	}))
}

func buildHeatmap(ctx context.Context, store storage.Store, monitorID int64, now time.Time) []views.HeatmapDay {
	from := now.AddDate(-1, 0, 0)
	daily, err := store.GetDailyUptime(ctx, monitorID, from, now)
	if err != nil {
		return nil
	}
	dayMap := make(map[string]*storage.DailyUptime, len(daily))
	for _, d := range daily {
		dayMap[d.Date] = d
	}

	startDay := from
	for startDay.Weekday() != time.Sunday {
		startDay = startDay.AddDate(0, 0, -1)
	}

	var days []views.HeatmapDay
	for d := startDay; !d.After(now); d = d.AddDate(0, 0, 1) {
		dateStr := d.Format("2006-01-02")
		label := d.Format("Jan 2, 2006")
		weekday := int(d.Weekday())
		week := int(d.Sub(startDay).Hours() / 24 / 7)

		hd := views.HeatmapDay{
			Date:    dateStr,
			Label:   label,
			Weekday: weekday,
			Week:    week,
		}
		if du, ok := dayMap[dateStr]; ok {
			hd.UptimePct = du.UptimePct
			hd.HasData = true
		}
		days = append(days, hd)
	}
	return days
}

func (h *Handler) renderMonitorForm(w http.ResponseWriter, r *http.Request, lp views.LayoutParams, fd *views.MonitorFormParams) {
	fd.LayoutParams = lp
	h.renderComponent(w, r, views.MonitorFormPage(*fd))
}

func (h *Handler) MonitorForm(w http.ResponseWriter, r *http.Request) {
	lp := h.newLayoutParams(r, "New Monitor", "monitors")

	groups, _ := h.store.ListMonitorGroups(r.Context())
	channels, _ := h.store.ListNotificationChannels(r.Context())
	proxies, _ := h.store.ListProxies(r.Context())
	allTags, _ := h.store.ListTags(r.Context())
	escalationPolicies, _ := h.store.ListEscalationPolicies(r.Context())

	idStr := r.PathValue("id")
	if idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			h.redirect(w, r, "/monitors")
			return
		}
		mon, err := h.store.GetMonitor(r.Context(), id)
		if err != nil {
			h.redirect(w, r, "/monitors")
			return
		}
		lp.Title = "Edit " + mon.Name
		fd := monitorToFormData(mon)
		fd.Groups = groups
		fd.NotificationChannels = channels
		fd.Proxies = proxies
		fd.AllTags = allTags
		fd.EscalationPolicies = escalationPolicies
		fd.SelectedChannelIDs, _ = h.store.GetMonitorNotificationChannelIDs(r.Context(), id)
		fd.SelectedTags, _ = h.store.GetMonitorTags(r.Context(), id)
		h.renderMonitorForm(w, r, lp, fd)
	} else {
		fd := monitorToFormData(nil)
		fd.Groups = groups
		fd.NotificationChannels = channels
		fd.Proxies = proxies
		fd.AllTags = allTags
		fd.EscalationPolicies = escalationPolicies
		h.renderMonitorForm(w, r, lp, fd)
	}
}

func (h *Handler) MonitorCreate(w http.ResponseWriter, r *http.Request) {
	mon, channelIDs, monTags := h.parseMonitorForm(r)

	h.applyMonitorDefaults(mon)

	if err := validate.ValidateMonitor(mon); err != nil {
		groups, _ := h.store.ListMonitorGroups(r.Context())
		channels, _ := h.store.ListNotificationChannels(r.Context())
		proxies, _ := h.store.ListProxies(r.Context())
		allTags, _ := h.store.ListTags(r.Context())
		escalationPolicies, _ := h.store.ListEscalationPolicies(r.Context())
		lp := h.newLayoutParams(r, "New Monitor", "monitors")
		lp.Error = err.Error()
		fd := monitorToFormData(mon)
		fd.Groups = groups
		fd.NotificationChannels = channels
		fd.Proxies = proxies
		fd.AllTags = allTags
		fd.EscalationPolicies = escalationPolicies
		fd.SelectedChannelIDs = channelIDs
		fd.SelectedTags = monTags
		h.renderMonitorForm(w, r, lp, fd)
		return
	}

	if err := h.store.CreateMonitor(r.Context(), mon); err != nil {
		groups, _ := h.store.ListMonitorGroups(r.Context())
		channels, _ := h.store.ListNotificationChannels(r.Context())
		proxies, _ := h.store.ListProxies(r.Context())
		allTags, _ := h.store.ListTags(r.Context())
		escalationPolicies, _ := h.store.ListEscalationPolicies(r.Context())
		h.logger.Error("web: create monitor", "error", err)
		lp := h.newLayoutParams(r, "New Monitor", "monitors")
		lp.Error = "Failed to create monitor"
		fd := monitorToFormData(mon)
		fd.Groups = groups
		fd.NotificationChannels = channels
		fd.Proxies = proxies
		fd.AllTags = allTags
		fd.EscalationPolicies = escalationPolicies
		fd.SelectedChannelIDs = channelIDs
		fd.SelectedTags = monTags
		h.renderMonitorForm(w, r, lp, fd)
		return
	}

	if len(channelIDs) > 0 {
		if err := h.store.SetMonitorNotificationChannels(r.Context(), mon.ID, channelIDs); err != nil {
			h.logger.Error("web: set monitor notification channels", "error", err)
		}
	}

	if len(monTags) > 0 {
		if err := h.store.SetMonitorTags(r.Context(), mon.ID, monTags); err != nil {
			h.logger.Error("web: set monitor tags", "error", err)
		}
	}

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	h.setFlash(w, "Monitor created")
	h.redirect(w, r, "/monitors")
}

func (h *Handler) MonitorUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/monitors")
		return
	}

	mon, channelIDs, monTags := h.parseMonitorForm(r)
	mon.ID = id

	if err := validate.ValidateMonitor(mon); err != nil {
		groups, _ := h.store.ListMonitorGroups(r.Context())
		channels, _ := h.store.ListNotificationChannels(r.Context())
		proxies, _ := h.store.ListProxies(r.Context())
		allTags, _ := h.store.ListTags(r.Context())
		escalationPolicies, _ := h.store.ListEscalationPolicies(r.Context())
		lp := h.newLayoutParams(r, "Edit Monitor", "monitors")
		lp.Error = err.Error()
		fd := monitorToFormData(mon)
		fd.Groups = groups
		fd.NotificationChannels = channels
		fd.Proxies = proxies
		fd.AllTags = allTags
		fd.EscalationPolicies = escalationPolicies
		fd.SelectedChannelIDs = channelIDs
		fd.SelectedTags = monTags
		h.renderMonitorForm(w, r, lp, fd)
		return
	}

	if err := h.store.UpdateMonitor(r.Context(), mon); err != nil {
		groups, _ := h.store.ListMonitorGroups(r.Context())
		channels, _ := h.store.ListNotificationChannels(r.Context())
		proxies, _ := h.store.ListProxies(r.Context())
		allTags, _ := h.store.ListTags(r.Context())
		escalationPolicies, _ := h.store.ListEscalationPolicies(r.Context())
		h.logger.Error("web: update monitor", "error", err)
		lp := h.newLayoutParams(r, "Edit Monitor", "monitors")
		lp.Error = "Failed to update monitor"
		fd := monitorToFormData(mon)
		fd.Groups = groups
		fd.NotificationChannels = channels
		fd.Proxies = proxies
		fd.AllTags = allTags
		fd.EscalationPolicies = escalationPolicies
		fd.SelectedChannelIDs = channelIDs
		fd.SelectedTags = monTags
		h.renderMonitorForm(w, r, lp, fd)
		return
	}

	if err := h.store.SetMonitorNotificationChannels(r.Context(), id, channelIDs); err != nil {
		h.logger.Error("web: set monitor notification channels", "error", err)
	}

	if err := h.store.SetMonitorTags(r.Context(), id, monTags); err != nil {
		h.logger.Error("web: set monitor tags", "error", err)
	}

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	h.setFlash(w, "Monitor updated")
	h.redirect(w, r, "/monitors/"+strconv.FormatInt(id, 10))
}

func (h *Handler) MonitorDelete(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/monitors")
		return
	}

	if err := h.store.DeleteMonitor(r.Context(), id); err != nil {
		h.logger.Error("web: delete monitor", "error", err)
	}

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	h.setFlash(w, "Monitor deleted")
	h.redirect(w, r, "/monitors")
}

func (h *Handler) MonitorPause(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/monitors")
		return
	}
	if err := h.store.SetMonitorEnabled(r.Context(), id, false); err != nil {
		h.logger.Error("web: pause monitor", "error", err)
		h.setFlash(w, "Failed to pause monitor")
		h.redirect(w, r, "/monitors/"+strconv.FormatInt(id, 10))
		return
	}
	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}
	h.setFlash(w, "Monitor paused")
	h.redirect(w, r, "/monitors/"+strconv.FormatInt(id, 10))
}

func (h *Handler) MonitorResume(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/monitors")
		return
	}
	if err := h.store.SetMonitorEnabled(r.Context(), id, true); err != nil {
		h.logger.Error("web: resume monitor", "error", err)
		h.setFlash(w, "Failed to resume monitor")
		h.redirect(w, r, "/monitors/"+strconv.FormatInt(id, 10))
		return
	}
	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}
	h.setFlash(w, "Monitor resumed")
	h.redirect(w, r, "/monitors/"+strconv.FormatInt(id, 10))
}

func (h *Handler) MonitorSetManualStatus(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/monitors")
		return
	}

	status := r.FormValue("status")
	if status != "up" && status != "down" && status != "degraded" {
		h.setFlash(w, "Invalid status")
		h.redirect(w, r, "/monitors/"+strconv.FormatInt(id, 10))
		return
	}

	mon, err := h.store.GetMonitor(r.Context(), id)
	if err != nil {
		h.setFlash(w, "Monitor not found")
		h.redirect(w, r, "/monitors")
		return
	}

	if mon.Type != "manual" {
		h.setFlash(w, "Status can only be set on manual monitors")
		h.redirect(w, r, "/monitors/"+strconv.FormatInt(id, 10))
		return
	}

	message := r.FormValue("message")
	if h.pipeline != nil {
		h.pipeline.ProcessManualStatus(r.Context(), mon, status, message)
	}

	h.audit(r, "set_status", "monitor", id, status)
	h.setFlash(w, "Status set to "+status)
	h.redirect(w, r, "/monitors/"+strconv.FormatInt(id, 10))
}

func (h *Handler) MonitorClone(w http.ResponseWriter, r *http.Request) {
	id, err := httputil.ParseID(r)
	if err != nil {
		h.redirect(w, r, "/monitors")
		return
	}

	ctx := r.Context()
	src, err := h.store.GetMonitor(ctx, id)
	if err != nil {
		h.setFlash(w, "Monitor not found")
		h.redirect(w, r, "/monitors")
		return
	}

	clone := &storage.Monitor{
		Name:               src.Name + " (copy)",
		Description:        src.Description,
		Type:               src.Type,
		Target:             src.Target,
		Interval:           src.Interval,
		Timeout:            src.Timeout,
		Enabled:            false,
		Settings:           src.Settings,
		Assertions:         src.Assertions,
		TrackChanges:       src.TrackChanges,
		FailureThreshold:   src.FailureThreshold,
		SuccessThreshold:   src.SuccessThreshold,
		UpsideDown:         src.UpsideDown,
		ResendInterval:     src.ResendInterval,
		SLATarget:          src.SLATarget,
		AnomalySensitivity: src.AnomalySensitivity,
		GroupID:            src.GroupID,
		ProxyID:            src.ProxyID,
		EscalationPolicyID: src.EscalationPolicyID,
	}

	if err := h.store.CreateMonitor(ctx, clone); err != nil {
		h.logger.Error("web: clone monitor", "error", err)
		h.setFlash(w, "Failed to clone monitor")
		h.redirect(w, r, "/monitors/"+strconv.FormatInt(id, 10))
		return
	}

	channelIDs, _ := h.store.GetMonitorNotificationChannelIDs(ctx, id)
	if len(channelIDs) > 0 {
		if err := h.store.SetMonitorNotificationChannels(ctx, clone.ID, channelIDs); err != nil {
			h.logger.Error("web: clone monitor channels", "error", err)
		}
	}

	srcTags, _ := h.store.GetMonitorTags(ctx, id)
	if len(srcTags) > 0 {
		if err := h.store.SetMonitorTags(ctx, clone.ID, srcTags); err != nil {
			h.logger.Error("web: clone monitor tags", "error", err)
		}
	}

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}

	h.setFlash(w, "Monitor cloned")
	h.redirect(w, r, "/monitors/"+strconv.FormatInt(clone.ID, 10)+"/edit")
}

func (h *Handler) MonitorBulk(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	action := r.FormValue("action")
	ids := parseIDList(r.Form["ids[]"])
	if len(ids) == 0 {
		h.setFlash(w, "No monitors selected")
		h.redirect(w, r, "/monitors")
		return
	}

	ctx := r.Context()
	var msg string

	switch action {
	case "pause":
		if _, err := h.store.BulkSetMonitorsEnabled(ctx, ids, false); err != nil {
			h.logger.Error("web: bulk pause", "error", err)
			h.setFlash(w, "Failed to pause monitors")
			h.redirect(w, r, "/monitors")
			return
		}
		msg = strconv.Itoa(len(ids)) + " monitors paused"
	case "resume":
		if _, err := h.store.BulkSetMonitorsEnabled(ctx, ids, true); err != nil {
			h.logger.Error("web: bulk resume", "error", err)
			h.setFlash(w, "Failed to resume monitors")
			h.redirect(w, r, "/monitors")
			return
		}
		msg = strconv.Itoa(len(ids)) + " monitors resumed"
	case "delete":
		if _, err := h.store.BulkDeleteMonitors(ctx, ids); err != nil {
			h.logger.Error("web: bulk delete", "error", err)
			h.setFlash(w, "Failed to delete monitors")
			h.redirect(w, r, "/monitors")
			return
		}
		msg = strconv.Itoa(len(ids)) + " monitors deleted"
	case "set_group":
		var gid *int64
		if v := r.FormValue("group_id"); v != "" {
			if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
				gid = &parsed
			}
		}
		if _, err := h.store.BulkSetMonitorGroup(ctx, ids, gid); err != nil {
			h.logger.Error("web: bulk set group", "error", err)
			h.setFlash(w, "Failed to update monitors")
			h.redirect(w, r, "/monitors")
			return
		}
		msg = strconv.Itoa(len(ids)) + " monitors updated"
	default:
		h.setFlash(w, "Invalid action")
		h.redirect(w, r, "/monitors")
		return
	}

	if h.pipeline != nil {
		h.pipeline.ReloadMonitors()
	}
	h.setFlash(w, msg)
	h.redirect(w, r, "/monitors")
}

func (h *Handler) applyMonitorDefaults(m *storage.Monitor) {
	if m.Interval == 0 {
		m.Interval = int(h.cfg.Monitor.DefaultInterval.Seconds())
	}
	if m.Timeout == 0 {
		m.Timeout = int(h.cfg.Monitor.DefaultTimeout.Seconds())
	}
	if m.FailureThreshold == 0 {
		m.FailureThreshold = h.cfg.Monitor.FailureThreshold
	}
	if m.SuccessThreshold == 0 {
		m.SuccessThreshold = h.cfg.Monitor.SuccessThreshold
	}
	if m.Type == "heartbeat" && m.Target == "" {
		m.Target = "heartbeat"
	}
	if m.Type == "manual" && m.Target == "" {
		m.Target = "manual"
	}
}

func (h *Handler) parseMonitorForm(r *http.Request) (*storage.Monitor, []int64, []storage.MonitorTag) {
	r.ParseForm()

	interval, _ := strconv.Atoi(r.FormValue("interval"))
	timeout, _ := strconv.Atoi(r.FormValue("timeout"))
	failThreshold, _ := strconv.Atoi(r.FormValue("failure_threshold"))
	successThreshold, _ := strconv.Atoi(r.FormValue("success_threshold"))

	mon := &storage.Monitor{
		Name:             r.FormValue("name"),
		Description:      r.FormValue("description"),
		Type:             r.FormValue("type"),
		Target:           r.FormValue("target"),
		Interval:         interval,
		Timeout:          timeout,
		Enabled:          true,
		TrackChanges:     r.FormValue("track_changes") == "on",
		UpsideDown:       r.FormValue("upside_down") == "on",
		FailureThreshold: failThreshold,
		SuccessThreshold: successThreshold,
	}

	if v := r.FormValue("resend_interval"); v != "" {
		mon.ResendInterval, _ = strconv.Atoi(v)
	}

	if v := r.FormValue("sla_target"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 100 {
			mon.SLATarget = f
		}
	}

	if v := r.FormValue("anomaly_sensitivity"); v != "" {
		mon.AnomalySensitivity = v
	}

	if v := r.FormValue("group_id"); v != "" {
		gid, err := strconv.ParseInt(v, 10, 64)
		if err == nil && gid > 0 {
			mon.GroupID = &gid
		}
	}

	if v := r.FormValue("proxy_id"); v != "" {
		pid, err := strconv.ParseInt(v, 10, 64)
		if err == nil && pid > 0 {
			mon.ProxyID = &pid
		}
	}

	if v := r.FormValue("escalation_policy_id"); v != "" {
		epid, err := strconv.ParseInt(v, 10, 64)
		if err == nil && epid > 0 {
			mon.EscalationPolicyID = &epid
		}
	}

	mon.Settings = parseJSONOrForm(r, "settings", func(r *http.Request) json.RawMessage {
		return assembleSettings(r, mon.Type)
	})
	mon.Assertions = parseJSONOrForm(r, "assertions", func(r *http.Request) json.RawMessage {
		return assembleAssertions(r)
	})

	tagIDs := parseIDList(r.Form["tag_ids[]"])
	tagValues := r.Form["tag_values[]"]
	var monTags []storage.MonitorTag
	for i, tid := range tagIDs {
		val := ""
		if i < len(tagValues) {
			val = strings.TrimSpace(tagValues[i])
		}
		monTags = append(monTags, storage.MonitorTag{TagID: tid, Value: val})
	}

	return mon, parseIDList(r.Form["notification_channel_ids[]"]), monTags
}

func parseJSONOrForm(r *http.Request, prefix string, formFn func(*http.Request) json.RawMessage) json.RawMessage {
	if r.FormValue(prefix+"_mode") == "json" {
		if raw := strings.TrimSpace(r.FormValue(prefix + "_json")); raw != "" && json.Valid([]byte(raw)) {
			return json.RawMessage(raw)
		}
		return nil
	}
	return formFn(r)
}

func parseIDList(values []string) []int64 {
	var ids []int64
	for _, v := range values {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
