<p align="center">
  <img src="assets/asura.gif" alt="Asura" height="28"/>
</p>

# Asura — uptime monitoring and status pages

Self-hosted uptime monitoring for websites, APIs and network services. Track availability, investigate incidents, and keep your team and customers informed with alerts and public status pages.

Asura runs as one Go binary with an embedded web interface and SQLite storage. V2 adds fine-grained roles scoped to individual monitors and groups, alongside remote probes, maintenance schedules and SLA reports. An optional PostgreSQL build is available.

[Documentation](https://y0f.github.io/asura/) · [Quick start](https://y0f.github.io/asura/getting-started.html) · [Access control](https://y0f.github.io/asura/access-control.html) · [Upgrading to v2](https://y0f.github.io/asura/upgrading.html) · [Contributing](CONTRIBUTING.md)

![Asura dashboard with monitor health, response times and incidents](assets/webpanel.png)

![Monitor details with response times, availability history and SLA metrics in dark mode](assets/monitorview.png)

![Public status page with service availability and incident updates](assets/statuspage.png)

## What it does

- **Check services:** [19 monitor types](https://y0f.github.io/asura/monitors.html), assertions, response-change detection and distributed probe agents.
- **Reach your team:** [13 alert integrations](https://y0f.github.io/asura/notifications.html), per-monitor routing, escalation policies and on-call schedules.
- **Manage incidents:** Live dashboards, bulk actions, maintenance windows, public status pages and email subscriptions.
- **Control access:** API keys, TOTP, reusable roles, action permissions and monitor/group scopes. Separate diagnostic access and protected secrets.
- **Understand availability:** Response-time charts, monthly SLA reports and error budgets from verified probe samples. Heartbeat and manual monitors report state without sampled uptime or SLA.

## Start with Docker Compose

```bash
git clone https://github.com/y0f/asura.git
cd asura
cp config.docker.example.yaml config.yaml
docker compose build
docker compose run --rm --no-deps asura --setup
```

Save the generated **API Key**. Put its **Hash** value in a local `.env` file:

```dotenv
ASURA_ADMIN_KEY_HASH=your_generated_hash
```

```bash
docker compose up -d
curl http://127.0.0.1:8090/api/v1/health
```

Open **http://127.0.0.1:8090** and sign in with the API Key. The non-root container publishes to loopback; its database persists in the `asura-data` volume.

For public access, follow the [deployment guide](https://y0f.github.io/asura/deployment.html) to configure an HTTPS reverse proxy, `server.external_url`, trusted proxy addresses and `auth.session.cookie_secure: true`.

Prefer a Linux service or a binary? See [installation options](https://y0f.github.io/asura/getting-started.html). For source builds and Windows development, see [CONTRIBUTING.md](CONTRIBUTING.md).

## Upgrade from v1

Read the [v2 upgrade guide](https://y0f.github.io/asura/upgrading.html) before switching. Stop the server and back up its database, configuration and encryption key; review permissions and upgrade probe agents together with the server. Schema 41 preserves older history but restarts verified metrics with new probe samples. Protected status pages require visitors to sign in again.

Rollback requires the previous binary **and its matching database backup**. A configuration export is not a database backup.

## Release artifacts and verification

The [v2 release workflow](https://github.com/y0f/asura/releases) produces server and probe-agent binaries for Linux amd64 and arm64, checksums and GitHub build attestations. Download the matching `LICENSE` and `THIRD_PARTY_NOTICES.txt` with your binaries.

```bash
sha256sum --check --ignore-missing checksums.txt
gh attestation verify ./asura-linux-amd64 --repo y0f/asura
```

CI checks generated assets and documentation, static analysis, race tests, SQLite and PostgreSQL storage, and source/binary vulnerabilities before release publication.

## Project

Built with Go, templ, HTMX, Alpine.js and Tailwind CSS. Assets are embedded; Node is not required to build or run Asura.

See [architecture](https://y0f.github.io/asura/architecture.html), [contributing](CONTRIBUTING.md) and the [security policy](SECURITY.md). Licensed under [MIT](LICENSE).
