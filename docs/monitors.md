---
title: "Monitors"
nav_title: "Monitors"
description: "Monitor websites, APIs and network services with Asura. Configure HTTP, DNS, TLS, heartbeat and other checks, assertions and remote probes."
permalink: "/monitors.html"
---

# Monitors {#monitors}

Asura supports 19 monitor types: 17 active protocol checks, heartbeat monitors for incoming pings, and manually controlled monitors. Each monitor has its own interval, protocol settings and optional assertions.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Monitor Fields {#monitor-fields}

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| `name` | string | yes | Display name |
| `description` | string |  | Optional description, rendered as markdown on the detail page (max 5000 chars) |
| `type` | string | yes | `http` `http_multi` `tcp` `dns` `icmp` `tls` `websocket` `command` `docker` `heartbeat` `domain` `grpc` `mqtt` `smtp` `ssh` `redis` `postgresql` `udp` `manual` |
| `target` | string | for active checks | URL, host:port, domain, or command; not required for heartbeat or manual monitors |
| `interval` | int |  | Seconds between checks (default: 60) |
| `timeout` | int |  | Timeout in seconds (default: 10) |
| `tags` | string\[\] |  | Grouping tags |
| `monitor_tags` | object\[\] |  | Managed tag assignments, e.g. `[{"tag_id":1,"value":"production"}]`; changes require `monitors.assign` and `tags.read`, and omission on update preserves assignments |
| `group_id` | int |  | Monitor group ID (null if ungrouped) |
| `proxy_id` | int |  | Proxy ID for routing checks through a proxy |
| `notification_channel_ids` | int\[\] |  | Notification channels to route to (empty = all) |
| `settings` | object |  | Protocol-specific (see below) |
| `assertions` | object |  | Condition groups (see below) |
| `track_changes` | bool |  | Enable content change detection |
| `failure_threshold` | int |  | Failures before incident (default: 3) |
| `success_threshold` | int |  | Successes before recovery (default: 1) |
| `upside_down` | bool |  | Inverted mode. "up" becomes "down" and vice versa |
| `agent_enabled` | bool |  | Enable remote checks on explicitly assigned agents (API creation default: false). Empty assignments produce no remote jobs. Proxy assignments, typed authentication settings, mTLS material and server-only monitor types remain ineligible for delegation |
| `agent_ids` | int\[\] |  | Existing trusted agent IDs. An omitted `PUT` field preserves current assignments; `[]` removes them. Changing delegation requires `monitors.assign` and `agents.read`. Newly enrolled agents receive no jobs until assigned |
| `resend_interval` | int |  | Seconds between reminder notifications while an incident is open (0 = notify once only) |
| `sla_target` | float |  | Scheduled-check SLA target percentage, from 0 to 100 (e.g. 99.9). Set to 0 to disable; unavailable for manual and heartbeat types |

Agent assignment is a trust decision: eligible jobs include their target, custom request headers and bodies, which may contain credentials. Assign only agents you control. The server keeps proxy-routed checks, typed authentication and mTLS settings local. Upgrade the server and agents together; v2 jobs and results use a configuration revision to reject obsolete or revoked work. See the [agent API](api.html#agents).

## Protocol Settings {#protocol-settings}

### HTTP {#http}

| Field | Type | Description |
| --- | --- | --- |
| `method` | string | HTTP method (default: `GET`) |
| `headers` | map\[string\]string | Custom request headers |
| `body` | string | Request body (for POST/PUT/PATCH) |
| `body_encoding` | string | Content-Type hint: `json`, `xml`, `form`, or empty for raw |
| `auth_method` | string | `none`, `basic`, `bearer`, or `oauth2` (default: inferred) |
| `basic_auth_user` | string | Basic auth username |
| `basic_auth_pass` | string | Basic auth password |
| `bearer_token` | string | Bearer token |
| `oauth2_token_url` | string | OAuth2 token endpoint URL |
| `oauth2_client_id` | string | OAuth2 client ID |
| `oauth2_client_secret` | string | OAuth2 client secret |
| `oauth2_scopes` | string | OAuth2 scopes (space-separated) |
| `oauth2_audience` | string | OAuth2 audience (optional) |
| `mtls_enabled` | bool | Enable mutual TLS client authentication |
| `mtls_client_cert` | string | Client certificate (PEM) |
| `mtls_client_key` | string | Client private key (PEM) |
| `mtls_ca_cert` | string | Custom CA certificate (PEM, optional) |
| `expected_status` | int | Expected HTTP status code (0 = any 2xx/3xx) |
| `follow_redirects` | bool | Follow redirects (default: true) |
| `max_redirects` | int | Maximum redirect hops (0 = don't follow, default: 10) |
| `skip_tls_verify` | bool | Skip TLS certificate verification |
| `cache_buster` | bool | Append unique query param to bypass caches |

```
{
  "method": "POST",
  "headers": {"X-Custom": "value"},
  "body": "{\"key\":\"value\"}",
  "body_encoding": "json",
  "auth_method": "bearer",
  "bearer_token": "eyJhbGci...",
  "expected_status": 200,
  "max_redirects": 5,
  "skip_tls_verify": false,
  "cache_buster": false
}
```

#### OAuth2 Client Credentials {#oauth2-client-credentials}

```
{
  "auth_method": "oauth2",
  "oauth2_token_url": "https://auth.example.com/oauth/token",
  "oauth2_client_id": "my-client-id",
  "oauth2_client_secret": "my-secret",
  "oauth2_scopes": "read write",
  "oauth2_audience": "https://api.example.com"
}
```

Asura fetches a token from the token URL using the client credentials grant and the monitor's proxy, when configured. It caches the token in memory for at most one day and refreshes before expiry. Credential, recipient or proxy changes invalidate reuse. The token endpoint must respond directly; redirects are not followed with client credentials. An unreachable or invalid token endpoint makes the check fail without recording its response body.

#### Mutual TLS (mTLS) {#mutual-tls-mtls}

```
{
  "mtls_enabled": true,
  "mtls_client_cert": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----",
  "mtls_client_key": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
  "mtls_ca_cert": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----"
}
```

mTLS is independent of the auth method and can be combined with any authentication type. The CA certificate is optional and used for custom trust stores (e.g., self-signed server certificates). Certificates are stored inline as PEM text.

### TCP {#tcp}

```
{"send_data": "PING\r\n", "expect_data": "PONG"}
```

### DNS {#dns}

```
{"record_type": "A", "server": "8.8.8.8"}
```

### TLS {#tls}

```
{"warn_days_before": 30}
```

Expiration uses the certificate's exact timestamp. The displayed days remaining and the warning window do not postpone expiration until the next whole day.

### WebSocket {#websocket}

```
{"headers": {"Authorization": "Bearer token"}, "send_message": "ping", "expect_reply": "pong"}
```

### Command {#command}

```
{"command": "/usr/local/bin/check_health", "args": ["--host", "db.local"]}
```

### Docker {#docker}

| Field | Type | Description |
| --- | --- | --- |
| `container_name` | string | Container name or ID (overrides target if set) |
| `socket_path` | string | Docker socket path (default: `/var/run/docker.sock`) |
| `check_health` | bool | Use container health status instead of just running state |

```
{"container_name": "nginx", "socket_path": "/var/run/docker.sock", "check_health": true}
```

Status mapping:

- **up**: container running (or healthy when `check_health` is enabled)
- **degraded**: container running, health status is `starting`
- **down**: container stopped/exited, not found, or unhealthy

**Setup. systemd (native install):**

```
sudo usermod -aG docker asura
sudo systemctl restart asura
```

**Setup. Docker:** Mount the host socket:

```
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
```

### Domain (WHOIS) {#domain-whois}

| Field | Type | Description |
| --- | --- | --- |
| `warn_days_before` | int | Days before expiry to mark as degraded (default: 30) |

```
{"warn_days_before": 30}
```

Target is a domain name (e.g. `example.com`). Asura queries the appropriate WHOIS server for the TLD and parses its expiry date. Expiration uses the exact available timestamp; rounded days are used for display and the warning window.

### gRPC {#grpc}

| Field | Type | Description |
| --- | --- | --- |
| `service_name` | string | gRPC service to health-check (empty = all) |
| `use_tls` | bool | Use TLS (default: false) |
| `skip_tls_verify` | bool | Skip TLS certificate verification |

```
{"service_name": "my.service.v1", "use_tls": true}
```

Uses the standard `grpc.health.v1.Health/Check` protocol. Default port: 50051 (plaintext), 443 (TLS).

### MQTT {#mqtt}

| Field | Type | Description |
| --- | --- | --- |
| `client_id` | string | MQTT client ID (auto-generated if empty) |
| `username` | string | Authentication username |
| `password` | string | Authentication password |
| `topic` | string | Topic to subscribe to (optional) |
| `use_tls` | bool | Use TLS (default: false) |

```
{"username": "monitor", "password": "secret", "topic": "health/status", "use_tls": true}
```

Connects using MQTT 3.1.1. Default port: 1883 (plaintext), 8883 (TLS). Optionally subscribes to a topic to verify permissions.

## Heartbeat Monitoring {#heartbeat-monitoring}

Create a heartbeat monitor to track cron jobs, workers, or pipelines. If they stop pinging, Asura fires an incident.

```
# Create heartbeat monitor
curl -X POST https://example.com/asura/api/v1/monitors \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"Nightly Backup","type":"heartbeat","interval":3600,"settings":{"grace":300}}'
```

The successful creation response includes the ping token once. Store it securely; later reads cannot recover it. The web creation form similarly shows a one-time ping URL.

```
{
  "monitor": { "id": 1, "name": "Nightly Backup", "type": "heartbeat", ... },
  "heartbeat": { "token": "a1b2c3d4e5f6...", "grace": 300, "status": "pending" }
}
```

Ping from your script (no auth needed):

```
curl -X POST https://example.com/asura/api/v1/heartbeat/a1b2c3d4e5f6...
```

After the first ping, if no further ping arrives within `interval + grace` seconds, heartbeat evaluation marks the monitor down and opens an incident. Explicit heartbeat transitions bypass consecutive-check thresholds. Heartbeat monitors have no measured network response time.

## Manual Monitoring {#manual-monitoring}

Manual monitors let you control status for manual processes, third-party dependencies or business-hour services. Asura does not probe a remote target: you set the status through the web UI or API.

```
# Create manual monitor
curl -X POST https://example.com/asura/api/v1/monitors \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"Third-Party Payment Gateway","type":"manual"}'
```

Set status via API:

```
# Set to down with a message
curl -X POST https://example.com/asura/api/v1/monitors/1/status \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d '{"status":"down","message":"Vendor reported outage"}'

# Set back to up
curl -X POST https://example.com/asura/api/v1/monitors/1/status \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d '{"status":"up","message":"Vendor confirmed recovery"}'
```

Valid statuses are `up`, `down` and `degraded`. An authorized status change records a check and updates the state immediately; down or degraded opens or updates an incident, and up resolves it. Maintenance can defer incident notifications. The detail page offers status controls to principals with `monitors.status` authority on that monitor.

## Inspecting private diagnostics {#inspecting-private-diagnostics}

Ordinary monitor reads show status and measured timing while keeping response headers, bodies, errors, content hashes and diffs private. Inspecting those stored details requires `monitors.read` together with `diagnostics.read` for the same monitor. Raw incident causes and timeline text use `incidents.read` plus `diagnostics.read`. Viewer and operator roles do not include diagnostic access by default.

Assertion definitions and multi-step extraction regex/JSON expressions may contain private literals, so ordinary editor views hide them too. An unrelated edit with those fields left blank preserves the current stored definitions. Supply new nonempty definitions to replace them, or explicitly remove them with the saved-value control or API `clear_secrets` paths. Saved request credentials, bodies, command arguments and full URLs remain hidden even with diagnostic access. See [API diagnostic fields](api.html#diagnostic-fields) for exact fields and removal paths.

A multi-step edit that retains private inputs must preserve the ordered request flow, including destinations, extraction rules and references in later requests. Unchanged independent requests may be reordered only without extraction or interpolation. Changing the flow requires replacing or clearing borrowed private inputs, or super administrator authority. This prevents a new step from forwarding an authenticated response elsewhere.

## Assertions {#assertions}

Assertions are evaluated after each check. A failed assertion marks a monitor `down` or, if `degraded` is set, `degraded` instead.

Conditions are organized into groups. Each group has its own `and`/`or` operator, and the top-level `operator` controls how the groups themselves are combined. The web UI lets you build this visually. API consumers send the structured object directly.

### Condition Types {#condition-types}

| Type | Description | Operators |
| --- | --- | --- |
| `status_code` | HTTP status code | eq, neq, gt, lt, gte, lte |
| `body_contains` | Body text search | contains, not\_contains |
| `body_regex` | Body regex match | matches, not\_matches |
| `json_path` | JSON value at dot-path | eq, neq, gt, lt, contains, exists |
| `header` | Response header value | eq, neq, contains, exists |
| `response_time` | Response time (ms) | lt, lte, gt, gte |
| `cert_expiry` | Days until cert expires | gt, gte, lt, lte |
| `dns_record` | DNS record value | contains, eq |

### Structure {#structure}

```
{
  "assertions": {
    "operator": "and",
    "groups": [
      {
        "operator": "or",
        "conditions": [
          {"type": "status_code", "operator": "eq", "value": "200"},
          {"type": "status_code", "operator": "eq", "value": "201"}
        ]
      },
      {
        "operator": "and",
        "conditions": [
          {"type": "response_time", "operator": "lt", "value": "5000"},
          {"type": "json_path", "target": "status", "operator": "eq", "value": "ok"}
        ]
      },
      {
        "operator": "and",
        "conditions": [
          {"type": "response_time", "operator": "lt", "value": "2000", "degraded": true}
        ]
      }
    ]
  }
}
```

With a 200 or 201 status and a JSON `status` field of `ok`, this example is up below 2 seconds, degraded from 2 seconds to below 5 seconds, and down at 5 seconds or longer. A failed hard condition dominates failed degraded conditions, regardless of their order. A passing alternative still satisfies an `or` group.

## Additional Monitor Types {#additional-monitor-types}

### SMTP {#smtp}

Checks mail server availability via the EHLO handshake. If an expected banner is configured, the server's greeting must contain that string or the check fails. Supports STARTTLS. Configurable port (25, 465, 587).

### SSH {#ssh}

Connects to an SSH server and requires a complete, syntactically valid identification line after a bounded number of pre-banner lines. A bare `SSH-` prefix is insufficient. An optional fingerprint compares the SHA-256 hash of the identification banner. This checks the banner rather than the host key and does not provide cryptographic host identity or MITM protection.

### Redis {#redis}

Sends PING via the RESP protocol and expects PONG. Supports optional AUTH password. Default port 6379.

### PostgreSQL {#postgresql}

Sends a PostgreSQL protocol v3 startup message and validates the bounded authentication or error response. This verifies that a PostgreSQL server answers, including an authentication or database-access error; it does not authenticate or run a query. Username and database are configurable, and no database driver is required for the check.

### UDP {#udp}

Sends a packet and optionally validates response content. Useful for DNS-over-UDP and custom UDP services.

### HTTP Multi-Step {#http-multi-step}

Executes a sequence of up to 5 HTTP requests. Variables extracted from one step (via regex or JSON dot-path) can be interpolated into subsequent steps using `{% raw %}{{var}}{% endraw %}` syntax. The check fails if any step returns an unexpected status or errors. Response time is the sum of all steps.

{% raw %}```
{
  "steps": [
    {
      "name": "Login",
      "url": "https://api.example.com/auth",
      "method": "POST",
      "headers": {"Content-Type": "application/json"},
      "body": "{\"user\":\"test\",\"pass\":\"secret\"}",
      "expected_status": 200,
      "extract_var": "token",
      "extract_json": "data.access_token"
    },
    {
      "name": "Get profile",
      "url": "https://api.example.com/me",
      "method": "GET",
      "headers": {"Authorization": "Bearer {{token}}"},
      "expected_status": 200
    }
  ]
}
```{% endraw %}

## Anomaly Detection {#anomaly-detection}

Per-monitor anomaly sensitivity detects response time spikes by comparing against a 7-day rolling baseline. When a check exceeds the configured threshold, the monitor is marked degraded instead of up.

| Sensitivity | Threshold |
| --- | --- |
| Off | Disabled |
| Low | 3 standard deviations above baseline |
| Medium | 2 standard deviations |
| High | 1.5 standard deviations |

Baselines recalculate every 6 hours. A minimum of 30 data points are required before detection activates. Set via the `anomaly_sensitivity` field or the monitor form.

## Incident Severity {#incident-severity}

Incidents are auto-assigned a severity when created: `critical` for down monitors, `warning` for degraded. Severity can be manually overridden via `POST /api/v1/incidents/{id}/severity` with `{"severity": "major"}`. Four levels: critical, major, minor, warning.

## Uptime Heatmap {#uptime-heatmap}

Scheduled monitors show a 365-day uptime calendar with neutral and blue shading. Its legend distinguishes at least 99.9%, 99% to below 99.9%, and below 99%; days without samples stay separate. Hover for a daily value or open the native daily-data table for exact percentages and check counts using a keyboard or touch. No samples are shown as an em dash, not as measured 100% uptime.

Manual and heartbeat monitors report state, not sampled uptime or latency. Their detail pages show the latest reported state and update; heartbeat details also show the last ping and grace period. State history remains available, but response charts, sampled uptime, heatmaps and SLA budgets are unavailable. Uptime and response badges show `n/a`, and public JSON uptime is `null`.

History records its source independently of the monitor's current type. Scheduled
server and agent results are probes; manual state changes remain manual observations.
Heartbeat pings update state without adding scheduled-check samples. Changing a
monitor's type preserves history and never turns manual state into uptime or latency
samples. Pre-schema-41 records have an unknown source and are excluded from verified
sample metrics. Genuine probe history remains available after later type changes,
including a measured response time of `0` ms. See the
[upgrade note](upgrading.html#review-historical-metrics) before migrating an existing database.

Legacy SLA targets on passive monitors remain stored and inactive. An unrelated edit preserves that value; clear it explicitly to remove it. When changing a scheduled monitor to a passive type, clear any active SLA target to `0` first. Custom targets, including `100`, are preserved by the form.
