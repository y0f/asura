package storage

const schemaVersion = 41

// Database triggers also invalidate queued checks after trusted setters,
// assignment cascades and proxy edits. Advancing a revision itself cannot
// recurse; there is no wall-clock granularity or process-local counter.
const monitorRevisionTriggers = `
CREATE TRIGGER monitor_config_revision AFTER UPDATE ON monitors
WHEN NEW.config_revision = OLD.config_revision
BEGIN
 UPDATE monitors SET config_revision=config_revision+1 WHERE id=NEW.id;
END;
CREATE TRIGGER monitor_agent_revision_insert AFTER INSERT ON monitor_agents
BEGIN
 UPDATE monitors SET config_revision=config_revision+1 WHERE id=NEW.monitor_id;
END;
CREATE TRIGGER monitor_agent_revision_delete AFTER DELETE ON monitor_agents
BEGIN
 UPDATE monitors SET config_revision=config_revision+1 WHERE id=OLD.monitor_id;
END;
CREATE TRIGGER monitor_agent_revision_update AFTER UPDATE ON monitor_agents
BEGIN
 UPDATE monitors SET config_revision=config_revision+1 WHERE id IN (OLD.monitor_id, NEW.monitor_id);
END;
CREATE TRIGGER agent_enabled_revision AFTER UPDATE OF enabled ON agents
WHEN NEW.enabled != OLD.enabled
BEGIN
 UPDATE monitors SET config_revision=config_revision+1
 WHERE id IN (SELECT monitor_id FROM monitor_agents WHERE agent_id=NEW.id);
END;
CREATE TRIGGER proxy_config_revision AFTER UPDATE ON proxies
BEGIN
 UPDATE monitors SET config_revision=config_revision+1 WHERE proxy_id=NEW.id;
END;
`

const legacyAgentAssignmentsSchema = `CREATE TABLE IF NOT EXISTS legacy_agent_assignments (
	kind TEXT NOT NULL CHECK (kind IN ('monitor', 'agent')),
	id INTEGER NOT NULL,
	PRIMARY KEY (kind, id)
);`

