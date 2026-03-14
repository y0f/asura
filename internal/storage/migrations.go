package storage

const schemaVersion = 28

const schema = `
CREATE TABLE IF NOT EXISTS schema_version (
	version INTEGER NOT NULL
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
	resend_interval INTEGER NOT NULL DEFAULT 0,
	sla_target           REAL    NOT NULL DEFAULT 0,
	anomaly_sensitivity  TEXT    NOT NULL DEFAULT 'off',
	group_id        INTEGER DEFAULT NULL,
	proxy_id              INTEGER DEFAULT NULL REFERENCES proxies(id) ON DELETE SET NULL,
	escalation_policy_id  INTEGER DEFAULT NULL REFERENCES escalation_policies(id) ON DELETE SET NULL,
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
	monitor_id             INTEGER PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
	status                 TEXT    NOT NULL DEFAULT 'pending',
	last_check_at          TEXT,
	consec_fails           INTEGER NOT NULL DEFAULT 0,
	consec_successes       INTEGER NOT NULL DEFAULT 0,
	last_body_hash         TEXT    NOT NULL DEFAULT '',
	last_cert_fingerprint  TEXT    NOT NULL DEFAULT '',
	baseline_avg           REAL    NOT NULL DEFAULT 0,
	baseline_stddev        REAL    NOT NULL DEFAULT 0,
	baseline_updated_at    TEXT
);

CREATE TABLE IF NOT EXISTS check_results (
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
	resolved_by     TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_incidents_monitor_id ON incidents(monitor_id, status);

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

CREATE INDEX IF NOT EXISTS idx_check_results_created_at ON check_results(created_at);
CREATE INDEX IF NOT EXISTS idx_incidents_resolved_at ON incidents(status, resolved_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_created_at ON audit_log(created_at);
CREATE INDEX IF NOT EXISTS idx_request_logs_client_ip ON request_logs(client_ip, created_at);
CREATE INDEX IF NOT EXISTS idx_check_results_monitor_latest ON check_results(monitor_id, id DESC);

CREATE TABLE IF NOT EXISTS totp_keys (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	api_key_name TEXT NOT NULL UNIQUE,
	secret       TEXT NOT NULL,
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
`

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
}
