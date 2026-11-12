---
title: "Configuration"
nav_title: "Configuration"
description: "Configure Asura's server, SQLite or optional PostgreSQL storage, API keys, sessions and monitoring settings. Includes environment variables and deployment options."
permalink: "/configuration.html"
---

# Configuration {#configuration}

Server, runtime and authentication settings live in `config.yaml`. Monitor, notification-channel and status-page definitions are stored in the application database. See [config.example.yaml](https://github.com/y0f/asura/blob/main/config.example.yaml) for additional settings. Environment variables expand via `${VAR_NAME}` before YAML parsing; missing variables expand to an empty string. Restart Asura after changing this file or its environment.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Server Settings {#server-settings}

| Setting | Default | Description |
| --- | --- | --- |
| `listen` | `127.0.0.1:8090` | Address to bind. Keep this localhost-only and put a reverse proxy in front |
| `base_path` | `""` | URL prefix for all routes (e.g. `/asura`) |
| `external_url` | `http://{listen}{base_path}` | Public URL used in notification links; set the HTTPS URL explicitly behind a proxy |
| `trusted_proxies` | `[]` | IPs/CIDRs of reverse proxies whose forwarding headers are trusted. The client address is the first untrusted entry from the right of `X-Forwarded-For`, which the proxy must append to; `X-Real-IP` is used only without `X-Forwarded-For`. Vendor headers such as `CF-Connecting-IP` are ignored; behind Cloudflare, add its address ranges here. HSTS is sent when TLS ends at Asura or a trusted proxy reports `X-Forwarded-Proto: https` |
| `rate_limit_per_sec` | `30` | Per-client request rate limit; IPv6 clients are grouped by /64 |
| `rate_limit_burst` | `60` | Burst allowance for that limiter |
| `max_body_size` | `1048576` | Ordinary request-body limit in bytes; authenticated configuration transfer has its own 32 MiB limit |
| `web_ui_enabled` | `true` | Serve the web dashboard. Set `false` for API-only operation; REST endpoints retain their authorization requirements |

## Database backend {#database-backend}

SQLite is the default. The standard release binaries, installer and Docker build include SQLite only. PostgreSQL requires the [optional source build](deployment.html#optional-postgresql-build); selecting `postgres` in a standard binary returns an actionable startup error.

| Setting | Default | Description |
| --- | --- | --- |
| `database.driver` | `sqlite` | `sqlite` or `postgres` |
| `database.path` | `asura.db` | SQLite file; ignored by PostgreSQL |
| `database.dsn` | empty | PostgreSQL connection string; invalid for SQLite |
| `database.max_read_conns` | `4` | Maximum ordinary read connections; must be positive |
| `database.encryption_key` | empty | Optional key for stored credentials, independent of connection TLS |

```yaml
database:
  driver: postgres
  dsn: "${ASURA_DATABASE_DSN}"
  max_read_conns: 4
  encryption_key: "${ASURA_ENCRYPTION_KEY}"
```

Set the environment values through your service's secret configuration. A PostgreSQL DSN can contain credentials; keep it out of source control and diagnostic output. For a remote database, configure certificate-verified TLS, for example `sslmode=verify-full` with the appropriate `sslrootcert`. Back up the encryption key separately from the database.

## Authentication {#authentication}

API-key hashes, role definitions and bindings are configured in YAML. Keys use SHA-256 hashes; the original key is not recoverable. Sessions and two-factor state use the application database. There is no public user-registration flow.

### Generating a Key {#generating-a-key}

```
./asura --setup
```

Output:

```
  API Key        : ak_a8f3e7b2c1d9...
  Hash           : fa223e3e1c4b96...
  Encryption Key : 4c1f0e9a7b3d52...

  Put the hash in config.yaml under auth.api_keys[].hash
  Put the encryption key in config.yaml under database.encryption_key
  Back up the encryption key separately from the database.
  Save the API key -- it cannot be recovered.
```

Generated keys have an `ak_` prefix. Treat the whole key as a credential. API keys are stored as an unsalted SHA-256 hash, which is only safe for long random keys, so prefer generated keys. To hash an existing key of at least 32 random characters:

```
./asura --hash-key "your-secret-key"
```

`--hash-key` rejects shorter or repetitive keys.

### Config {#config}

```
auth:
  api_keys:
    - name: "admin"
      hash: "2c26b46b68ffc..."
      super_admin: true
```

### Roles {#roles}

| Role | Access |
| --- | --- |
| `viewer` | Monitor-associated read access, scoped through a role binding |
| `operator` | Viewer reads plus operational monitor, incident and maintenance actions |
| `administrator` | Every ordinary action; global binding only, without `super_admin` |
| `role: admin` | Shorthand for explicit super-administrator access |
| `role: readonly` | A fixed global read set that excludes subscriber contacts and does not grow with new resources |

Use `auth.roles` for custom reusable roles and `api_keys[].role_bindings` for explicit global, group or monitor scopes. See [Access control](access-control.html) for complete YAML examples, built-in actions and role shorthands. Ordinary roles cannot grant `super_admin`.

### Custom Permissions {#custom-permissions}

Direct permissions are global grants and can coexist with role bindings:

```
auth:
  api_keys:
    - name: "ci-bot"
      hash: "..."
      permissions:
        - "monitors.read"
        - "monitors.write"
        - "incidents.read"
```

Permissions use `resource.action`, for example `monitors.pause`. Each resource with mutation actions also accepts `resource.write`, which grants all of that resource's mutations. A write grant never grants read access or access to another resource. Unknown resources, actions, roles, and wildcard scopes are rejected.

| Resource | Actions |
| --- | --- |
| `monitors` | `read`, `create`, `update`, `delete`, `pause`, `resume`, `status`, `clone`, `assign` |
| `diagnostics` | `read` |
| `incidents` | `read`, `acknowledge`, `resolve`, `severity`, `delete` |
| `notifications` | `read`, `create`, `update`, `delete`, `test` |
| `notification_history` | `read` |
| `escalation_policies` | `read`, `create`, `update`, `delete` |
| `maintenance` | `read`, `create`, `update`, `delete`, `toggle` |
| `groups` | `read`, `create`, `update`, `delete` |
| `tags` | `read`, `create`, `update`, `delete` |
| `proxies` | `read`, `create`, `update`, `delete` |
| `agents` | `read`, `create`, `delete` |
| `status_pages` | `read`, `create`, `update`, `delete` |
| `subscribers` | `read`, `delete` |
| `on_call` | `read`, `create`, `update`, `delete`, `override` |
| `request_logs` | `read` |
| `audit` | `read` |
| `settings` | `read` |
| `database` | `read`, `maintain` |
| `metrics` | `read` |
| `sla` | `read` |

Operations may require several independent actions. Cloning checks source read/clone, destination clone, and permission to copy relationships. Moving a monitor checks source/destination update and assignment plus `groups.read`. Editing a resource through a form requires its read and update actions. Bulk changes authorize every selected object. Configuration import and export require `super_admin: true`, even when every ordinary permission is granted. Subscriber contacts require `subscribers.read`; global metrics require the separate operational-export grant `metrics.read`.

`diagnostics.read` supports monitor/group scope and must be granted explicitly for raw check output, incident causes/timeline text and assertion/extraction definitions. It is absent from the built-in viewer/operator roles and the `readonly` shorthand; administrator includes it. It never grants ordinary monitor or incident reads by itself. See [Diagnostic access](access-control.html#diagnostic-access).

### Resource scopes {#resource-scopes}

Each resource has its own scopes: `monitors.*` does not cover groups, tags, proxies, agents, status pages, subscribers or SLA reports, and `notifications.*` does not cover notification history or on-call rotations. Grant each resource you need explicitly. Replace a write umbrella with individual actions when a key should only perform part of the workflow. A key needs access to at least one application page to start a web session; API-only permissions such as `metrics.read` still work through the API. Session requests recheck the key's current permissions.

### Using a Key {#using-a-key}

**API**: Pass the raw key (not the hash) in the `X-API-Key` header:

```
curl -H "X-API-Key: ak_a8f3e7b2c1d9..." https://example.com/asura/api/v1/monitors
```

**Web UI**: Enter the key on the login page and complete its configured two-factor challenge. Sessions default to 24 hours and use an HttpOnly cookie; `auth.session.lifetime` and `auth.session.cookie_secure` configure lifetime and the Secure flag. Keep Secure enabled for HTTPS deployments. Login is rate-limited per IP, and each request resolves the current key's permissions.

## Monitor Settings {#monitor-settings}

| Setting | Default | Description |
| --- | --- | --- |
| `workers` | `10` | Concurrent check workers |
| `default_interval` | `60s` | Default check interval for new monitors |
| `default_timeout` | `10s` | Default per-monitor check timeout, including command checks |
| `adaptive_intervals` | `true` | Dynamically adjust check frequency based on monitor stability |
| `failure_threshold` | `3` | Consecutive failures before opening an incident |
| `success_threshold` | `1` | Consecutive successes before resolving an incident |
| `heartbeat_check_interval` | `30s` | Scan interval for expired heartbeat deadlines |
| `allow_private_targets` | `false` | Permit private/reserved target addresses for server checks |
| `command_allowlist` | `[]` | Allowed command executable paths; empty blocks all command checks |
| `private_smtp_hosts` | `[]` | SMTP host names notification channels may use even when they resolve to private or reserved addresses |
| `docker_sockets` | `[/var/run/docker.sock]` | Clean absolute socket paths Docker checks may use; empty allows only the default socket |

These keys are under `monitor`. Each monitor can override its interval, timeout and incident thresholds. There is no separate command timeout: the worker applies the monitor's timeout to the command's context. Older `command_timeout` and `max_concurrent_dns` YAML keys had no runtime effect and remain ignored.

### Adaptive Intervals {#adaptive-intervals}

When enabled, the scheduler adjusts the effective check interval per monitor based on its recent behavior:

- **Stable monitors** (60+ consecutive successes) gradually slow to 2x their base interval, reducing unnecessary load
- **Flapping monitors** (failure after being slowed down) snap back to 0.5x for faster detection
- **Recovering monitors** return to the normal base interval until stability is re-established
- **Actively failing monitors** stay at the normal base interval

The configured base interval is unchanged; adaptive intervals change the scheduler's effective timing, with a five-second minimum. Disable them with `adaptive_intervals: false`.

## Encryption at Rest {#encryption-at-rest}

Monitor settings, notification-channel settings and proxy passwords can be encrypted in the database with AES-256-GCM. This protects those stored fields; it does not encrypt the entire database or all free-text metadata.

```
database:
  encryption_key: "64-hex-character-key-from-asura---setup"
```

`asura --setup` generates a random key, and the Linux installer and Docker example configure one. The server logs a warning at startup when encryption is off or the key is shorter than 32 characters.

When configured, those fields are encrypted on write and decrypted for authorized internal use. Existing plaintext remains readable and is encrypted on its next update. Encryption derives a key with PBKDF2-HMAC-SHA256 and a persisted salt. Keep the matching encryption key with your database backup. Ordinary API and form reads still redact saved credentials.

## Data Rollup {#data-rollup}

Check results are automatically aggregated into hourly and daily rollups by the retention worker. Chart queries select the appropriate tier based on the requested time range:

- Up to 7 days: raw check results
- Over 7 days through 90 days: hourly aggregates (response-time statistics and status counts)
- Over 90 days: daily aggregates

Hourly rollups are retained for 90 days and daily rollups for one calendar year. A failed rollup pass retains its source history rather than deleting unsummarized checks. Raw-history and request-log retention use these separate settings:

| Setting | Default | Purpose |
| --- | --- | --- |
| `database.retention_days` | `90` | Retain raw checks, resolved incidents and their events, content changes, audit records and notification history |
| `database.request_log_retention_days` | `7` | Retain request history |
| `database.retention_period` | `1h` | Interval between rollup and retention passes; one also runs at startup |

Metrics use verified probe observations. History with an `unknown` source remains available as history but does not enter those calculations.

## Keyboard Shortcuts {#keyboard-shortcuts}

| Key | Action |
| --- | --- |
| `Ctrl+K` / `Cmd+K` | Open search |
| `Escape` | Close modal |

The search shortcut works while typing in a form. To create a monitor from the keyboard, open search, choose **New Monitor**, and press Enter. That action appears only when creation is allowed. Unmodified character keys do not trigger global navigation or search.

## Maintenance Scheduling {#maintenance-scheduling}

Maintenance windows support 6 scheduling modes:

| Mode | Description |
| --- | --- |
| `manual` | Toggle on/off manually via the web UI or API. No schedule. |
| (empty) | One-time window with fixed start and end time. |
| `cron` | Cron expression (minute hour dom month dow). Window duration = end minus start time. |
| `daily` | Same time-of-day every day. |
| `weekly` | Same weekday and time every week. |
| `monthly` | Same day-of-month and time every month. |

During active maintenance, checks and incident state continue while incident announcements are deferred. This does not suppress every possible event type. Manual windows can be toggled via `POST /api/v1/maintenance/{id}/toggle` with `maintenance.toggle` covering every affected monitor. All-monitor windows require a global grant.
