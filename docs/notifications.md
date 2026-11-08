---
title: "Notifications"
nav_title: "Notifications"
description: "Send Asura uptime alerts through email, webhooks and messaging services. Configure notification channels, event filters and delivery settings."
permalink: "/notifications.html"
---

# Notifications {#notifications}

Asura supports 13 notification channel types. Each channel can filter event types and receive events from selected monitors. Notification history records the final per-channel delivery outcome rather than a separate row for every retry.

Events: `incident.created`, `incident.acknowledged`, `incident.resolved`, `incident.reminder`, `incident.escalated`, `content.changed`, `cert.changed`, `sla.breach`, `maintenance.started`, `maintenance.ended`. Maintenance events are sent when a maintenance window is toggled manually in the web interface.

The `incident.reminder` event fires when a monitor's `resend_interval` elapses while an incident is still open, re-alerting channels that subscribe to it.

A channel with no event filter set receives **all** event types. To limit a channel to specific events, set its event list — only the listed events are then delivered.

In the web form, choose **All events** explicitly or select at least one event. Editing a channel preserves its existing selection, including escalation and SLA alerts. Choosing all events also includes event types added in future versions.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Diagnostic data in deliveries {#diagnostic-data-in-deliveries}

Automatic notification channels and public status-page subscriber deliveries use fixed incident summaries instead of raw checker errors. Content-change payloads retain identifiers and timestamps but contain no response bodies, hashes or diffs. Monitor payloads omit assertion definitions and mask private request fields. Changing a notification destination therefore does not grant access to diagnostic evidence from other monitors.

The original evidence remains stored and can be inspected through the authenticated API and web interface with the matching resource read permission and scoped `diagnostics.read`. There is no automatic raw-diagnostic delivery option. Notification history exposes a generic delivery failure instead of provider error text, which can contain credentials or private URLs.

HTTP notification channels and subscriber webhooks require a successful 2xx response.
They do not follow redirects: provider credentials and signed payloads stay at the
configured endpoint. Configure the final HTTPS URL directly; a redirect response is
recorded as a failed delivery.

## Webhook {#webhook}

```
{
  "type": "webhook",
  "settings": {
    "url": "https://example.com/hook",
    "secret": "hmac-secret"
  }
}
```

When a `secret` is set, Asura signs every payload with HMAC-SHA256 and sends the signature in the `X-Asura-Signature` header as `sha256=<hex>`.

### Webhook Signing {#webhook-signing}

To verify a webhook delivery:

1. Read the raw request body
2. Compute `HMAC-SHA256(secret, body)`
3. Compare with the hex value after `sha256=` in the `X-Asura-Signature` header

## Email (SMTP) {#email-smtp}

```
{
  "type": "email",
  "settings": {
    "host": "smtp.example.com",
    "port": 587,
    "username": "alerts@example.com",
    "password": "...",
    "from": "alerts@example.com",
    "to": ["ops@example.com", "oncall@example.com"],
    "cc": ["manager@example.com"],
    "bcc": ["archive@example.com"],
    "tls_mode": "starttls"
  }
}
```

`tls_mode`: `starttls` (default; upgrades the plain connection with STARTTLS when the server offers it and otherwise sends in plaintext, as an empty value does), `starttls_required` (upgrades with STARTTLS and fails instead of sending in plaintext when the server does not offer it, which protects against an attacker stripping STARTTLS), `smtps` (TLS from the start, typically port 465), or `none` (plaintext). The same values apply to `subscriptions.smtp.tls_mode` in `config.yaml`. Mail servers on localhost or a private network are allowed; `allow_private_targets` does not apply to SMTP. Each delivery attempt, including the whole SMTP conversation, has a time limit, so a mail server that stops responding cannot block other notifications. Emails are sent as HTML; no separate plain-text alternative is included.

## Telegram {#telegram}

```
{
  "type": "telegram",
  "settings": {
    "bot_token": "123456:ABC-DEF...",
    "chat_id": "-1001234567890"
  }
}
```

## Discord {#discord}

```
{
  "type": "discord",
  "settings": {
    "webhook_url": "https://discord.com/api/webhooks/..."
  }
}
```

## Slack {#slack}

```
{
  "type": "slack",
  "settings": {
    "webhook_url": "https://hooks.slack.com/services/...",
    "channel": "#alerts"
  }
}
```

## ntfy {#ntfy}

```
{
  "type": "ntfy",
  "settings": {
    "topic": "my-alerts",
    "server_url": "https://ntfy.sh",
    "priority": 4,
    "tags": "warning"
  }
}
```

`server_url` defaults to `https://ntfy.sh` if omitted. `priority` (1-5), `tags`, and `click_url` are optional.

## Microsoft Teams {#microsoft-teams}

```
{
  "type": "teams",
  "settings": {
    "webhook_url": "https://outlook.office.com/webhook/..."
  }
}
```

Sends Adaptive Cards with color-coded severity (Attention for alerts, Warning for acknowledged, Good for resolved).

## PagerDuty {#pagerduty}

```
{
  "type": "pagerduty",
  "settings": {
    "routing_key": "your-events-api-v2-key"
  }
}
```

Uses the Events API v2. Incident lifecycle maps to PagerDuty actions: `incident.created` triggers an alert, `incident.acknowledged` acknowledges it, and `incident.resolved` resolves it. Dedup keys are generated per incident for proper correlation.

## Opsgenie {#opsgenie}

```
{
  "type": "opsgenie",
  "settings": {
    "api_key": "your-api-key",
    "region": "eu"
  }
}
```

`region` is optional. set to `"eu"` for the EU instance. Incident lifecycle maps to Opsgenie actions: create, acknowledge, and close. Priority is P1 for new incidents and P3 for reminders.

## Pushover {#pushover}

```
{
  "type": "pushover",
  "settings": {
    "user_key": "your-user-key",
    "app_token": "your-app-token",
    "priority": 0,
    "sound": "pushover",
    "device": ""
  }
}
```

An omitted or zero `priority` selects the event-derived priority (1=high for created, 0=normal for reminders, -1=low for resolved). A nonzero value overrides that selection. `sound` and `device` are optional.

## Google Chat {#google-chat}

```
{
  "type": "googlechat",
  "settings": {
    "webhook_url": "https://chat.googleapis.com/v1/spaces/..."
  }
}
```

## Matrix {#matrix}

```
{
  "type": "matrix",
  "settings": {
    "homeserver": "https://matrix.example.com",
    "access_token": "syt_...",
    "room_id": "!roomid:example.com"
  }
}
```

## Gotify {#gotify}

```
{
  "type": "gotify",
  "settings": {
    "server_url": "https://gotify.example.com",
    "app_token": "your-app-token",
    "priority": 5
  }
}
```

`priority` is optional (default: 5). Higher values show as more urgent in Gotify.

## Per-Monitor Routing {#per-monitor-routing}

Without an explicit channel assignment, monitor events are offered to every enabled channel whose event filter matches. Set `notification_channel_ids` to restrict ordinary monitor notifications to selected channels; their enabled state and event filters still apply. Explicit escalation recipients follow their policy instead.

Changing this assignment requires `monitors.update` and `monitors.assign` on the monitor, plus `notifications.read`. Retrieve the current configuration with `GET /api/v1/monitors/{id}` using `monitors.read`, then prepare a complete monitor configuration with the desired `notification_channel_ids`, such as `[1, 3]`. A body containing only that relationship is not a valid monitor `PUT`; see the [complete configuration example](sla.html#setting-an-sla-target).

Save the edited configuration as `monitor.json` and submit it:

```
curl --fail --show-error -X PUT https://example.com/asura/api/v1/monitors/1 \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  --data-binary @monitor.json
```

Omit unrelated relationship fields to retain their current assignments atomically. Saved private values follow the [credential-preservation rules](api.html#stored-credentials). For a heartbeat GET response, use the nested `monitor` configuration, not the outer response envelope.

An explicit empty `notification_channel_ids` array (`[]`) removes the restriction and offers events to all matching enabled channels. Omitting the field preserves the current assignment.

## Notification History {#notification-history}

History records each final channel outcome as sent or failed, with its event and associated monitor. Retries do not create separate rows. Persistence failures are logged, so history is not a durable delivery ledger. History follows the configured retention policy, and access is independently controlled by `notification_history.read`; monitor and channel names require their corresponding read permissions.

```
GET /api/v1/notifications/history?channel_id=1&status=failed&event_type=incident.created&page=1
```

Filters: `channel_id`, `status` (`sent` / `failed`), `event_type`. The web UI has a History page linked from Notification Channels.

## Delivery and shutdown {#delivery-and-shutdown}

The notifier uses 10 workers and a bounded queue of 100 jobs. Queue admission waits at most 100 milliseconds per job and respects caller cancellation. Ordinary channel delivery makes up to three attempts, each with a 10-second timeout, with cancelable 2-second and 4-second delays between attempts. A channel test makes one 10-second attempt and follows the test request's cancellation.

Subscriber delivery has a 30-second limit; confirmation mail has a 60-second limit. A successful email subscription response means its confirmation was queued. If mail is unavailable or the queue cannot accept it, the request returns an error so the visitor can retry.

Shutdown stops producers and drains queued deliveries before closing the database, subject to the shutdown deadline. Queue saturation and exhausted retries are explicit failures. The queue is in memory: delivery is not durable or guaranteed exactly once across process failure.
