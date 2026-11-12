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

Edit an active-check type, such as HTTP, and enter its **SLA target (%)**. Here, scheduled monitors means probe-based types, including ones currently paused. The numeric field accepts custom decimals and `100`, with suggestions for common targets. Use `0` to disable tracking.

API updates require `monitors.update` and a complete monitor configuration, not a partial `{ "sla_target": 99.9 }` patch. Retrieve the current configuration with `GET /api/v1/monitors/{id}` using `monitors.read`, preserve its configuration fields, and change `sla_target` in the `PUT` body. For example, an existing HTTP health check could use:

```
PUT /api/v1/monitors/{id}
{
  "name": "Public API",
  "description": "Public health endpoint",
  "type": "http",
  "target": "https://api.example.com/health",
  "interval": 60,
  "timeout": 10,
  "enabled": true,
  "failure_threshold": 3,
  "success_threshold": 1,
  "settings": { "method": "GET", "expected_status": 200 },
  "sla_target": 99.9
}
```

Adapt the values to the existing monitor rather than replacing unrelated configuration with the example. Omitted relationships are preserved atomically; saved private fields follow the [credential-preservation rules](api.html#stored-credentials). The API accepts a finite target from `0` through `100`.

Manual and heartbeat monitors do not calculate sampled uptime or SLA budgets. Existing targets on those types remain stored but inactive. Unrelated form edits preserve them; **Clear inactive target** removes one explicitly. Before changing a scheduled monitor to either passive type, set its target to `0`.

## Error Budget {#error-budget}

The error budget is the allowed downtime in a UTC calendar month. For a 30-day month:

| Target | Monthly Budget |
| --- | --- |
| 99% | ~7h 12m |
| 99.5% | ~3h 36m |
| 99.9% | ~43m 12s |
| 99.95% | ~21m 36s |
| 99.99% | ~4m 19s |

Uptime is calculated from verified probe observations: up probes divided by all probes, including degraded and down results. Days are weighted by their probe counts, not averaged equally. Daily rollups and uncovered raw history are combined without counting the same samples twice.

Budget usage estimates downtime by multiplying the non-up sample fraction by elapsed time in the selected month. For the current month, elapsed time stops at now; completed months use their full duration. This is a sample-based estimate, not a reconstruction of every outage's exact duration. Paused or unobserved periods do not create synthetic healthy samples.

Manual state changes and heartbeat state updates are not periodic probes and do not enter these calculations. Observations with an `unknown` source keep their history but cannot establish verified uptime, latency or SLA values.

Budget comparisons use the configured decimal target and exact sample counts. Whole-second display rounding does not decide whether a budget is breached or at risk. A 100% target with no measured downtime remains healthy despite its zero allowance.

## SLA Report {#sla-report}

The SLA Report page shows scheduled monitors within the caller's `sla.read` scope that have targets for the selected month. Manual and heartbeat monitors are excluded. Monitor names and detail links separately require `monitors.read`. Periods use `YYYY-MM`; invalid and future months are rejected.

The report is available in the sidebar under "SLA Report" and via the API:

```
GET /api/v1/reports/sla?period=2025-01
```

Export as a JSON download:

```
GET /api/v1/reports/sla/export?period=2025-01
```

## Breach Alerts {#breach-alerts}

Checks and a periodic reconciliation evaluate enabled scheduled monitors' current-month budgets, with evaluation throttled to once per minute per unchanged target and month. A breached budget or at most 10% of a positive allowance remaining can emit `sla.breach`. No verified samples, disabled tracking, maintenance, or an unreadable suppression state produce no alert. Passive monitors do not emit SLA alerts.

The same or lower alert level is suppressed for one hour after successful queue admission. A rise from at-risk to breached, a changed target or a new month can alert sooner. Configure notification channels to include `sla.breach`; delivery follows the bounded retry behavior described in [Notifications](notifications.html#delivery-and-shutdown).

## Monitor Detail {#monitor-detail}

When a scheduled monitor has an SLA target and the reader has `sla.read`, its detail page shows an SLA card with:

- Current month uptime vs target
- Error budget used and remaining
- Status label: No checks, Healthy, At risk, or Breached

No checks means no verified uptime is available; the UI shows an em dash instead of observed 100%. With no monthly probes, API responses have `has_data: false`, `total_checks: 0`, and `null` for monthly uptime, consumed/remaining budget and compliance decisions (`breached` and `budget_at_risk`). The target and theoretical allowance remain available. Rolling 30-day uptime independently becomes `null` when `total_checks_30d` is zero. Report entries use the same nullable no-data semantics for their selected month.

When data exists, use the supplied `breached` and `budget_at_risk` decisions rather than recomputing them from rounded display values. A direct SLA request for a manual or heartbeat monitor returns HTTP 400 because sampled SLA is unavailable.

## API Endpoints {#api-endpoints}

| Method | Endpoint | Description |
| --- | --- | --- |
| `GET` | `/api/v1/monitors/{id}/sla` | Current SLA status and error budget |
| `GET` | `/api/v1/reports/sla?period=YYYY-MM` | Monthly SLA report for all tracked monitors |
| `GET` | `/api/v1/reports/sla/export?period=YYYY-MM` | JSON export of SLA report |
