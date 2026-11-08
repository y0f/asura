---
title: "SLA reports"
nav_title: "SLA reports"
description: "Review monthly uptime and error budgets in Asura. Learn how SLA targets, check samples, reports and budget alerts are calculated."
permalink: "/sla.html"
---

# SLA Targets & Reporting {#sla-targets-reporting}

Asura tracks Service Level Agreement (SLA) compliance per monitor. Set a target uptime percentage and Asura calculates your error budget, tracks usage, and alerts when the budget is at risk.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Setting an SLA Target {#setting-an-sla-target}

Edit any monitor and select an SLA target from the dropdown (99%, 99.5%, 99.9%, 99.95%, or 99.99%). Or set it via the API:

```
PUT /api/v1/monitors/{id}
{ "sla_target": 99.9 }
```

Set to `0` to disable SLA tracking for a monitor.

## Error Budget {#error-budget}

The error budget is the maximum allowed downtime in a calendar month. For a 30-day month:

| Target | Monthly Budget |
| --- | --- |
| 99% | ~7h 12m |
| 99.5% | ~3h 36m |
| 99.9% | ~43m 12s |
| 99.95% | ~21m 36s |
| 99.99% | ~4m 19s |

## SLA Report {#sla-report}

The SLA Report page shows all monitors with SLA targets for a given month. Navigate between months and see at a glance which monitors are meeting their targets and which are breached or at risk.

The report is available in the sidebar under "SLA Report" and via the API:

```
GET /api/v1/reports/sla?period=2025-01
```

Export as a JSON download:

```
GET /api/v1/reports/sla/export?period=2025-01
```

## Breach Alerts {#breach-alerts}

After each check, the pipeline evaluates the monitor's current SLA status. When the SLA target is already breached, or 10% or less of the error budget remains, Asura emits an `sla.breach` notification event. This is sent at most once per hour per monitor to avoid flooding. Configure notification channels to receive these alerts.

## Monitor Detail {#monitor-detail}

When an SLA target is configured, the monitor detail page shows an SLA card with:

- Current month uptime vs target
- Error budget used and remaining
- Status badge: Healthy, At Risk, or Breached

## API Endpoints {#api-endpoints}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/monitors/{id}/sla` | Current SLA status and error budget |
| `GET` | `/api/v1/reports/sla?period=YYYY-MM` | Monthly SLA report for all tracked monitors |
| `GET` | `/api/v1/reports/sla/export?period=YYYY-MM` | JSON export of SLA report |