const schema = legacyAgentAssignmentsSchema + `
CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT
);

CREATE TABLE IF NOT EXISTS monitor_groups (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT    NOT NULL,
	sort_order INTEGER NOT NULL DEFAULT 0,
	created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE IF NOT EXISTS escalation_policies (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT    NOT NULL,
	description TEXT    NOT NULL DEFAULT '',
	enabled     INTEGER NOT NULL DEFAULT 1,
	repeat      INTEGER NOT NULL DEFAULT 0,
	created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE IF NOT EXISTS escalation_policy_steps (
	id                       INTEGER PRIMARY KEY AUTOINCREMENT,
	policy_id                INTEGER NOT NULL REFERENCES escalation_policies(id) ON DELETE CASCADE,
	step_order               INTEGER NOT NULL,
	delay_minutes            INTEGER NOT NULL DEFAULT 0,
	notification_channel_ids TEXT    NOT NULL DEFAULT '[]',
	UNIQUE(policy_id, step_order)
);

CREATE INDEX IF NOT EXISTS idx_escalation_steps_policy ON escalation_policy_steps(policy_id, step_order);

CREATE TABLE IF NOT EXISTS monitors (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	name            TEXT    NOT NULL,
	description     TEXT    NOT NULL DEFAULT '',
	type            TEXT    NOT NULL,
	target          TEXT    NOT NULL,
	interval_secs   INTEGER NOT NULL DEFAULT 60,
	timeout_secs    INTEGER NOT NULL DEFAULT 10,
	enabled         INTEGER NOT NULL DEFAULT 1,
	tags            TEXT    NOT NULL DEFAULT '[]',
	settings        TEXT    NOT NULL DEFAULT '{}',
	assertions      TEXT    NOT NULL DEFAULT '[]',
	track_changes   INTEGER NOT NULL DEFAULT 0,
	failure_threshold INTEGER NOT NULL DEFAULT 3,
	success_threshold INTEGER NOT NULL DEFAULT 1,
	upside_down     INTEGER NOT NULL DEFAULT 0,
	agent_enabled   INTEGER NOT NULL DEFAULT 1,
	resend_interval INTEGER NOT NULL DEFAULT 0,
	sla_target           REAL    NOT NULL DEFAULT 0,
	anomaly_sensitivity  TEXT    NOT NULL DEFAULT 'off',
	group_id        INTEGER DEFAULT NULL,
	proxy_id              INTEGER DEFAULT NULL REFERENCES proxies(id) ON DELETE SET NULL,
	escalation_policy_id  INTEGER DEFAULT NULL REFERENCES escalation_policies(id) ON DELETE SET NULL,
	config_revision       INTEGER NOT NULL DEFAULT 1 CHECK (config_revision > 0),
	created_at            TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at            TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_monitors_group_id ON monitors(group_id);

CREATE TABLE IF NOT EXISTS monitor_notifications (
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	channel_id INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
	PRIMARY KEY (monitor_id, channel_id)
);

CREATE INDEX IF NOT EXISTS idx_monitor_notif_channel ON monitor_notifications(channel_id);

CREATE TABLE IF NOT EXISTS monitor_status (
	observation_kind TEXT NOT NULL DEFAULT 'unknown' CHECK (observation_kind IN ('unknown','probe','manual','heartbeat')),
	monitor_id             INTEGER PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
	config_revision        INTEGER NOT NULL DEFAULT 0,
	status                 TEXT    NOT NULL DEFAULT 'pending',
	last_check_at          TEXT,
	consec_fails           INTEGER NOT NULL DEFAULT 0,
	consec_successes       INTEGER NOT NULL DEFAULT 0,
	last_body_hash         TEXT    NOT NULL DEFAULT '',
	last_cert_fingerprint  TEXT    NOT NULL DEFAULT '',
	baseline_avg           REAL    NOT NULL DEFAULT 0,
	baseline_stddev        REAL    NOT NULL DEFAULT 0,
	baseline_updated_at    TEXT,
	last_response_time     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS check_results (
	observation_kind TEXT NOT NULL DEFAULT 'unknown' CHECK (observation_kind IN ('unknown','probe','manual','heartbeat')),
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	monitor_id       INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	status           TEXT    NOT NULL,
	response_time    INTEGER NOT NULL DEFAULT 0,
	status_code      INTEGER NOT NULL DEFAULT 0,
	message          TEXT    NOT NULL DEFAULT '',
	headers          TEXT    NOT NULL DEFAULT '',
	body             TEXT    NOT NULL DEFAULT '',
	body_hash        TEXT    NOT NULL DEFAULT '',
	cert_expiry      TEXT,
	cert_fingerprint TEXT    NOT NULL DEFAULT '',
	dns_records      TEXT    NOT NULL DEFAULT '',
	agent_id         INTEGER DEFAULT NULL,
	created_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_check_results_monitor_id ON check_results(monitor_id, created_at DESC);

CREATE TABLE IF NOT EXISTS incidents (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	monitor_id      INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	status          TEXT    NOT NULL DEFAULT 'open',
	cause           TEXT    NOT NULL DEFAULT '',
	severity        TEXT    NOT NULL DEFAULT 'critical',
	started_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	acknowledged_at TEXT,
	acknowledged_by TEXT    NOT NULL DEFAULT '',
	resolved_at     TEXT,
	resolved_by     TEXT    NOT NULL DEFAULT '',
	announcement_pending INTEGER NOT NULL DEFAULT 0,
	escalation_pending INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_incidents_monitor_id ON incidents(monitor_id, status);
CREATE INDEX IF NOT EXISTS idx_incidents_pending ON incidents(id)
WHERE status IN ('open','acknowledged') AND (announcement_pending=1 OR escalation_pending=1);

CREATE TABLE IF NOT EXISTS incident_events (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
	type        TEXT    NOT NULL,
	message     TEXT    NOT NULL DEFAULT '',
	created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_incident_events_incident_id ON incident_events(incident_id);

CREATE TABLE IF NOT EXISTS escalation_states (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	incident_id  INTEGER NOT NULL UNIQUE REFERENCES incidents(id) ON DELETE CASCADE,
	policy_id    INTEGER NOT NULL REFERENCES escalation_policies(id) ON DELETE CASCADE,
	current_step INTEGER NOT NULL DEFAULT 0,
	next_fire_at TEXT    NOT NULL,
	started_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_escalation_states_fire ON escalation_states(next_fire_at);

CREATE TABLE IF NOT EXISTS notification_channels (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT    NOT NULL,
	type       TEXT    NOT NULL,
	enabled    INTEGER NOT NULL DEFAULT 1,
	settings   TEXT    NOT NULL DEFAULT '{}',
	events     TEXT    NOT NULL DEFAULT '[]',
	created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE IF NOT EXISTS maintenance_windows (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT    NOT NULL,
	monitor_ids TEXT    NOT NULL DEFAULT '[]',
	start_time  TEXT    NOT NULL,
	end_time    TEXT    NOT NULL,
	recurring   TEXT    NOT NULL DEFAULT '',
	cron_expr   TEXT    NOT NULL DEFAULT '',
	active      INTEGER NOT NULL DEFAULT 0,
	created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE IF NOT EXISTS content_changes (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	old_hash   TEXT    NOT NULL DEFAULT '',
	new_hash   TEXT    NOT NULL,
	diff       TEXT    NOT NULL DEFAULT '',
	old_body   TEXT    NOT NULL DEFAULT '',
	new_body   TEXT    NOT NULL DEFAULT '',
	created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_content_changes_monitor_id ON content_changes(monitor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_content_changes_created_at ON content_changes(created_at);

CREATE TABLE IF NOT EXISTS audit_log (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	action       TEXT    NOT NULL,
	entity       TEXT    NOT NULL,
	entity_id    INTEGER NOT NULL DEFAULT 0,
	api_key_name TEXT    NOT NULL DEFAULT '',
	detail       TEXT    NOT NULL DEFAULT '',
	created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE IF NOT EXISTS heartbeats (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	monitor_id  INTEGER NOT NULL UNIQUE REFERENCES monitors(id) ON DELETE CASCADE,
	token       TEXT    NOT NULL UNIQUE,
	grace       INTEGER NOT NULL DEFAULT 0,
	last_ping_at TEXT,
	status      TEXT    NOT NULL DEFAULT 'pending'
);

CREATE TABLE IF NOT EXISTS sessions (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	token_hash   TEXT    NOT NULL UNIQUE,
	api_key_name TEXT    NOT NULL,
	key_hash     TEXT    NOT NULL DEFAULT '',
	ip_address   TEXT    NOT NULL DEFAULT '',
	created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	expires_at   TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS request_logs (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	method         TEXT    NOT NULL,
	path           TEXT    NOT NULL,
	status_code    INTEGER NOT NULL,
	latency_ms     INTEGER NOT NULL,
	client_ip      TEXT    NOT NULL,
	user_agent     TEXT    NOT NULL DEFAULT '',
	referer        TEXT    NOT NULL DEFAULT '',
	monitor_id     INTEGER DEFAULT NULL,
	route_group    TEXT    NOT NULL DEFAULT '',
	created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_request_logs_created ON request_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_request_logs_monitor ON request_logs(monitor_id, created_at) WHERE monitor_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_request_logs_group   ON request_logs(route_group, created_at);

CREATE TABLE IF NOT EXISTS request_log_rollups (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	date            TEXT    NOT NULL,
	route_group     TEXT    NOT NULL DEFAULT '',
	monitor_id      INTEGER DEFAULT NULL,
	requests        INTEGER NOT NULL DEFAULT 0,
	unique_visitors INTEGER NOT NULL DEFAULT 0,
	avg_latency_ms  INTEGER NOT NULL DEFAULT 0,
	UNIQUE(date, route_group, monitor_id)
);

CREATE TABLE IF NOT EXISTS status_pages (
	id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	slug               TEXT    NOT NULL UNIQUE,
	title              TEXT    NOT NULL DEFAULT 'Service Status',
	description        TEXT    NOT NULL DEFAULT '',
	custom_css         TEXT    NOT NULL DEFAULT '',
	show_incidents     INTEGER NOT NULL DEFAULT 1,
	enabled            INTEGER NOT NULL DEFAULT 0,
	api_enabled        INTEGER NOT NULL DEFAULT 0,
	sort_order         INTEGER NOT NULL DEFAULT 0,
	logo_url           TEXT    NOT NULL DEFAULT '',
	favicon_url        TEXT    NOT NULL DEFAULT '',
	custom_header_html TEXT    NOT NULL DEFAULT '',
	password_hash      TEXT    NOT NULL DEFAULT '',
	analytics_script   TEXT    NOT NULL DEFAULT '',
	created_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE IF NOT EXISTS status_page_monitors (
	page_id    INTEGER NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	sort_order INTEGER NOT NULL DEFAULT 0,
	group_name TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (page_id, monitor_id)
);

CREATE TABLE IF NOT EXISTS status_page_subscribers (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	status_page_id INTEGER NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
	type           TEXT    NOT NULL,
	email          TEXT    NOT NULL DEFAULT '',
	webhook_url    TEXT    NOT NULL DEFAULT '',
	confirmed      INTEGER NOT NULL DEFAULT 0,
	token          TEXT    NOT NULL UNIQUE,
	created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_sp_subscribers_page ON status_page_subscribers(status_page_id, confirmed);
CREATE INDEX IF NOT EXISTS idx_sp_subscribers_token ON status_page_subscribers(token);

CREATE TABLE IF NOT EXISTS check_result_hourly (
	observation_kind TEXT NOT NULL DEFAULT 'unknown' CHECK (observation_kind IN ('unknown','probe','manual','heartbeat')),
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	hour       TEXT    NOT NULL,
	sum_rt     INTEGER,
	avg_rt     INTEGER NOT NULL DEFAULT 0,
	min_rt     INTEGER NOT NULL DEFAULT 0,
	max_rt     INTEGER NOT NULL DEFAULT 0,
	p95_rt     INTEGER NOT NULL DEFAULT 0,
	up_count   INTEGER NOT NULL DEFAULT 0,
	down_count INTEGER NOT NULL DEFAULT 0,
	total      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (monitor_id, hour, observation_kind)
);

CREATE TABLE IF NOT EXISTS check_result_daily (
	observation_kind TEXT NOT NULL DEFAULT 'unknown' CHECK (observation_kind IN ('unknown','probe','manual','heartbeat')),
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	day        TEXT    NOT NULL,
	sum_rt     INTEGER,
	avg_rt     INTEGER NOT NULL DEFAULT 0,
	min_rt     INTEGER NOT NULL DEFAULT 0,
	max_rt     INTEGER NOT NULL DEFAULT 0,
	p95_rt     INTEGER NOT NULL DEFAULT 0,
	up_count   INTEGER NOT NULL DEFAULT 0,
	down_count INTEGER NOT NULL DEFAULT 0,
	total      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (monitor_id, day, observation_kind)
);

CREATE TABLE IF NOT EXISTS agents (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	name           TEXT    NOT NULL,
	location       TEXT    NOT NULL DEFAULT '',
	token          TEXT    NOT NULL UNIQUE,
	last_heartbeat TEXT,
	enabled        INTEGER NOT NULL DEFAULT 1,
	created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_agents_token ON agents(token);

-- Agents a monitor is assigned to. A monitor without rows runs on every agent.
CREATE TABLE IF NOT EXISTS monitor_agents (
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	agent_id   INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	PRIMARY KEY (monitor_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_monitor_agents_agent_id ON monitor_agents(agent_id);

CREATE TABLE IF NOT EXISTS on_call_rotations (
	id                  INTEGER PRIMARY KEY AUTOINCREMENT,
	name                TEXT    NOT NULL,
	channel_ids         TEXT    NOT NULL DEFAULT '[]',
	period              TEXT    NOT NULL DEFAULT 'daily',
	current_index       INTEGER NOT NULL DEFAULT 0,
	period_started_at   TEXT NOT NULL DEFAULT '',
	override_channel_id INTEGER DEFAULT NULL,
	override_until      TEXT,
	created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_check_results_created_at ON check_results(created_at);
CREATE INDEX IF NOT EXISTS idx_incidents_resolved_at ON incidents(status, resolved_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_request_logs_client_ip ON request_logs(client_ip, created_at);
CREATE INDEX IF NOT EXISTS idx_check_results_monitor_latest ON check_results(monitor_id, id DESC);

CREATE TABLE IF NOT EXISTS totp_keys (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	api_key_name TEXT NOT NULL UNIQUE,
	secret       TEXT NOT NULL,
	last_counter INTEGER NOT NULL DEFAULT 0,
	created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE IF NOT EXISTS proxies (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT    NOT NULL,
	protocol   TEXT    NOT NULL DEFAULT 'http',
	host       TEXT    NOT NULL,
	port       INTEGER NOT NULL,
	auth_user  TEXT    NOT NULL DEFAULT '',
	auth_pass  TEXT    NOT NULL DEFAULT '',
	enabled    INTEGER NOT NULL DEFAULT 1,
	created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE IF NOT EXISTS tags (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT    NOT NULL UNIQUE,
	color      TEXT    NOT NULL DEFAULT '#808080',
	created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE TABLE IF NOT EXISTS monitor_tags (
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	tag_id     INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	value      TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (monitor_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_monitor_tags_tag ON monitor_tags(tag_id);

CREATE TABLE IF NOT EXISTS notification_history (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	channel_id  INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
	monitor_id  INTEGER,
	incident_id INTEGER,
	event_type  TEXT    NOT NULL DEFAULT '',
	status      TEXT    NOT NULL DEFAULT 'sent',
	error       TEXT    NOT NULL DEFAULT '',
	sent_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_notif_history_channel ON notification_history(channel_id, sent_at DESC);
CREATE INDEX IF NOT EXISTS idx_notif_history_sent_at ON notification_history(sent_at DESC);
` + monitorRevisionTriggers

