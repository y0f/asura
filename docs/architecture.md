---
title: "Architecture"
nav_title: "Architecture"
description: "Explore Asura's Go server, scheduler, probe agents, SQLite storage and embedded web interface. Understand its runtime and data flow."
permalink: "/architecture.html"
---

# Architecture {#architecture}

Single binary, channel-based pipeline, SQLite storage. Everything talks through Go channels with backpressure.

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

- Stable monitors (60+ consecutive successes) slow to 2x base interval
- Flapping monitors snap to 0.5x for faster detection
- Recovering monitors return to base interval

## Worker Pool {#worker-pool}

Fixed-size goroutine pool (default 10, set via `monitor.workers`). Workers pull jobs off the channel, invoke the protocol checker's `Check()`, and measure response time. Assertion evaluation, status transitions, and threshold logic happen downstream in the result processor — not in the pool. Bounded channel capacity means the scheduler blocks when all workers are busy.

## Result Processor {#result-processor}

Receives check results from the worker pool. For each result:

1. Persists the check result to SQLite
2. Updates monitor state (status, consecutive counts, response time)
3. Evaluates failure/success thresholds for incident transitions
4. Computes content diffs if `track_changes` is enabled
5. Publishes events to the dispatcher

## Dispatcher {#dispatcher}

Routes events to notification channels. Each channel type has its own sender behind the `Sender` interface. 13 implementations: Webhook, Email, Telegram, Discord, Slack, ntfy, Microsoft Teams, PagerDuty, Opsgenie, Pushover, Google Chat, Matrix, and Gotify. If a monitor has `notification_channel_ids` set, only those channels fire. Otherwise all matching channels get the event.

## Storage {#storage}

SQLite in WAL mode with separate connection pools:

- **Write pool**: Single writer protected by `writeMu` mutex. all INSERT/UPDATE/DELETE go through this
- **Read pool**: Multiple concurrent readers via `readerPool()`. all SELECT queries

Reads never block on writes. The `Store` interface covers everything from monitors to request logs.

## Checker Registry {#checker-registry}

Each checker implements `Checker` with a `Check(ctx, monitor)` method and gets registered at startup. 17 implementations:

| Checker | Protocol |
| --- | --- |
| HTTP | HTTP/HTTPS with auth, redirects, TLS verify |
| TCP | TCP connect with send/expect |
| DNS | DNS record queries |
| ICMP | Ping (raw socket or unprivileged) |
| TLS | Certificate expiry checking |
| WebSocket | WS connect with send/expect |
| Command | Execute shell commands |
| Docker | Container state via Docker socket |
| Domain | WHOIS expiry lookups |
| gRPC | gRPC health check protocol |
| MQTT | MQTT 3.1.1 connect + subscribe |
| HTTP Multi-Step | Chained HTTP requests with variable extraction |
| SMTP | Mail server connect (optional STARTTLS) |
| SSH | SSH server handshake |
| Redis | Redis PING |
| PostgreSQL | PostgreSQL connection check |
| UDP | UDP send/expect |

## Project Structure {#project-structure}

```
asura/
├── cmd/asura/         # Entry point, setup
├── internal/
│   ├── analytics/     # Request log analytics and rollups
│   ├── api/           # API handlers (JSON endpoints)
│   ├── assertion/     # Assertion engine for check results
│   ├── checker/       # 17 protocol checkers
│   ├── config/        # YAML configuration loading
│   ├── diff/          # Content change diff computation
│   ├── httputil/      # HTTP utilities (ID parsing, JSON helpers)
│   ├── incident/      # Incident lifecycle management
│   ├── monitor/       # Scheduler, workers, result processor
│   ├── notifier/      # 13 notification channel senders
│   ├── safenet/       # SSRF protection (private IP blocking)
│   ├── server/        # HTTP server, routes, middleware
│   ├── storage/       # SQLite store, migrations
│   ├── totp/          # TOTP two-factor authentication
│   ├── validate/      # Input validation
│   └── web/           # Web UI handlers (template rendering)
├── web/
│   ├── templates/     # HTML templates (layout + feature dirs)
│   └── static/        # CSS, JS, images
├── deploy/            # nginx.conf, Dockerfile, docker-compose
└── docs/              # Documentation site
```
