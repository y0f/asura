---
title: "API reference"
nav_title: "API reference"
description: "Automate Asura through its REST API. Reference authentication, permissions, monitor management, incidents, status pages and reporting endpoints."
permalink: "/api.html"
---

# API Reference {#api-reference}

REST resource endpoints return JSON; badges return SVG and the live event endpoint uses server-sent events. Pass your key in the `X-API-Key` header. If you set a `base_path`, all paths get prefixed (e.g. `/asura/api/v1/health`). See [Access control](access-control.html) for role bindings, object scopes and privileged operations.

Pagination: `?page=1&per_page=20` applies to the paginated list endpoints only: monitors, checks, changes, incidents, notification history, request-logs, and audit. Other list endpoints return the full set as `{"data":[...]}`.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Permissions {#permissions}

Each endpoint requires its resource and action scope. List and detail endpoints use `resource.read`; creates, updates, and deletes use `resource.create`, `resource.update`, and `resource.delete`. `resource.write` grants all mutations for that resource, without read access. Actions such as `monitors.pause`, `incidents.acknowledge`, and `notifications.test` can be granted separately. See [Configuration](configuration.html) for the full scope registry and v1 migration mappings.

Exceptions: monitor SLA endpoints use `sla.read`; notification delivery history uses `notification_history.read`; request logs use `request_logs.read`; on-call rotations use `on_call`; status pages use `status_pages`; escalation policies use `escalation_policies`. Cloning requires source read and clone authority, destination clone authority, and permission to assign any copied relationships. Bulk monitor operations require the corresponding action on every selected monitor; group moves require `monitors.update` and `monitors.assign` for the source and destination, plus `groups.read`. `/metrics` uses the global `metrics.read` permission.

### Stored credentials {#stored-credentials}

Monitor and notification responses hide stored authentication secrets and custom request-header values. Generic webhook URLs are private because their path or query can contain a token; subscriber listings also omit webhook URLs. Keys with mutation permissions but no matching read permission receive only the affected identifier for monitor and channel mutations. A newly created heartbeat monitor returns its new ping token once; later monitor reads do not return the token or its stored hash.

When updating the same monitor or channel type, leave a redacted secret field blank to retain its stored value. To clear a secret explicitly, include a top-level `clear_secrets` array, for example `["bearer_token", "headers.X-Token"]`. A blank header value keeps the stored value for that header name; deleting the header entry removes it. Multi-step checks retain blank header values only when the step name and URL identify one stored step, including after reordering. Ambiguous duplicate steps require replacement values or explicit clearing. Ordinary updates that change the target, proxy assignment, or OAuth token URL must replace or clear the credentials that would otherwise be retained. Super administrators can retain them across a destination change because that role already permits full credential backups. Configuration export remains a separate super administrator operation.

Notification credentials are also bound to their delivery destination: server, account and recipients. An ordinary editor changing those values must replace or explicitly clear retained secrets; cosmetic settings such as priority and sound do not change the destination. Super administrators have the same deliberate retargeting exception. Omitted channel fields are preserved against the current stored row.

## Health (no auth) {#health-no-auth}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/health` | Status, uptime |

## Heartbeat Ping (no auth) {#heartbeat-ping-no-auth}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` / `POST` | `/api/v1/heartbeat/{token}` | Record a ping for a heartbeat monitor |

Heartbeat (push) monitors expect periodic pings from your job or service. Hit this endpoint each time the job runs. The token is generated when the heartbeat monitor is created. Returns `{"status":"ok"}`; an unknown token returns `404`. No authentication is required so it can be called from anywhere (e.g. a cron job's curl).

## Metrics (read auth) {#metrics-read-auth}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/metrics` | Prometheus exposition format |

`metrics.read` grants a global operational export containing monitor names, types and tags, plus global incident and request aggregates. Treat it as sensitive operational data. It does not expose credentials, targets or settings, and does not grant ordinary monitor API reads. It cannot be restricted with monitor or group selectors.

## Monitors {#monitors}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/monitors` | List |
| `POST` | `/api/v1/monitors` | Create |
| `GET` | `/api/v1/monitors/{id}` | Get |
| `PUT` | `/api/v1/monitors/{id}` | Update |
| `DELETE` | `/api/v1/monitors/{id}` | Delete |
| `POST` | `/api/v1/monitors/{id}/pause` | Pause |
| `POST` | `/api/v1/monitors/{id}/resume` | Resume |
| `POST` | `/api/v1/monitors/{id}/clone` | Clone |
| `POST` | `/api/v1/monitors/{id}/status` | Set manual status (up/down/degraded) |
| `POST` | `/api/v1/monitors/bulk` | Bulk pause/resume/delete/set\_group |
| `GET` | `/api/v1/monitors/{id}/checks` | Check history |
| `GET` | `/api/v1/monitors/{id}/metrics` | Analytics |
| `GET` | `/api/v1/monitors/{id}/changes` | Content changes |
| `GET` | `/api/v1/monitors/{id}/chart` | Response time chart data |

See [Monitors](monitors.html) for the full field reference and protocol settings.

Omitted relationship fields on `PUT` preserve the current assignments inside the update transaction. This includes `agent_ids`, `monitor_tags`, `notification_channel_ids`, `group_id`, `proxy_id` and `escalation_policy_id`. An explicit empty array clears a list assignment; null clears an optional single relationship. Changing an assignment requires `monitors.assign` and the related resource's read permission. Empty agent assignments mean no remote jobs. Initial group placement is authorized by destination `monitors.create`; later group moves also require source and destination update/assignment authority and `groups.read`.

### Bulk Operations {#bulk-operations}

```
POST /api/v1/monitors/bulk
{
  "action": "pause",   // pause | resume | delete | set_group
  "ids": [1, 2, 3],
  "group_id": 5        // only for set_group (null to unset)
}
```

Returns `{"status": "pause", "affected": 3}`. Max 500 monitors per request.

### Clone {#clone}

`POST /api/v1/monitors/{id}/clone` creates a duplicate with `" (copy)"` appended to its name. The clone starts paused (`enabled: false`). Copied relationships require the same assignment authority as new assignments. The response includes only the identifier when the caller cannot read the new monitor.

### Set Manual Status {#set-manual-status}

```
POST /api/v1/monitors/{id}/status
{
  "status": "down",
  "message": "Vendor reported outage"
}
```

Only works for monitors with `type: "manual"` and requires `monitors.status` on that monitor. Valid statuses are `up`, `down` and `degraded`. The operation records a check and changes state immediately. Down or degraded opens or updates an incident; up resolves it. Maintenance can defer incident notifications.

## Status Pages {#status-pages}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/status-pages` | List all status pages |
| `GET` | `/api/v1/status-pages/{id}` | Get status page with assigned monitors |
| `POST` | `/api/v1/status-pages` | Create a new status page |
| `PUT` | `/api/v1/status-pages/{id}` | Update a status page |
| `DELETE` | `/api/v1/status-pages/{id}` | Delete a status page |

Each status page has its own `slug` (URL path), `title`, `description`, and monitor set. Monitors are assigned per-page with optional `sort_order` and `group_name` for display grouping. The page is served at `/{slug}`.

Membership changes and exposure expansion require `monitors.read` on all affected members, plus independent `incidents.read` when incident publication is enabled. Metadata, protection and membership are saved atomically. Omitted memberships and secrets are preserved. Analytics changes require `super_admin`; ordinary page mutation without `status_pages.read` returns only the identifier. See [Status pages](status-pages.html) for publication and branding rules.

The public API for a specific page is available at `GET /api/v1/status-pages/{id}/public` (no API key required). Returns public fields only. It is served when the page has `enabled` or `api_enabled` on and returns 404 only when both are off. A password-protected page also needs the page password in the `X-Status-Page-Password` header (attempts are rate-limited like logins).

An unsampled member has `uptime_90d: null` and `total_checks: 0`. Empty pages and pending data produce `overall_status: "unknown"`; known degraded or down states take precedence. Storage failures return an error rather than fabricated healthy or empty data.

### Subscribers {#subscribers}

| Method | Endpoint | Auth | Description |
| --- | --- | --- | --- |
| `POST` | `/api/v1/status-pages/{id}/subscribe` | public | Subscribe to incident updates |
| `GET` | `/api/v1/status-pages/{id}/subscribers` | `subscribers.read` | Subscriber count and confirmed contacts |
| `DELETE` | `/api/v1/status-pages/{id}/subscribers/{subId}` | `subscribers.delete` | Remove a subscriber |

```
POST /api/v1/status-pages/{id}/subscribe
{
  "type": "email",                       // email | webhook
  "email": "alerts@example.com",         // required when type is email
  "webhook_url": "https://example.com/hook" // required when type is webhook
}
```

Subscribe is public so status page visitors can opt in. It needs a page with both `enabled` and `api_enabled` on (404 otherwise). Email subscriptions queue a confirmation when subscriptions and mail are available; unavailable mail or failed queue admission returns 503 so the visitor can retry. Webhook subscriptions are confirmed immediately. The subscribers endpoint requires `subscribers.read` and returns `{"count":N,"data":[...]}` with confirmed contacts. The legacy `readonly` role does not include this scope.

## Status Badges {#status-badges}

Available for monitors assigned to an enabled, unprotected status page. A password-protected or API-only page does not authorize public badges.

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/badge/{id}/status` | Current status, including pending or paused |
| `GET` | `/api/v1/badge/{id}/uptime` | 30-day uptime percentage |
| `GET` | `/api/v1/badge/{id}/response` | Median (p50) response time of successful checks over 24h |
| `GET` | `/api/v1/badge/{id}/cert` | TLS certificate expiry (days remaining) |

All badges accept `?label=` to override the left-hand text and use a neutral/blue palette with explicit values. Uptime without observations reads `no checks`; response time without successful samples reads `no samples`, while a measured zero remains `0ms`. Manual and heartbeat response badges read `n/a`. A missing certificate expiry reads `n/a`; an expired timestamp reads `expired`, and a future expiry under one day reads `<1d`.

Successful badges may be cached for five minutes. Invalid IDs return 400, missing or unpublished monitors return 404, and storage failures return 500, with an SVG error label and `Cache-Control: no-store`. Publication and metric data are read from the same snapshot; certificate badges do not load check bodies or headers.

Embed in a README:

```
![Status](https://example.com/asura/api/v1/badge/1/status)
![Uptime](https://example.com/asura/api/v1/badge/1/uptime)
![Cert](https://example.com/asura/api/v1/badge/1/cert?label=TLS)
```

## Monitor Groups {#monitor-groups}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/groups` | List |
| `POST` | `/api/v1/groups` | Create |
| `PUT` | `/api/v1/groups/{id}` | Update |
| `DELETE` | `/api/v1/groups/{id}` | Delete |

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Group name (max 255 chars) |
| `sort_order` | int |  | Display order (default: 0) |

Assign monitors by setting `group_id` under the creation or move permissions described above. Deleting a nonempty group returns 409; move or ungroup its monitors first. The occupancy check and deletion are atomic. Group administration does not grant read access to its monitors.

## Proxies {#proxies}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/proxies` | List |
| `POST` | `/api/v1/proxies` | Create |
| `GET` | `/api/v1/proxies/{id}` | Get |
| `PUT` | `/api/v1/proxies/{id}` | Update |
| `DELETE` | `/api/v1/proxies/{id}` | Delete |

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Display name (max 255 chars) |
| `protocol` | string | yes | `http` or `socks5` |
| `host` | string | yes | Proxy hostname or IP |
| `port` | int | yes | Proxy port (1-65535) |
| `auth_user` | string |  | Authentication username |
| `auth_pass` | string |  | Authentication password |
| `enabled` | bool |  | Whether proxy is active (API creation default: false; the web creation form starts enabled) |

Assign a proxy with `proxy_id`. HTTP proxies are supported by HTTP and WebSocket checks. SOCKS5 is supported by HTTP, WebSocket, TCP, TLS, domain WHOIS, gRPC and MQTT checks, plus DNS checks with an explicit `settings.server` using TCP. Other types reject proxy assignments, including HTTP multi-step, SSH, SMTP, Redis, PostgreSQL, UDP and ICMP. A proxy protocol change must remain compatible with every assigned monitor. A disabled, unavailable or incompatible saved proxy makes the check fail; it never falls back to a direct connection. Proxy-assigned monitors run on the server because agents do not receive proxy credentials.

Deleting an assigned proxy returns `409`. Change each monitor's routing explicitly before deleting it. HTTP OAuth token requests use the monitor's proxy too; token endpoints must respond directly because redirects are not followed with client credentials.

Proxy reads hide `auth_pass`. An omitted or blank password on update retains the saved value; `clear_auth_pass: true` removes it, and a nonempty value replaces it. Other omitted fields are preserved. An ordinary editor changing protocol, host, port or authentication username must replace or remove a retained password. A super administrator can retain it across that destination change. Mutations without `proxies.read` return only the proxy ID.

Changing a proxy's protocol, endpoint or authentication also requires `monitors.update` and `monitors.assign` for **every current monitor using that proxy**. This is checked within the same transaction as the proxy edit, so proxy ownership alone cannot redirect another scope's request credentials. Name and enabled-state edits need only `proxies.update`.

Unless `allow_private_targets` is enabled, a proxied target (and, for HTTP monitors, every redirect target) is refused when it is a private or reserved IP or when its hostname resolves to one. The check uses Asura's own DNS resolver, but the proxy is still given the hostname and resolves it on its own, in its own network (which is why proxies are used: other regions, split DNS, hostname ACLs). The check therefore protects the network Asura runs in, but cannot constrain what the proxy reaches: a name can resolve differently for the proxy, or change between the two lookups. A hostname Asura cannot resolve at all (one that only exists in the proxy's network) is passed to the proxy unchecked. To keep a proxy away from internal services, use the proxy's own access controls (ACLs).

## Incidents {#incidents}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/incidents` | List (filter: monitor\_id, status) |
| `GET` | `/api/v1/incidents/{id}` | Get with timeline |
| `POST` | `/api/v1/incidents/{id}/ack` | Acknowledge |
| `POST` | `/api/v1/incidents/{id}/resolve` | Resolve |
| `DELETE` | `/api/v1/incidents/{id}` | Delete |

## Notifications {#notifications}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/notifications` | List |
| `POST` | `/api/v1/notifications` | Create |
| `PUT` | `/api/v1/notifications/{id}` | Update |
| `DELETE` | `/api/v1/notifications/{id}` | Delete |
| `POST` | `/api/v1/notifications/{id}/test` | Test |
| `GET` | `/api/v1/notifications/history` | Delivery history (filter: channel\_id, status, event\_type) |

Types: `webhook` `email` `telegram` `discord` `slack` `ntfy` `teams` `pagerduty` `opsgenie` `pushover` `googlechat` `matrix` `gotify`

Events: `incident.created` `incident.acknowledged` `incident.resolved` `incident.reminder` `incident.escalated` `content.changed` `cert.changed` `sla.breach` `maintenance.started` `maintenance.ended` (a channel with no event filter receives all of them)

See [Notifications](notifications.html) for per-type settings and webhook signing.

## Escalation Policies {#escalation-policies}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/escalation-policies` | List |
| `POST` | `/api/v1/escalation-policies` | Create |
| `GET` | `/api/v1/escalation-policies/{id}` | Get |
| `PUT` | `/api/v1/escalation-policies/{id}` | Update |
| `DELETE` | `/api/v1/escalation-policies/{id}` | Delete |

Permissions: `escalation_policies.read`, `escalation_policies.write`. See [Escalation Policies](escalation-policies.html) for details.

## SLA Reports {#sla-reports}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/monitors/{id}/sla` | SLA status for a monitor (current month uptime, error budget) |
| `GET` | `/api/v1/reports/sla?period=2025-01` | Monthly SLA report (all monitors with targets) |
| `GET` | `/api/v1/reports/sla/export?period=2025-01` | Export SLA report as JSON download |

Set `sla_target` (e.g. `99.9`) on a monitor to enable SLA tracking. The response includes current month uptime, error budget in seconds, and breach status. The report endpoint returns all monitors that have an SLA target configured.

## Maintenance Windows {#maintenance-windows}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/maintenance` | List |
| `POST` | `/api/v1/maintenance` | Create |
| `PUT` | `/api/v1/maintenance/{id}` | Update |
| `DELETE` | `/api/v1/maintenance/{id}` | Delete |

`recurring` values are `""` (one-time), `"daily"`, `"weekly"`, `"monthly"`, `"manual"` and `"cron"`. Cron windows also specify `cron_expr`.

Each mutation requires its action over every old and new target; an all-monitor window requires global authority. Updates additionally require `maintenance.toggle` only when `active` changes. Omitted `active` and `monitor_ids` preserve the current values. During maintenance, checks and incident state continue while incident announcements are deferred.

## Request Logs {#request-logs}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/request-logs` | List (filter: group, method, status\_code, monitor\_id, from, to) |
| `GET` | `/api/v1/request-logs/stats` | Aggregate stats (requests, visitors, latency) |

The `from` and `to` filters take RFC 3339 timestamps.

## Audit Log {#audit-log}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/audit` | List audit log entries (paginated) |

Records configuration changes (creates, updates, deletes) with the acting key, action, target, and timestamp. Requires `audit.read`.

## Export / Import {#export-import}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/export` | Export full configuration as JSON |
| `POST` | `/api/v1/import` | Import configuration from JSON |

Import and export require super administrator access, including redacted exports. No collection of ordinary resource permissions grants this access.

A full v2 export contains portable monitor configuration and relationships, groups, tags, proxies, notification channels, escalation policies, on-call rotations, maintenance windows and status pages. Relationships use file-local IDs that are remapped on import. Database history, subscribers, authentication and role configuration, agent enrollment tokens and heartbeat credentials are excluded. See [backup and configuration transfer](backup.html) for the complete inventory and disaster-recovery procedure.

Query parameters:

- `GET /api/v1/export?redact_secrets=true`: produces a sharing file with sensitive configuration removed. It cannot be imported; review free text and branding before sharing.
- `POST /api/v1/import?mode=merge`: keeps existing uniquely named entities, with status pages matched by slug, and reports them as skipped (default). Ambiguous existing names are rejected.
- `POST /api/v1/import?mode=replace`: replaces all portable configuration, deleting its related monitoring and notification history, and status-page subscribers.
- `agent_mapping=7:2,8:5`: optionally maps exported agent IDs to existing destination agent IDs. Unique matching agent names resolve automatically. Import never enrolls or enables agents.

Imports accept up to 32 MiB of JSON and commit as one transaction. Any validation or write failure rolls back the entire import. Success returns created counts for each entity type and a skipped count. Newly imported heartbeat monitors also return a one-time `heartbeats` array containing `monitor_id`, `monitor_name` and `token`; update those pingers with the new credentials. The response uses `Cache-Control: no-store`. Existing heartbeat monitors skipped during a merge retain their credentials.

## Tags {#tags}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/tags` | List all tags |
| `POST` | `/api/v1/tags` | Create a tag |
| `PUT` | `/api/v1/tags/{id}` | Update a tag |
| `DELETE` | `/api/v1/tags/{id}` | Delete a tag |

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Tag name (max 50 chars) |
| `color` | string |  | Hex color (default: `#808080`) |

Tags are global. Assign them with `monitor_tags`, for example `[{"tag_id":1,"value":"production"}]`, on monitor creation or update. Changing assignments requires `monitors.assign` on the monitor and `tags.read`; omission preserves current assignments. Monitor reads without `tags.read` expose necessary tag IDs without names, colors or values. Deleting a tag removes its monitor assignments. Tags and per-monitor values are included in global Prometheus metrics and configuration exports.

## Database {#database}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/db/size` | Database file size and page stats |
| `POST` | `/api/v1/db/vacuum` | Run SQLite VACUUM to reclaim space |

`db/size` requires `database.read`. `db/vacuum` requires `database.maintain`.

## Other {#other}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/overview` | Status summary |
| `GET` | `/api/v1/events` | Server-Sent Events stream (SSE) |

### Server-Sent Events {#server-sent-events}

`GET /api/v1/events` opens an SSE stream using an API key; the web UI uses session-authenticated `GET /events`. Admission requires any scoped `monitors.read`, `incidents.read` or `sla.read` capability. Each event is then authorized separately for its monitor:

| Event family | Required read authority |
| --- | --- |
| `incident.created`, `incident.acknowledged`, `incident.resolved`, `incident.reminder`, `incident.escalated` | `incidents.read` |
| `content.changed`, `cert.changed` | `monitors.read` |
| `sla.breach` | `sla.read` |

The JSON `data` contains `monitor_id`; incident events also contain `incident_id` and optionally `status` (`open`, `acknowledged` or `resolved`). Nonincident events contain only the monitor ID. Payloads contain no names, settings, credentials or full resource objects. Unknown event types are dropped.

The server rechecks the current principal and object scope before every queued event. A 30-second heartbeat also checks principal and session validity. Logout, session expiry, key removal or key rotation closes the stream. A group move or loss of scope suppresses unauthorized events. At most 100 connections are admitted concurrently.

## Agents {#agents}

Distributed probe agents run checks from remote locations and report results back to the central server.

### Admin Endpoints (API key auth) {#admin-endpoints-api-key-auth}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/agents` | List all agents |
| `POST` | `/api/v1/agents` | Create agent (returns token) |
| `DELETE` | `/api/v1/agents/{id}` | Delete agent |
| `GET` | `/api/v1/agents/status` | Agent online/offline status |

`POST /api/v1/agents` returns the created agent including its `token`. The token is shown once at creation — store it now, as it cannot be retrieved later. Pass it as the `X-Agent-Token` header when running the agent.

### Agent Endpoints (X-Agent-Token auth) {#agent-endpoints-x-agent-token-auth}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/agent/jobs` | Poll for monitors to check |
| `POST` | `/api/v1/agent/results` | Submit check results (JSON array) |

Agent results are processed through the full pipeline: monitor status updates, incident creation/resolution, and notifications all work the same as central checks. Monitors whose settings include credentials or mTLS material (basic auth password, bearer token, OAuth2 client secret, mTLS client key, client certificate or CA certificate, password) are not offered to agents; they are checked only by the central server.

Only enabled monitors with `agent_enabled` set to true and an explicit assignment to the authenticated, enabled agent are included in job responses. Empty assignments mean no remote jobs. Disable this flag on monitors targeting localhost or private IPs that should only be checked by the central server. An agent refuses private, loopback and other reserved targets unless it is started with `-allow-private`.

Custom request headers and bodies (including those of `http_multi` steps) are sent to agents unchanged and often carry credentials such as `Authorization`, `X-API-Key` or `Cookie`. A monitor that has them is therefore only offered to the agents it is explicitly assigned to (`agent_ids`), never to every agent.

Deleting an agent never widens a monitor to every agent: a monitor whose only assigned agent is deleted gets `agent_enabled` set to false, which is recorded in the audit log. The `DELETE` response lists these monitors in `agent_disabled_monitor_ids`.

Each object posted to `/api/v1/agent/results` has `monitor_id`, the positive `config_revision` copied unchanged from its job, `status`, `response_time` (ms), `status_code`, `message`, `body_hash`, `cert_fingerprint`, and optionally `dns_records` (a JSON-encoded array of strings, used for `dns_record` assertions; when it is absent those assertions are skipped) and `checked_at` (RFC 3339 time the check ran; defaults to the time the result is received, and a time in the future counts as the receive time). For the same current configuration revision, a result checked before the newest result already applied from the same agent is kept in the history but does not change the monitor's status or incidents. There is no age limit, and results from the server or other agents never make an agent's result stale, so an agent with a long cycle is still applied.

Upgrade the server and agents together to v2. Results without a positive revision are rejected. Monitor edits, delegation changes, agent enable/disable changes and proxy edits invalidate earlier jobs; the server rechecks revision and delegation in the transaction that stores the result. An obsolete job writes no history, status or incident. The response reports `accepted` and `rejected` counts, plus rejected subsets `stale` (obsolete or no longer delegated) and `failed` (server persistence/read failures); remaining rejections have invalid fields. The agent reports incomplete acceptance and fetches current jobs on its next poll.

### Running an Agent {#running-an-agent}

```
asura-agent --server https://your-server.com --token <agent-token> --interval 30s
```

| Flag | Default | Description |
| --- | --- | --- |
| `-server` |  | Asura server URL (required) |
| `-token` |  | Agent token (required) |
| `-interval` | `30s` | How often to poll the server for jobs |
| `-concurrency` | `10` | Maximum number of checks run at the same time |
| `-allow-private` | `false` | Allow checks against private, loopback and other reserved addresses, for agents inside a LAN |
| `-version` |  | Print the version and exit |

Flags can be written with one or two dashes (`-server` or `--server`).

The agent binary polls the server for jobs, runs checks locally, and posts results back. It needs an outbound connection to the configured server; use HTTPS for remote connections. Choose the binary matching the host operating system and architecture.

## On-Call Rotations {#on-call-rotations}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/on-call` | List all rotations |
| `GET` | `/api/v1/on-call/current` | Current on-call per rotation |
| `POST` | `/api/v1/on-call` | Create rotation |
| `PUT` | `/api/v1/on-call/{id}` | Update rotation |
| `POST` | `/api/v1/on-call/{id}/override` | Set, replace or clear a temporary override |
| `DELETE` | `/api/v1/on-call/{id}` | Delete rotation |

Rotations expose a contact schedule through the API; they are not automatically connected to notification dispatch. Daily periods are 24 hours and weekly periods are seven days in UTC, measured from `period_started_at`. Current contact calculations account for missed ticks and restarts. Name and override edits preserve cadence; changes to period, members or current index start a new period. Schedule anchors and timestamps are read-only, and omitted update fields are preserved.

Creation and channel assignment require `notifications.read` in addition to the relevant `on_call` mutation. Setting or replacing an override requires `on_call.override` and `notifications.read`; clearing an override requires only `on_call.override`. Channel names are omitted without channel-read authority.
