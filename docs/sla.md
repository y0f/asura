---
title: "SLA reports"
nav_title: "SLA reports"
description: "Review monthly uptime and error budgets in Asura. Learn how SLA targets, check samples, reports and budget alerts are calculated."
permalink: "/sla.html"
---

# SLA Targets & Reporting {#sla-targets-reporting}

Asura reports uptime against a configured target and estimates the remaining monthly error budget. These reports describe Asura's recorded observations; they do not establish an external service-level agreement or guarantee.

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

Set to `0` to disable SLA tracking. The API accepts a target greater than 0 and at most 100.

## Error Budget {#error-budget}

The error budget is the allowed downtime in a UTC calendar month. For a 30-day month:

| Target | Monthly Budget |
| --- | --- |
| 99% | ~7h 12m |
| 99.5% | ~3h 36m |
| 99.9% | ~43m 12s |
| 99.95% | ~21m 36s |
| 99.99% | ~4m 19s |

Uptime is calculated from counts of recorded up checks divided by all recorded checks, including degraded and down results. Days are weighted by their check counts, not averaged equally. Daily rollups and uncovered raw history are combined without counting the same samples twice.

Budget usage estimates downtime by multiplying the non-up sample fraction by elapsed time in the selected month. For the current month, elapsed time stops at now; completed months use their full duration. This is a sample-based estimate, not a reconstruction of every outage's exact duration. Paused or unobserved periods do not create synthetic healthy samples.

Manual monitors currently record samples when their status is changed, rather than through periodic probes. Heartbeat pings and expiry update state without creating check samples. Their sample-based reports therefore do not measure elapsed time in each state; a heartbeat can have a known current status and still have no uptime samples.

Budget comparisons use the configured decimal target and exact sample counts. Whole-second display rounding does not decide whether a budget is breached or at risk. A 100% target with no measured downtime remains healthy despite its zero allowance.

## SLA Report {#sla-report}

The SLA Report page shows monitors within the caller's `sla.read` scope that have targets for the selected month. Monitor names and detail links separately require `monitors.read`. Periods use `YYYY-MM`; invalid and future months are rejected.

The report is available in the sidebar under "SLA Report" and via the API:

```
GET /api/v1/reports/sla?period=2025-01
```

Export as a JSON download:

```
GET /api/v1/reports/sla/export?period=2025-01
```

## Breach Alerts {#breach-alerts}

Checks and a periodic reconciliation evaluate enabled monitors' current-month budgets, with evaluation throttled to once per minute per unchanged target and month. A breached budget or at most 10% of a positive allowance remaining can emit `sla.breach`. No samples, disabled tracking, maintenance, or an unreadable suppression state produce no alert.

The same or lower alert level is suppressed for one hour after successful queue admission. A rise from at-risk to breached, a changed target or a new month can alert sooner. Configure notification channels to include `sla.breach`; delivery follows the bounded retry behavior described in [Notifications](notifications.html#delivery-and-shutdown).

## Monitor Detail {#monitor-detail}

When an SLA target is configured, the monitor detail page shows an SLA card with:

- Current month uptime vs target
- Error budget used and remaining
- Status label: No checks, Healthy, At risk, or Breached

No checks means no measured uptime is available; the UI shows an em dash instead of treating it as observed 100%. API consumers should inspect `total_checks` before interpreting numeric uptime and use `breached` and `budget_at_risk` rather than recomputing decisions from rounded display values.

## API Endpoints {#api-endpoints}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/monitors/{id}/sla` | Current SLA status and error budget |
| `GET` | `/api/v1/reports/sla?period=YYYY-MM` | Monthly SLA report for all tracked monitors |
| `GET` | `/api/v1/reports/sla/export?period=YYYY-MM` | JSON export of SLA report |
