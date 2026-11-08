---
title: "Configuration"
nav_title: "Configuration"
description: "Configure Asura's server, SQLite or optional PostgreSQL storage, API keys, sessions and monitoring settings. Includes environment variables and deployment options."
permalink: "/configuration.html"
---

# Configuration {#configuration}

Everything lives in `config.yaml`. See [config.example.yaml](https://github.com/y0f/Asura/blob/main/config.example.yaml) for the full reference. Environment variables expand via `${VAR_NAME}`.

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
| `external_url` | auto | Public URL used in notification links |
| `trusted_proxies` | `[]` | IPs/CIDRs whose `X-Real-IP`/`X-Forwarded-For` headers are trusted |
| `rate_limit_per_sec` | `30` | Per-IP request rate limit |
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
  API Key : ak_a8f3e7b2c1d9...
  Hash    : fa223e3e1c4b96...

  Put the hash in config.yaml under auth.api_keys[].hash
  Save the API key -- it cannot be recovered.
```

Generated keys have an `ak_` prefix. Treat the whole key as a credential. If you already have a key and need its hash:

```
./asura --hash-key "your-secret-key"
```

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
| Legacy `role: admin` | Compatibility mapping to explicit super-administrator access |
| Legacy `role: readonly` | A frozen global read set that excludes subscriber contacts and does not grow with new resources |

Use `auth.roles` for custom reusable roles and `api_keys[].role_bindings` for explicit global, group or monitor scopes. See [Access control](access-control.html) for complete YAML examples, built-in actions and legacy compatibility. Ordinary roles cannot grant `super_admin`.

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

`diagnostics.read` supports monitor/group scope and must be granted explicitly for raw check output, incident causes/timeline text and assertion/extraction definitions. It is absent from the built-in viewer/operator roles and the frozen legacy readonly role; administrator includes it. It never grants ordinary monitor or incident reads by itself. See [Diagnostic access](access-control.html#diagnostic-access).

### Upgrading permissions from v1 {#upgrading-permissions-from-v1}

V2 separates resources that previously shared a scope. Existing explicit permissions are not expanded automatically. Retain the scopes you need and add the following scopes only for the corresponding features:

| Previous scope | Additional v2 scopes for previously shared features |
| --- | --- |
| `monitors.read` | `groups.read`, `tags.read`, `proxies.read`, `agents.read`, `status_pages.read`, `sla.read`. Subscriber listing now requires the separate sensitive grant `subscribers.read`. |
| `monitors.write` | `groups.write`, `tags.write`, `proxies.write`, `agents.write`, `status_pages.write`, `subscribers.delete`, `database.maintain`. Import and export require super administrator access. |
| `notifications.read` | `notification_history.read`, `on_call.read` |
| `notifications.write` | `on_call.write` |
| `metrics.read` | `request_logs.read`, `audit.read`, `settings.read`, `database.read` |

Existing `incidents`, `maintenance`, and `escalation_policies` read/write grants remain within their own resources. Replace a write umbrella with individual actions when a key should only perform part of the workflow. A key needs access to at least one application page to start a web session; API-only permissions such as `metrics.read` still work through the API. Session requests recheck the key's current permissions.

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
| `default_timeout` | `10s` | Default check timeout |
| `adaptive_intervals` | `true` | Dynamically adjust check frequency based on monitor stability |

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
  encryption_key: "your-secret-key-here"
```

When configured, those fields are encrypted on write and decrypted for authorized internal use. Existing plaintext remains readable and is encrypted on its next update. New encryption derives a key with PBKDF2-HMAC-SHA256 and a persisted salt; legacy SHA-256-derived ciphertext remains readable and is rewritten on update. Keep the matching encryption key with your database backup. Ordinary API and form reads still redact saved credentials.

## Data Rollup {#data-rollup}

Check results are automatically aggregated into hourly and daily rollups by the retention worker. Chart queries select the appropriate tier based on the requested time range:

- Up to 7 days: raw check results
- Over 7 days through 90 days: hourly aggregates (response-time statistics and status counts)
- Over 90 days: daily aggregates

Hourly rollups are retained for 90 days and daily rollups for one calendar year. Raw-history and request-log retention are configured separately. A failed rollup pass retains its source history rather than deleting unsummarized checks.

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
