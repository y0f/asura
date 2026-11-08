---
title: "Configuration"
nav_title: "Configuration"
description: "Configure Asura's server, SQLite storage, API keys, sessions and monitoring settings. Includes environment variables and deployment options."
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
| `web_ui_enabled` | `true` | Serve the web dashboard. Set `false` to run in API-only mode. all REST endpoints remain available |

## Authentication {#authentication}

API keys are hashed with SHA-256 and stored in `config.yaml`. There's no user registration and no database-stored auth.

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

The `ak_` prefix makes keys easy to identify in logs without leaking the secret. If you already have a key and just need the hash:

```
./asura --hash-key "your-secret-key"
```

### Config {#config}

```
auth:
  api_keys:
    - name: "admin"
      hash: "2c26b46b68ffc..."
      role: "admin"
```

### Roles {#roles}

| Role | Access |
| --- | --- |
| `admin` | Super administrator access, including configuration import and export |
| `readonly` | All registered read scopes except subscriber contacts (`subscribers.read`) |

### Custom Permissions {#custom-permissions}

Instead of a role, grant specific permissions per key:

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

Cloning requires both `monitors.read` and `monitors.clone`. Editing an existing item through a form requires its read and update scopes. Bulk operations require the selected action; assigning a group also requires `monitors.assign` and `groups.read`. Configuration import and export require `super_admin: true` (or the legacy `admin` role), even when every ordinary permission is granted. Subscriber contact details require an explicit `subscribers.read` grant.

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

**Web UI**: Enter the key on the login page. Asura creates a session cookie (24h, HttpOnly, Secure). Login is rate-limited per IP. You can have multiple keys with different roles or permission sets.

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

The base interval on each monitor is never modified. adaptive intervals only change the scheduler's internal timing. Disable with `adaptive_intervals: false`.

## Encryption at Rest {#encryption-at-rest}

Notification channel settings (tokens, passwords, webhook secrets) and proxy auth passwords can be encrypted in the database using AES-256-GCM.

```
database:
  encryption_key: "your-secret-key-here"
```

When set, secrets are encrypted on write and decrypted on read. Existing unencrypted data is read transparently and encrypted on the next update. The key is hashed with SHA-256 to derive the AES key.

## Data Rollup {#data-rollup}

Check results are automatically aggregated into hourly and daily rollups by the retention worker. Chart queries select the appropriate tier based on the requested time range:

- Less than 7 days: raw check results
- 7 to 90 days: hourly aggregates (avg/min/max/p95 response time, up/down counts)
- Over 90 days: daily aggregates

Hourly rollups are retained for 90 days, daily rollups for 365 days. This keeps the database size manageable for long-running instances.

## Keyboard Shortcuts {#keyboard-shortcuts}

| Key | Action |
| --- | --- |
| `n` | New monitor |
| `/` | Focus search |
| `Escape` | Close modal |
| `?` | Show shortcut help |

Shortcuts are disabled when typing in form fields.

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

During active maintenance, alerts are suppressed for affected monitors. Manual windows can be toggled via `POST /api/v1/maintenance/{id}/toggle`.
