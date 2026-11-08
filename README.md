<p align="center">
  <img src="assets/asura.gif" alt="Asura" height="28"/>
</p>

# Asura — uptime monitoring and status pages

Asura is an open-source, self-hosted uptime monitoring platform for websites, APIs and network services. Track availability, investigate incidents, and keep your team and customers informed with alerts and public status pages.

Run a single Go binary with an embedded web interface and SQLite storage. Combine remote probes, maintenance schedules and monthly SLA reports with role-based access to individual monitors and groups. No separate frontend server or database service is required.

[Documentation](https://y0f.github.io/asura/) · [Quick start](https://y0f.github.io/asura/getting-started.html) · [Access control](https://y0f.github.io/asura/access-control.html) · [Upgrading to v2](https://y0f.github.io/asura/upgrading.html) · [Contributing](CONTRIBUTING.md)

![Asura dashboard with monitor health, response times and incidents](assets/webpanel.png)

## What it does

- **Monitoring:** HTTP and multi-step HTTP, TCP, UDP, DNS, ICMP, TLS, WebSocket, gRPC, MQTT, Docker, domain/WHOIS, command, SMTP, SSH, Redis, PostgreSQL, heartbeat and manual monitors. Add assertions, response-body change detection and remote probe agents.
- **Alerts:** Webhook, email, Telegram, Discord, Slack, ntfy, Teams, PagerDuty, Opsgenie, Pushover, Google Chat, Matrix and Gotify. Route events through notification channels and escalation policies.
- **Operations:** Live updates, bulk actions, maintenance schedules, incident history, public status pages and subscriptions.
- **Access control:** API keys, TOTP and reusable roles with action permissions. Bind monitor access globally or to selected monitors and groups; reserve configuration transfer and full-secret export for super administrators.
- **History:** Hourly and daily summaries, uptime charts, monthly SLA reports and error budgets use verified active-check observations. Manual and heartbeat monitors report state without sampled uptime or SLA. Configuration export/import is separate from a database backup.

The [monitor reference](https://y0f.github.io/asura/monitors.html) describes each check's settings and limits. The [API reference](https://y0f.github.io/asura/api.html) covers automation.

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

Open **http://127.0.0.1:8090** and sign in with the API Key. The container runs as a non-root user, listens on its container interface, and publishes only to the host's loopback address. Its SQLite database is stored in the named `asura-data` volume and survives container replacement.

For public access, follow the [deployment guide](https://y0f.github.io/asura/deployment.html) to configure an HTTPS reverse proxy, `server.external_url`, trusted proxy addresses and `auth.session.cookie_secure: true`.

## Install as a Linux service

On Ubuntu or Debian with systemd:

```bash
git clone https://github.com/y0f/asura.git
cd asura
sudo bash install.sh
```

The installer builds the server, creates its service account, and installs the systemd unit. It preserves an existing configuration and data directory. A fresh installation prints an admin API key and writes configuration to `/etc/asura/config.yaml`; the database lives under `/var/lib/asura`.

The service binds to `127.0.0.1:8090`. Its generated configuration requires HTTPS for session cookies; finish the [reverse proxy setup](https://y0f.github.io/asura/deployment.html) before using the web interface.

For source builds or Windows development, see [CONTRIBUTING.md](CONTRIBUTING.md). Builds use Go 1.27.3; CI also tests the supported minimum, Go 1.26.10.

## Upgrade from v1

Read the [v2 upgrade guide](https://y0f.github.io/asura/upgrading.html) and [access-control migration notes](https://y0f.github.io/asura/access-control.html) before replacing an existing installation.

1. Stop the server and back up the database directory, configuration and encryption key. Keep the previous binary or image reference.
2. Review API key roles and permissions, then install the new version while preserving the configuration and data paths.
3. Start the server to apply database migrations. Check health, sign-in, representative monitor results, role restrictions and alert delivery.

For rollback, restore the previous binary or image together with its matching database backup. A configuration export contains configuration objects; it does not replace a backup of check history and other database state. See [backup and restore](https://y0f.github.io/asura/backup.html).

## Release artifacts and verification

The v2 release workflow builds these assets from sources that pass CI:

| Platform | Server | Probe agent |
|---|---|---|
| Linux amd64 | `asura-linux-amd64` | `asura-agent-linux-amd64` |
| Linux arm64 | `asura-linux-arm64` | `asura-agent-linux-arm64` |

Download the binaries you need, `LICENSE`, `THIRD_PARTY_NOTICES.txt` and `checksums.txt` from the same [release](https://github.com/y0f/asura/releases). Keep both license files with the binaries and verify the downloaded files before installation:

```bash
sha256sum --check --ignore-missing checksums.txt
```

For a release with GitHub build attestations, verify the artifact's repository identity as well:

```bash
gh attestation verify ./asura-linux-amd64 --repo y0f/asura
```

CI checks generated templates, documentation and CSS; formatting and static analysis; tests with race detection; source and binary vulnerabilities; and both server and agent architectures. Release publication additionally generates build attestations. Local builds remain available through `make build` and `make release` without publishing artifacts.

## Project

The server uses Go's `net/http`, pure Go SQLite, templ, HTMX, Alpine.js and Tailwind CSS. Assets are embedded in the server binary. The standalone Tailwind CLI and the module-pinned templ generator are build tools; Node is not required to build or run Asura.

See [architecture](https://y0f.github.io/asura/architecture.html), [contributing](CONTRIBUTING.md) and the [security policy](SECURITY.md). Licensed under [MIT](LICENSE).