// migrations holds incremental schema changes after the baseline.
// Consolidated at v1.0.0: all pre-v15 migrations are folded into the
// baseline schema above. Databases created before v1.0.0 must be
// upgraded through v1.0.0 first. Append new migrations here for v16+.
var migrations = []struct {
	version int
	sql     string
}{
	{
		version: 16,
		sql: `CREATE TABLE IF NOT EXISTS totp_keys (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	api_key_name TEXT NOT NULL UNIQUE,
	secret       TEXT NOT NULL,
	created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);`,
	},
	{
		version: 17,
		sql: `CREATE TABLE IF NOT EXISTS proxies (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT    NOT NULL,
	protocol   TEXT    NOT NULL DEFAULT 'http',
	host       TEXT    NOT NULL,
	port       INTEGER NOT NULL,
	auth_user  TEXT    NOT NULL DEFAULT '',
	auth_pass  TEXT    NOT NULL DEFAULT '',
	enabled    INTEGER NOT NULL DEFAULT 1,
	created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
ALTER TABLE monitors ADD COLUMN proxy_id INTEGER DEFAULT NULL REFERENCES proxies(id) ON DELETE SET NULL;`,
	},
	{
		version: 18,
		sql: `CREATE TABLE IF NOT EXISTS tags (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT    NOT NULL UNIQUE,
	color      TEXT    NOT NULL DEFAULT '#808080',
	created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE TABLE IF NOT EXISTS monitor_tags (
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	tag_id     INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
	value      TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (monitor_id, tag_id)
);
CREATE INDEX IF NOT EXISTS idx_monitor_tags_tag ON monitor_tags(tag_id);
INSERT OR IGNORE INTO tags (name) SELECT DISTINCT j.value FROM monitors, json_each(monitors.tags) AS j WHERE monitors.tags != '[]' AND monitors.tags != '';
INSERT OR IGNORE INTO monitor_tags (monitor_id, tag_id) SELECT m.id, t.id FROM monitors m, json_each(m.tags) AS j JOIN tags t ON t.name = j.value WHERE m.tags != '[]' AND m.tags != '';
UPDATE monitors SET tags = '[]';`,
	},
	{
		version: 19,
		sql: `ALTER TABLE check_results ADD COLUMN cert_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE monitor_status ADD COLUMN last_cert_fingerprint TEXT NOT NULL DEFAULT '';`,
	},
	{
		version: 20,
		sql: `CREATE TABLE IF NOT EXISTS notification_history (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	channel_id INTEGER NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
	monitor_id INTEGER,
	incident_id INTEGER,
	event_type TEXT    NOT NULL DEFAULT '',
	status     TEXT    NOT NULL DEFAULT 'sent',
	error      TEXT    NOT NULL DEFAULT '',
	sent_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_notif_history_channel ON notification_history(channel_id, sent_at DESC);
CREATE INDEX IF NOT EXISTS idx_notif_history_sent_at ON notification_history(sent_at DESC);`,
	},
	{
		version: 21,
		sql: `UPDATE monitors
SET assertions = json_object(
	'operator', 'and',
	'groups', json_array(
		json_object('operator', 'and', 'conditions', json(assertions))
	)
)
WHERE assertions IS NOT NULL
  AND assertions != ''
  AND assertions != '[]'
  AND json_type(assertions) = 'array';`,
	},
	{
		version: 22,
		sql: `ALTER TABLE status_pages ADD COLUMN logo_url TEXT NOT NULL DEFAULT '';
ALTER TABLE status_pages ADD COLUMN favicon_url TEXT NOT NULL DEFAULT '';
ALTER TABLE status_pages ADD COLUMN custom_header_html TEXT NOT NULL DEFAULT '';
ALTER TABLE status_pages ADD COLUMN password_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE status_pages ADD COLUMN analytics_script TEXT NOT NULL DEFAULT '';`,
	},
	{
		version: 23,
		sql: `CREATE TABLE IF NOT EXISTS escalation_policies (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT    NOT NULL,
	description TEXT    NOT NULL DEFAULT '',
	enabled     INTEGER NOT NULL DEFAULT 1,
	repeat      INTEGER NOT NULL DEFAULT 0,
	created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE TABLE IF NOT EXISTS escalation_policy_steps (
	id                       INTEGER PRIMARY KEY AUTOINCREMENT,
	policy_id                INTEGER NOT NULL REFERENCES escalation_policies(id) ON DELETE CASCADE,
	step_order               INTEGER NOT NULL,
	delay_minutes            INTEGER NOT NULL DEFAULT 0,
	notification_channel_ids TEXT    NOT NULL DEFAULT '[]',
	UNIQUE(policy_id, step_order)
);
CREATE INDEX IF NOT EXISTS idx_escalation_steps_policy ON escalation_policy_steps(policy_id, step_order);
CREATE TABLE IF NOT EXISTS escalation_states (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	incident_id  INTEGER NOT NULL UNIQUE REFERENCES incidents(id) ON DELETE CASCADE,
	policy_id    INTEGER NOT NULL REFERENCES escalation_policies(id) ON DELETE CASCADE,
	current_step INTEGER NOT NULL DEFAULT 0,
	next_fire_at TEXT    NOT NULL,
	started_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_escalation_states_fire ON escalation_states(next_fire_at);
ALTER TABLE monitors ADD COLUMN escalation_policy_id INTEGER DEFAULT NULL REFERENCES escalation_policies(id) ON DELETE SET NULL;`,
	},
	{
		version: 24,
		sql:     `ALTER TABLE monitors ADD COLUMN sla_target REAL NOT NULL DEFAULT 0;`,
	},
	{
		version: 25,
		sql: `CREATE TABLE IF NOT EXISTS status_page_subscribers (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	status_page_id INTEGER NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
	type           TEXT    NOT NULL,
	email          TEXT    NOT NULL DEFAULT '',
	webhook_url    TEXT    NOT NULL DEFAULT '',
	confirmed      INTEGER NOT NULL DEFAULT 0,
	token          TEXT    NOT NULL UNIQUE,
	created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_sp_subscribers_page ON status_page_subscribers(status_page_id, confirmed);
CREATE INDEX IF NOT EXISTS idx_sp_subscribers_token ON status_page_subscribers(token);`,
	},
	{
		version: 26,
		sql: `ALTER TABLE monitors ADD COLUMN anomaly_sensitivity TEXT NOT NULL DEFAULT 'off';
ALTER TABLE monitor_status ADD COLUMN baseline_avg REAL NOT NULL DEFAULT 0;
ALTER TABLE monitor_status ADD COLUMN baseline_stddev REAL NOT NULL DEFAULT 0;
ALTER TABLE monitor_status ADD COLUMN baseline_updated_at TEXT;`,
	},
	{
		version: 27,
		sql: `ALTER TABLE maintenance_windows ADD COLUMN cron_expr TEXT NOT NULL DEFAULT '';
ALTER TABLE maintenance_windows ADD COLUMN active INTEGER NOT NULL DEFAULT 0;`,
	},
	{
		version: 28,
		sql:     `ALTER TABLE incidents ADD COLUMN severity TEXT NOT NULL DEFAULT 'critical';`,
	},
	{
		version: 29,
		sql: `CREATE TABLE IF NOT EXISTS check_result_hourly (
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	hour       TEXT    NOT NULL,
	avg_rt     INTEGER NOT NULL DEFAULT 0,
	min_rt     INTEGER NOT NULL DEFAULT 0,
	max_rt     INTEGER NOT NULL DEFAULT 0,
	p95_rt     INTEGER NOT NULL DEFAULT 0,
	up_count   INTEGER NOT NULL DEFAULT 0,
	down_count INTEGER NOT NULL DEFAULT 0,
	total      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (monitor_id, hour)
);
CREATE TABLE IF NOT EXISTS check_result_daily (
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	day        TEXT    NOT NULL,
	avg_rt     INTEGER NOT NULL DEFAULT 0,
	min_rt     INTEGER NOT NULL DEFAULT 0,
	max_rt     INTEGER NOT NULL DEFAULT 0,
	p95_rt     INTEGER NOT NULL DEFAULT 0,
	up_count   INTEGER NOT NULL DEFAULT 0,
	down_count INTEGER NOT NULL DEFAULT 0,
	total      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (monitor_id, day)
);`,
	},
	{
		version: 30,
		sql: `CREATE TABLE IF NOT EXISTS agents (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	name           TEXT    NOT NULL,
	location       TEXT    NOT NULL DEFAULT '',
	token          TEXT    NOT NULL UNIQUE,
	last_heartbeat TEXT,
	enabled        INTEGER NOT NULL DEFAULT 1,
	created_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_agents_token ON agents(token);
ALTER TABLE check_results ADD COLUMN agent_id INTEGER DEFAULT NULL;`,
	},
	{
		version: 31,
		sql: `CREATE TABLE IF NOT EXISTS on_call_rotations (
	id                  INTEGER PRIMARY KEY AUTOINCREMENT,
	name                TEXT    NOT NULL,
	channel_ids         TEXT    NOT NULL DEFAULT '[]',
	period              TEXT    NOT NULL DEFAULT 'daily',
	current_index       INTEGER NOT NULL DEFAULT 0,
	override_channel_id INTEGER DEFAULT NULL,
	override_until      TEXT,
	created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
	updated_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);`,
	},
	{
		version: 32,
		sql:     `ALTER TABLE monitors ADD COLUMN agent_enabled INTEGER NOT NULL DEFAULT 1;`,
	},
	{
		version: 33,
		sql: `CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT
);
ALTER TABLE monitor_status ADD COLUMN last_response_time INTEGER NOT NULL DEFAULT 0;
ALTER TABLE totp_keys ADD COLUMN last_counter INTEGER NOT NULL DEFAULT 0;`,
	},
	{
		version: 34,
		sql:     `CREATE INDEX IF NOT EXISTS idx_content_changes_created_at ON content_changes(created_at);`,
	},
	{
		version: 35,
		sql: `CREATE TABLE IF NOT EXISTS monitor_agents (
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	agent_id   INTEGER NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	PRIMARY KEY (monitor_id, agent_id)
);

CREATE INDEX IF NOT EXISTS idx_monitor_agents_agent_id ON monitor_agents(agent_id);`,
	},
	{
		version: 36,
		sql: `ALTER TABLE check_result_hourly ADD COLUMN sum_rt INTEGER;
ALTER TABLE check_result_daily ADD COLUMN sum_rt INTEGER;`,
	},
	{
		version: 37,
		sql: `ALTER TABLE incidents ADD COLUMN announcement_pending INTEGER NOT NULL DEFAULT 0;
ALTER TABLE incidents ADD COLUMN escalation_pending INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_incidents_pending ON incidents(id)
WHERE status IN ('open','acknowledged') AND (announcement_pending=1 OR escalation_pending=1);`,
	},
	{
		version: 38,
		sql: `ALTER TABLE on_call_rotations ADD COLUMN period_started_at TEXT NOT NULL DEFAULT '';
UPDATE on_call_rotations SET period_started_at=updated_at;`,
	},
	{
		version: 39,
		sql: legacyAgentAssignmentsSchema + `
INSERT INTO legacy_agent_assignments (kind, id)
SELECT 'monitor', m.id FROM monitors m
WHERE m.agent_enabled=1 AND m.type NOT IN ('heartbeat','manual','docker','command')
AND NOT EXISTS (SELECT 1 FROM monitor_agents ma WHERE ma.monitor_id=m.id);
INSERT INTO legacy_agent_assignments (kind, id)
SELECT 'agent', id FROM agents WHERE enabled=1;`,
	},
	{
		version: 40,
		sql: `ALTER TABLE monitors ADD COLUMN config_revision INTEGER NOT NULL DEFAULT 1 CHECK (config_revision > 0);
ALTER TABLE monitor_status ADD COLUMN config_revision INTEGER NOT NULL DEFAULT 0;
UPDATE monitor_status SET config_revision=(SELECT config_revision FROM monitors WHERE monitors.id=monitor_status.monitor_id);` + monitorRevisionTriggers,
	},
	{
		version: 41,
		sql: `ALTER TABLE check_results ADD COLUMN observation_kind TEXT NOT NULL DEFAULT 'unknown' CHECK (observation_kind IN ('unknown','probe','manual','heartbeat'));
ALTER TABLE monitor_status ADD COLUMN observation_kind TEXT NOT NULL DEFAULT 'unknown' CHECK (observation_kind IN ('unknown','probe','manual','heartbeat'));
UPDATE monitor_status SET baseline_avg=0, baseline_stddev=0, baseline_updated_at=NULL;
CREATE TABLE check_result_hourly_v41 (
	observation_kind TEXT NOT NULL DEFAULT 'unknown' CHECK (observation_kind IN ('unknown','probe','manual','heartbeat')),
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	hour       TEXT    NOT NULL,
	sum_rt     INTEGER,
	avg_rt     INTEGER NOT NULL DEFAULT 0,
	min_rt     INTEGER NOT NULL DEFAULT 0,
	max_rt     INTEGER NOT NULL DEFAULT 0,
	p95_rt     INTEGER NOT NULL DEFAULT 0,
	up_count   INTEGER NOT NULL DEFAULT 0,
	down_count INTEGER NOT NULL DEFAULT 0,
	total      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (monitor_id, hour, observation_kind)
);
INSERT INTO check_result_hourly_v41 (monitor_id, hour, sum_rt, avg_rt, min_rt, max_rt, p95_rt, up_count, down_count, total) SELECT monitor_id, hour, sum_rt, avg_rt, min_rt, max_rt, p95_rt, up_count, down_count, total FROM check_result_hourly;
DROP TABLE check_result_hourly;
ALTER TABLE check_result_hourly_v41 RENAME TO check_result_hourly;
CREATE TABLE check_result_daily_v41 (
	observation_kind TEXT NOT NULL DEFAULT 'unknown' CHECK (observation_kind IN ('unknown','probe','manual','heartbeat')),
	monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
	day        TEXT    NOT NULL,
	sum_rt     INTEGER,
	avg_rt     INTEGER NOT NULL DEFAULT 0,
	min_rt     INTEGER NOT NULL DEFAULT 0,
	max_rt     INTEGER NOT NULL DEFAULT 0,
	p95_rt     INTEGER NOT NULL DEFAULT 0,
	up_count   INTEGER NOT NULL DEFAULT 0,
	down_count INTEGER NOT NULL DEFAULT 0,
	total      INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (monitor_id, day, observation_kind)
);
INSERT INTO check_result_daily_v41 (monitor_id, day, sum_rt, avg_rt, min_rt, max_rt, p95_rt, up_count, down_count, total) SELECT monitor_id, day, sum_rt, avg_rt, min_rt, max_rt, p95_rt, up_count, down_count, total FROM check_result_daily;
DROP TABLE check_result_daily;
ALTER TABLE check_result_daily_v41 RENAME TO check_result_daily;`,
	},
}
