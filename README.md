<p align="center">
  <img src="assets/asura.gif" alt="Asura" height="28"/>
</p>
<p align="center">Uptime monitoring in a single Go binary. No databases to manage, no runtime to install, no containers required.</p>
<p align="center">
  <a href="https://github.com/y0f/asura/actions/workflows/ci.yml"><img src="https://github.com/y0f/asura/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://goreportcard.com/report/github.com/y0f/asura?branch=main"><img src="https://goreportcard.com/badge/github.com/y0f/asura?branch=main" alt="Go Report Card"></a>
  <a href="https://github.com/y0f/asura/blob/main/go.mod"><img src="https://img.shields.io/github/go-mod/go-version/y0f/asura" alt="Go Version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"></a>
  <a href="https://github.com/y0f/asura/releases/latest"><img src="https://img.shields.io/github/v/release/y0f/asura?include_prereleases&sort=semver" alt="Release"></a>
  <a href="https://github.com/y0f/asura/pkgs/container/asura"><img src="https://img.shields.io/badge/ghcr.io-asura-blue?logo=docker" alt="Docker"></a>
</p>
<p align="center">
  <a href="https://y0f.github.io/asura/">Documentation</a> &middot;
  <a href="https://y0f.github.io/asura/#getting-started">Quick Start</a> &middot;
  <a href="https://y0f.github.io/asura/#api">API Reference</a> &middot;
  <a href="CONTRIBUTING.md">Contributing</a>
</p>

---

## Quick Start

```bash
git clone https://github.com/y0f/Asura.git && cd Asura && sudo bash install.sh
```

Installs Go if needed, builds the binary, creates a systemd service, and generates an admin API key. Takes a couple of minutes on a fresh Ubuntu box.

By default Asura binds to `127.0.0.1:8090` — set up a [reverse proxy](https://y0f.github.io/asura/#deployment) to expose it publicly.

**Docker:**

```bash
git clone https://github.com/y0f/Asura.git && cd Asura
cp config.example.yaml config.yaml
docker compose up -d
```

---

![Web UI](assets/webpanel.png)

---

## What it does

- **13 monitor types** — HTTP, TCP, DNS, ICMP, TLS, WebSocket, Command, Docker, Domain (WHOIS), gRPC, MQTT, passive heartbeat, and manual
- **Assertion engine** — 8 condition types with AND/OR group logic: status code, body text, body regex, JSON path, headers, response time, cert expiry, DNS records
- **Incidents** — automatic creation with configurable failure/success thresholds, acknowledge, recovery
- **13 notification channels** — Webhook (HMAC-SHA256), Email, Telegram, Discord, Slack, ntfy, Microsoft Teams, PagerDuty, Opsgenie, Pushover, Google Chat, Matrix, Gotify
- **Escalation policies** — time-based notification chains with per-step delays, channel targeting, and repeat mode
- **Status pages** — multiple public pages with custom slugs and monitor grouping
- **Change detection** — line-level diffs on HTTP response bodies
- **Maintenance windows** — recurring schedules to suppress alerts during planned downtime
- **Heartbeat monitoring** — cron jobs and workers report in; silence triggers an incident
- **Advanced HTTP auth** — OAuth2 Client Credentials with token caching, mutual TLS (mTLS) with inline PEM certificates
- **Proxy support** — HTTP and SOCKS5 proxies with per-monitor assignment
- **SLA targets** — per-monitor SLA tracking with error budgets, breach alerts, and monthly reports
- **Analytics** — uptime %, response time percentiles, Prometheus `/metrics`

## Why Asura?

| | Asura | Typical alternative |
|---|---|---|
| **Runtime** | Single static binary | Node.js / Java / Python runtime |
| **Database** | SQLite compiled in | Requires Postgres, MySQL, or Redis |
| **Binary size** | ~15 MB | 100 MB+ installed |
| **Deploy** | `scp` binary + run | Package manager, runtime install, migrations |
| **RAM** | ~20 MB idle | Varies — runtime + database overhead |

---

## Documentation

| | |
|---|---|
| [Getting Started](https://y0f.github.io/asura/#getting-started) | Install via VPS, Docker, or source |
| [Deployment](https://y0f.github.io/asura/#deployment) | nginx / Caddy reverse proxy, TLS |
| [Configuration](https://y0f.github.io/asura/#configuration) | Config reference, auth, adaptive intervals |
| [Monitors](https://y0f.github.io/asura/#monitors) | All monitor types, assertions, heartbeats, manual |
| [Notifications](https://y0f.github.io/asura/#notifications) | All channels, webhook signing, per-monitor routing |
| [Escalation Policies](https://y0f.github.io/asura/#escalation-policies) | Time-based notification chains with delays and repeat |
| [API Reference](https://y0f.github.io/asura/#api) | Full endpoint reference with request/response examples |
| [SLA Targets](https://y0f.github.io/asura/#sla) | Per-monitor SLA tracking, error budgets, breach alerts |
| [Backup & Restore](https://y0f.github.io/asura/#backup) | SQLite backup strategy and restore procedure |
| [Architecture](https://y0f.github.io/asura/#architecture) | Pipeline, storage, checker registry |

---

## Tech Stack

- **Go 1.24+** with stdlib `net/http` — no frameworks
- **SQLite** via `modernc.org/sqlite` (pure Go, no CGO)
- **[templ](https://templ.guide/)** for type-safe server-rendered HTML
- **HTMX** + **Alpine.js** for progressive enhancement
- **Tailwind CSS v4** standalone CLI (no Node.js)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
