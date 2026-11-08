---
title: "Status pages"
nav_title: "Status pages"
description: "Publish service availability with Asura status pages. Configure monitor groups, branding, password protection and subscriber notifications."
permalink: "/status-pages.html"
---

# Status Pages {#status-pages}

Status pages share the current and historical health of selected services. Each page has its own URL, optional password protection and 90-day uptime bars with an accessible daily-data table. Only enabled assigned monitors appear publicly.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Creating a Status Page {#creating-a-status-page}

Go to **Status Pages → New Status Page** in the sidebar. The minimum required fields are a title and slug.

| Field | Description |
| --- | --- |
| `title` | Display name shown in the page header |
| `slug` | URL path segment. Lowercase letters, numbers, and hyphens. The page is served at `/{slug}` |
| `description` | Short subtitle shown below the title |
| `enabled` | When off, the public page returns 404. The admin API still works. |
| `api_enabled` | "Public JSON API". Also serves the public JSON endpoint at `/api/v1/status-pages/{id}/public` while `enabled` is off, and allows subscribing through the API (which also needs `enabled`). An enabled page serves its JSON whether or not this is on. |
| `show_incidents` | Display recent incidents below the monitor list |
| `sort_order` | Controls ordering in the admin list |

## Monitor Assignment {#monitor-assignment}

Page administration and data publication are separate permissions. Creating or updating a page requires `status_pages.create` or `status_pages.update`. Assigning, removing or reordering members also requires `monitors.read` for every affected monitor. Publishing incidents requires `incidents.read` independently for those same monitors.

Enabling a page or its JSON API, changing its public URL, changing or removing an existing password, or enabling incident display checks the required read permissions for every current member. These checks and the metadata, password and membership changes commit in one transaction. Metadata-only edits preserve omitted membership. Turning incident display off does not require incident read access. The edit form hides selection when the editor cannot read every current member's required data.

Select which monitors appear on the page. When a monitor is checked, two additional fields appear:

| Field | Description |
| --- | --- |
| **Sort order** | Integer controlling position within the page (lower = higher) |
| **Group name** | Optional label. Monitors with the same group name are grouped under a shared heading on the public page |

## Branding {#branding}

Expand **Branding and access** to customise the public page. Public image loading permits only the exact configured HTTPS origins; authenticated application pages keep their existing resource policy. Public pages send `Referrer-Policy: no-referrer`.

| Field | Description |
| --- | --- |
| `logo_url` | Image above the status banner. Use HTTPS without embedded credentials, or a path starting with `/`. Protocol-relative URLs and wildcard hosts are rejected. |
| `favicon_url` | Favicon with the same URL rules. Defaults to the Asura favicon. |
| `custom_header_html` | Supported structural HTML, text, links and images before the banner, up to 5,000 characters. Scripts, forms, event handlers and unsupported attributes are removed, including when legacy content is rendered. |
| `custom_css` | Presentation CSS capped at 10,000 characters. New saves keep allowlisted properties and remove external resources, comments, escapes and at-rules. Legacy CSS also has an HTML raw-text boundary guard when rendered. |
| `analytics_script` | Only a super administrator may change this field. Supported external HTTPS script tags can be stored, but external script execution remains blocked by the current content policy. Inline script bodies are removed. Other editors preserve omitted or unchanged stored values. |

## Password Protection {#password-protection}

Set a password under **Branding and access**. Visitors are redirected to `/{slug}/auth` and receive a 7-day `HttpOnly` cookie on success. Branding and page contents are not rendered before successful authentication.

- Newly set or reset passwords use PBKDF2-HMAC-SHA256 with 600,000 iterations and a random 16-byte salt; plaintext is never stored. New passwords are limited to 1,024 UTF-8 bytes without truncation.
- Existing lowercase SHA-256 password hashes remain valid, including legacy longer passwords. Explicitly reset a legacy password to upgrade it; login does not rewrite stored protection. Full configuration transfer preserves supported opaque hashes.
- Password digests use constant-time comparison.
- Auth attempts are rate-limited per client IP using the same limiter as admin login; wrong passwords sent to the JSON endpoint in the `X-Status-Page-Password` header count against it too
- Leave the field blank when editing to keep the existing password unchanged
- Tick **Remove password protection** to clear the password and make the page publicly accessible

## Public JSON API {#public-json-api}

Asura serves a machine-readable endpoint for every page that has `enabled` or `api_enabled` on; it returns 404 only when both are off. Turning `api_enabled` off therefore does not hide the JSON of an enabled page; to hide it, turn off both.

```
GET /api/v1/status-pages/{id}/public
```

No API key is required. A password-protected page requires its password in the `X-Status-Page-Password` header, or the cookie set by its login form; without either, the endpoint returns 401. Wrong header passwords are rate-limited per client IP (429 once the limit is reached). Unprotected JSON responses use `Cache-Control: public, max-age=30`; protected responses use `no-store`.

```
{
  "page": {
    "title": "Service Status",
    "description": "Current status of our services"
  },
  "overall_status": "operational",
  "monitors": [
    {
      "id": 1,
      "name": "API",
      "type": "http",
      "status": "up",
      "uptime_90d": 99.94,
      "total_checks": 129600,
      "daily_uptime": [
        { "date": "2025-01-01", "uptime_pct": 100, "up_checks": 1440, "down_checks": 0, "total_checks": 1440 },
        ...
      ]
    }
  ],
  "incidents": [
    {
      "id": 3,
      "monitor_name": "API",
      "status": "resolved",
      "cause": "",
      "started_at": "2025-01-15T14:22:00Z",
      "resolved_at": "2025-01-15T14:35:00Z"
    }
  ]
}
```

The `cause` field is always returned empty in the public payload — incident causes are never exposed publicly.

`overall_status` is `operational`, `degraded`, `major_outage` or `unknown`. Empty pages and sets containing pending checks are unknown unless a known degraded or down state takes priority. The page displays “Awaiting status data” for unknown status.

In v2, a monitor with no samples has `uptime_90d: null` and `total_checks: 0`, rather than an assumed 100%. A measured uptime remains numeric. Query failures return HTTP 500 instead of a healthy or empty result. Public monitor queries do not load private targets, settings or credentials.

## Admin REST API {#admin-rest-api}

All endpoints require authentication and the corresponding `status_pages.read`, `create`, `update` or `delete` action. Member lists include only monitors the caller can read. Successful create/update responses contain only the page ID when the caller lacks `status_pages.read`.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/api/v1/status-pages` | List all status pages |
| `POST` | `/api/v1/status-pages` | Create a status page |
| `GET` | `/api/v1/status-pages/{id}` | Get a status page with its monitor list |
| `PUT` | `/api/v1/status-pages/{id}` | Update a status page |
| `DELETE` | `/api/v1/status-pages/{id}` | Delete a status page |

### Create / Update body {#create-update-body}

```
{
  "title": "Service Status",
  "slug": "status",
  "description": "Current status of our services",
  "enabled": true,
  "api_enabled": false,
  "show_incidents": true,
  "sort_order": 0,
  "logo_url": "https://example.com/logo.png",
  "favicon_url": "https://example.com/favicon.ico",
  "custom_header_html": "",
  "custom_css": "",
  "analytics_script": "",
  "password": "optional-plain-text-password",
  "monitors": [
    { "monitor_id": 1, "sort_order": 0, "group_name": "Core" },
    { "monitor_id": 2, "sort_order": 1, "group_name": "Core" },
    { "monitor_id": 3, "sort_order": 0, "group_name": "Third-party" }
  ]
}
```

On `PUT`, omitted metadata, `password` and `monitors` preserve their current values. Send `"password": ""` to remove password protection, or `"monitors": []` to clear membership with the required read permissions. `clear_password` is a web-form control, not an API field.

## Subscriptions {#subscriptions}

Visitors can subscribe to status page updates via email or webhook. Email subscribers go through double opt-in confirmation. Webhook subscribers are confirmed immediately.

Subscribers are notified when incidents are created or resolved for monitors on the status page. The subscribe widget appears when subscriptions are enabled in the server config. Setting `subscriptions.enabled: false` rejects all new web and API registrations with HTTP 503; existing subscriptions remain stored and unsubscribe links remain available. When enabled, webhook subscriptions need no SMTP configuration; email subscriptions require a working confirmation-email transport.

| Method | Endpoint | Description |
| --- | --- | --- |
| `POST` | `/api/v1/status-pages/{id}/subscribe` | Subscribe (email or webhook) |
| `GET` | `/api/v1/status-pages/{id}/subscribers` | Confirmed subscriber count and contact records; webhook URLs are redacted; requires `subscribers.read` |
| `DELETE` | `/api/v1/status-pages/{id}/subscribers/{subId}` | Remove subscriber; requires `subscribers.delete` |

The HTML page, public JSON and RSS feed read the page's current visibility,
password protection, members and displayed data from one database snapshot.
Subscription creation checks the same current page authority in its write
transaction. Confirmation and unsubscribe tokens work only for their own page;
existing subscribers can still unsubscribe after a page is disabled.

Web and JSON subscription forms use the same destination limits: email addresses
up to 254 bytes and HTTPS webhook URLs up to 2,048 bytes. Private webhook targets
require the server's explicit private-target setting; delivery rechecks addresses
when connecting. Token-result pages do not reveal private page branding.

Enable subscriptions by adding an SMTP config to your `config.yaml`:

```
subscriptions:
  enabled: true
  smtp:
    host: "smtp.example.com"
    port: 587
    from: "status@example.com"
    tls_mode: "starttls"
```
