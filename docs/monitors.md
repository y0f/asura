---
title: "Monitors"
nav_title: "Monitors"
description: "Monitor websites, APIs and network services with Asura. Configure HTTP, DNS, TLS, heartbeat and other checks, assertions and remote probes."
permalink: "/monitors.html"
---

# Monitors {#monitors}

Asura supports 19 monitor types. Each monitor runs on its own interval with protocol-specific settings and optional assertions.

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
| `target` | string | yes | URL, host:port, domain, or command |
| `interval` | int |  | Seconds between checks (default: 60) |
| `timeout` | int |  | Timeout in seconds (default: 10) |
| `tags` | string\[\] |  | Grouping tags |
| `group_id` | int |  | Monitor group ID (null if ungrouped) |
| `proxy_id` | int |  | Proxy ID for routing checks through a proxy |
| `notification_channel_ids` | int\[\] |  | Notification channels to route to (empty = all) |
| `settings` | object |  | Protocol-specific (see below) |
| `assertions` | object |  | Condition groups (see below) |
| `track_changes` | bool |  | Enable content change detection |
| `failure_threshold` | int |  | Failures before incident (default: 3) |
| `success_threshold` | int |  | Successes before recovery (default: 1) |
| `upside_down` | bool |  | Inverted mode. "up" becomes "down" and vice versa |
| `agent_enabled` | bool |  | Include this monitor in agent job distribution (default: true). Disable for monitors targeting localhost or private IPs. Monitors whose settings contain credentials or mTLS material are never offered to agents and are checked only by the central server |
| `agent_ids` | int\[\] |  | IDs of the agents that run this monitor (empty = every agent). Each ID must name an existing agent. `PUT` replaces the list, so leaving it out clears the assignment. Only `GET /api/v1/monitors/{id}` returns it; the list endpoint does not |
| `resend_interval` | int |  | Seconds between reminder notifications while an incident is open (0 = notify once only) |
| `sla_target` | float |  | SLA uptime target percentage (e.g. 99.9). Set to 0 to disable SLA tracking |

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

Asura fetches a token from the token URL using the client credentials grant, caches it in memory, and automatically refreshes before expiry. If the token endpoint is unreachable, the monitor goes down with a distinct error message.

#### Mutual TLS (mTLS) {#mutual-tls-mtls}

```
{
  "mtls_enabled": true,
  "mtls_client_cert": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----",
  "mtls_client_key": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----",
  "mtls_ca_cert": "-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----"
}
```

mTLS is independent of the auth method. it can be combined with any authentication type. The CA certificate is optional and used for custom trust stores (e.g., self-signed server certificates). Certificates are stored inline as PEM text.

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

Target is a domain name (e.g. `example.com`). Queries the appropriate WHOIS server for the TLD, parses the expiry date, and reports status based on remaining days.

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

Response includes the ping token:

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

If no ping arrives within `interval + grace` seconds, the monitor goes down and an incident is created.

## Manual Monitoring {#manual-monitoring}

Manual monitors let you control status directly for things that can't be auto-checked. manual processes, third-party dependencies, or business-hour services. No automated checks run; you set the status via the web UI or API.

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

Valid statuses: `up`, `down`, `degraded`. Status changes create check results and trigger incidents normally. the same notification channels, thresholds, and incident lifecycle apply. The web UI shows Set Up / Set Degraded / Set Down buttons on the monitor detail page.

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
          {"type": "response_time", "operator": "lt", "value": "2000"},
          {"type": "json_path", "target": "status", "operator": "eq", "value": "ok"}
        ]
      },
      {
        "operator": "and",
        "conditions": [
          {"type": "response_time", "operator": "lt", "value": "5000", "degraded": true}
        ]
      }
    ]
  }
}
```

In this example: the monitor passes if the status code is 200 *or* 201 **and** the response time is under 2 s **and** the `status` JSON field equals `ok`. A response time between 2 s and 5 s marks the monitor degraded instead of down.

## Additional Monitor Types {#additional-monitor-types}

### SMTP {#smtp}

Checks mail server availability via the EHLO handshake. If an expected banner is configured, the server's greeting must contain that string or the check fails. Supports STARTTLS. Configurable port (25, 465, 587).

### SSH {#ssh}

Connects to an SSH server and reads the identification banner. The check passes if the banner starts with the `SSH-` prefix (any protocol version). An optional fingerprint check compares a SHA-256 hash of the banner string. Note: this verifies the server's identification banner, not its host key — it does not provide cryptographic host identity or MITM protection.

### Redis {#redis}

Sends PING via the RESP protocol and expects PONG. Supports optional AUTH password. Default port 6379.

### PostgreSQL {#postgresql}

Performs a protocol v3 startup handshake to verify the server accepts connections. Configurable username and database. No driver required.

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

The monitor detail page shows a 365-day uptime calendar. Each cell represents one day, color-coded by uptime percentage: green (100%), yellow (>99%), orange (>95%), red (<95%). Hover for exact percentages.
