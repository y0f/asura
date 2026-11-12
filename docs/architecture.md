---
title: "Architecture"
nav_title: "Architecture"
description: "Explore Asura's Go server, scheduler, probe agents, SQLite storage and embedded web interface. Understand its runtime and data flow."
permalink: "/architecture.html"
---

# Architecture {#architecture}

Asura's Go server combines scheduling, checks, incident processing, notifications and an embedded web interface. SQLite is the default local storage. Bounded queues connect background workers; request handlers and storage operations use explicit calls and transactions.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Pipeline {#pipeline}

```
Scheduler ──▶ Worker Pool ──▶ Result Processor ──▶ Dispatcher
    │              │                 │                   │
 Min-heap      Concurrent       Incidents +        Webhook/Email
 dispatch       Checks         Change Diffs      Telegram/Discord
                                                   Slack/ntfy/Teams
                                                  PagerDuty/Opsgenie
                                                  Pushover/Gotify
                                               Google Chat/Matrix
```

## Scheduler {#scheduler}

A min-heap priority queue ordered by next-due time. Each tick, the scheduler pops all monitors that are due and dispatches them to the worker pool. Dispatch is **O(log n)** per monitor.

When **adaptive intervals** are enabled, the scheduler adjusts timing per monitor:

- After 60 consecutive successes, intervals increase in 1.25x steps up to twice the configured interval.
- A failure after a slowdown schedules the next check at half the base interval.
- Continuing failures or a recent recovery use the base interval, with a five-second minimum.

Only one local check per monitor can be queued or in flight. A full worker queue skips that dispatch and records the saturation; it does not block the scheduler indefinitely. Disabling or reloading a monitor does not create a second in-flight check. Manual and heartbeat monitors are handled separately from active protocol checks.

## Worker Pool {#worker-pool}

A fixed-size goroutine pool (default 10, configured with `monitor.workers`) consumes queued checks, invokes the protocol checker and measures response time. Assertion evaluation, state transitions and incident thresholds happen in result processing. Remote agents run only explicitly delegated, eligible jobs and must return the job's current configuration revision.

## Result Processor {#result-processor}

Receives check results from the worker pool. For each result:

1. Validates the result against the current monitor configuration and evaluates assertions.
2. Commits check history and the corresponding monitor state together. Obsolete or revoked agent jobs are rejected before those writes.
3. Processes incident thresholds and recovery, and computes tracked content changes.
4. Publishes authorized live-event signals and queues notifications after successful persistence.

State locks serialize competing transitions for a monitor. Persistence failures are surfaced or logged rather than committing an in-memory status first. Maintenance keeps checks and incident state current while deferring incident announcements; it does not erase an outage.

## Dispatcher {#dispatcher}

Routes events through 13 channel senders implementing the `Sender` interface; see [Notifications](notifications.html) for providers and settings. A monitor's `notification_channel_ids` restrict delivery to those channels; otherwise all matching channels receive the event.

Each notifier owns a bounded 100-job queue and 10 delivery workers. Admission and sends are cancelable, retries are bounded, and shutdown joins delivery before closing storage. The queue is not durable across process failure. See [delivery behavior](notifications.html#delivery-and-shutdown) for exact limits.

## Storage {#storage}

SQLite in WAL mode with separate connection pools:

- **Write pool**: one connection serializes writes and writer transactions.
- **Read pool**: separate read-only connections serve queries and consistent read snapshots.

WAL permits concurrent readers and a writer, subject to SQLite locking and checkpoint behavior. Foreign keys, a bounded busy timeout and transactions enforce integrity. Long-range uptime queries use daily rollups plus uncovered raw intervals. The `Store` interface separates handlers and background services from SQL implementation details.

The server records observation provenance when persisting a check or passive state
update. Hourly and daily rollup keys include that provenance, so a manual state change
cannot contribute to probe uptime or latency even after a monitor changes type.
Observations without a recorded source keep an `unknown` source and their original
data; only verified probes feed sample metrics and anomaly baselines.

The optional `postgres` build adds pgx and an independently authored PostgreSQL schema. Shared queries use explicit dialect fragments and positional binding; runtime code does not translate SQLite DDL. PostgreSQL keeps one writer, read-only repeatable-read snapshots and a separate ownership observer. Its session lock prevents multiple active Asura servers using the same database. The default binary does not link pgx, and probe agents link neither database backend. See [PostgreSQL deployment](deployment.html#optional-postgresql-build) for setup and operational boundaries.

## Checker Registry {#checker-registry}

Startup registers 17 protocol checkers implementing `Check(ctx, monitor)`. The worker supplies the monitor's timeout context. Shared registry checks reject unsupported proxy combinations before executing a check; private-target restrictions also apply inside supported dialers. Commands require an explicit executable allowlist. See [Monitors](monitors.html) for each protocol's settings and success criteria.

## Project Structure {#project-structure}

| Area | Source |
| --- | --- |
| Executables | `cmd/asura` server/setup; `cmd/asura-agent` remote probe |
| Shared probe types | `internal/model`, independent of server and database packages |
| Check runtime | `internal/monitor`, `checker`, `assertion`, `incident`, `escalation`, `notifier` and `sla` |
| Persistence | `internal/storage`: shared SQL operations, explicit SQLite/PostgreSQL schemas and migrations |
| HTTP and permissions | `internal/server`, `api`, `web`, `config` and `httputil` |
| Credential boundaries | `internal/redact`, `logsafe`, `pageauth`, `totp` and `safenet` |
| Interface | `internal/web/views/*.templ`, `web/tailwind.input.css`, `web/css` and embedded `web/static` |
| Build and deployment | Root `Makefile`, `Dockerfile`, `docker-compose.yml`, `install.sh`, `asura.service`; `deploy/nginx.conf` |
| Verification tools | `cmd/docscheck`, `cmd/notices` and `scripts` fixtures/dependency guards |
| Documentation | `docs`: Markdown, Jekyll layouts and plain CSS; generated site in `.build/docs` |
