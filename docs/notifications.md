---
title: "Notifications"
nav_title: "Notifications"
description: "Send Asura uptime alerts through email, webhooks and messaging services. Configure notification channels, event filters and delivery settings."
permalink: "/notifications.html"
---

# Notifications {#notifications}

13 notification channels. Each one can be scoped to specific event types and routed to specific monitors. All delivery attempts are recorded in notification history.

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

`tls_mode`: `starttls` (default; upgrades the plain connection with STARTTLS when the server offers it and otherwise sends in plaintext, as an empty value does), `starttls_required` (upgrades with STARTTLS and fails instead of sending in plaintext when the server does not offer it, which protects against an attacker stripping STARTTLS), `smtps` (TLS from the start, typically port 465), or `none` (plaintext). The same values apply to `subscriptions.smtp.tls_mode` in `config.yaml`. Mail servers on localhost or a private network are allowed; `allow_private_targets` does not apply to SMTP. Each delivery attempt, including the whole SMTP conversation, has a time limit, so a mail server that stops responding cannot block other notifications. Emails are sent as HTML with a plain-text fallback.

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

`priority` defaults to auto-select based on event type (1=high for created, 0=normal for reminders, -1=low for resolved). Set explicitly to override. `sound` and `device` are optional.

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

By default every channel gets every event. To limit which channels a monitor sends to, set `notification_channel_ids`:

```
curl -X PUT https://example.com/asura/api/v1/monitors/1 \
  -H "X-API-Key: $KEY" \
  -H "Content-Type: application/json" \
  -d '{"notification_channel_ids": [1, 3]}'
```

An empty array (`[]`) means the monitor sends to all channels.

## Notification History {#notification-history}

Every delivery attempt is recorded (sent or failed) with the channel, event type, monitor, and error message if any. History is pruned with the same retention window as check results.

```
GET /api/v1/notifications/history?channel_id=1&status=failed&event_type=incident.created&page=1
```

Filters: `channel_id`, `status` (`sent` / `failed`), `event_type`. The web UI has a History page linked from Notification Channels.
