---
title: "Deployment"
nav_title: "Deployment"
description: "Deploy Asura with Docker or systemd. Configure HTTPS, reverse proxies, trusted headers, subpaths and persistent storage."
permalink: "/deployment.html"
---

# Deployment {#deployment}

Keep the service on a private interface and put an HTTPS reverse proxy in front. The Linux installer binds to `127.0.0.1:8090`. The Docker sample listens on `0.0.0.0:8090` inside its container and publishes only to host loopback. See [getting started](getting-started.html) for installation and [upgrading](upgrading.html) before replacing an existing version.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Optional PostgreSQL build {#optional-postgresql-build}

SQLite remains the default and needs no database service. PostgreSQL is available through an explicit source build, tested against PostgreSQL 18.6. Follow the [source prerequisites](https://github.com/y0f/asura/blob/main/CONTRIBUTING.md#local-setup), then build:

```sh
make build-postgres
./asura-postgres -config config.yaml
```

The target regenerates the embedded interface and writes `asura-postgres`. For unchanged committed assets, `go build -tags postgres -o asura-postgres ./cmd/asura` also builds the optional backend. Standard release binaries, the installer and the default Docker image keep the SQLite-only build.

Provision a dedicated empty database owned by Asura's login role. The role needs ownership to create and migrate its tables, indexes, functions and triggers; it does not need superuser, role-creation or database-creation privileges. Asura uses the `public` schema and rejects unrelated existing objects during first initialization. Configure the [database driver and DSN](configuration.html#database-backend) before starting. Keep PostgreSQL autovacuum enabled.

Run one Asura server per database. Its persistent writer connection holds an advisory ownership lock, while separate read-only connections serve queries and consistent snapshots. A second server fails startup. Loss of the writer session or ownership stops the process instead of reconnecting silently; a configured service supervisor may then restart it. Investigate repeated ownership failures. Do not use a transaction-pooling proxy: ownership requires a persistent database session. The server uses up to `max_read_conns + 2` database connections, including its writer and ownership observer.

Changing `database.driver` does not convert a SQLite file. Transfer portable configuration into the new backend with [v2 export/import](backup.html#json-configuration-export), then re-enroll imported heartbeats and review explicit agent mappings. Historical data, sessions and subscriptions are not transferred. Keep the original database, configuration and encryption key for rollback.

## Configure the public address {#configure-the-public-address}

For a Linux service behind a proxy on the same host, merge these settings into `/etc/asura/config.yaml`. Keep your existing `auth.api_keys` and database settings:

```
server:
  listen: "127.0.0.1:8090"
  base_path: "/asura"
  external_url: "https://example.com/asura"
  trusted_proxies:
    - "127.0.0.1"
    - "::1"
auth:
  session:
    cookie_secure: true
```

Use your own domain. For a dedicated hostname such as `https://monitor.example.com`, omit `base_path` and use that URL for `external_url`. The reverse proxy must preserve the configured path. Trust only the proxy addresses Asura actually receives connections from; container networking can make these different from the proxy's host-loopback address.

```
sudo systemctl restart asura
sudo systemctl status asura
```

For Docker, edit the mounted `config.yaml`, keep its container listen address and `/app/data/asura.db` path, then run `docker compose restart asura`. If you enable `base_path`, update the Compose healthcheck URL to include it; changes to Compose configuration require `docker compose up -d` to recreate the service.

## Nginx {#nginx}

The repository's [nginx example](https://github.com/y0f/asura/blob/main/deploy/nginx.conf) includes a sub-path configuration and a commented root-hostname alternative. Set up your domain and TLS certificates, then edit the domain, certificate paths and application path before enabling the site:

```
sudo cp deploy/nginx.conf /etc/nginx/sites-available/asura
sudoedit /etc/nginx/sites-available/asura
sudo ln -s /etc/nginx/sites-available/asura /etc/nginx/sites-enabled/asura
sudo nginx -t
sudo systemctl reload nginx
```

Use `proxy_pass http://127.0.0.1:8090;` without a replacement URI when preserving `/asura/`. Forward the host, client-address and scheme headers as shown in the example. Asura sends `X-Accel-Buffering: no` for live event streams; avoid proxy settings that ignore this header. See the [nginx proxy reference](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_pass) for URI handling.

## Caddy {#caddy}

For a domain pointing at this host, Caddy can manage HTTPS automatically. This configuration preserves the application prefix:

```
example.com {
    redir /asura /asura/ permanent
    reverse_proxy /asura/* 127.0.0.1:8090
}
```

For a dedicated hostname with no `base_path`:

```
monitor.example.com {
    reverse_proxy 127.0.0.1:8090
}
```

See the [Caddy reverse-proxy reference](https://caddyserver.com/docs/caddyfile/directives/reverse_proxy) for forwarded headers and streaming behavior.

## Verify the deployment {#verify-the-deployment}

```
# For the /asura base path used above
curl --fail http://127.0.0.1:8090/asura/api/v1/health
curl --fail https://example.com/asura/api/v1/health
```

Open `https://example.com/asura/` and sign in. Check that navigation, live updates and a permitted write action work. Test a restricted key as well as the administrator key. A health response confirms that the HTTP service is reachable; it does not validate monitor targets, notification credentials or role assignments.

If the service fails, inspect `journalctl -u asura` or `docker compose logs asura`. Confirm that the process can write to its data directory, the configured credential hash is present, and the public URL and path match the proxy. Secure cookies require browser access over HTTPS.

## Data and upgrades {#data-and-upgrades}

Keep the SQLite database on persistent local storage and preserve its encryption key. The default systemd unit permits writes under `/var/lib/asura`; changing the database path requires matching directory ownership and service write permissions. Docker stores the default SQLite database in its named data volume. PostgreSQL persistence and backups are managed by its database service.

Use the [backup and restore guide](backup.html) and test a restore before upgrading. A configuration export does not include all database state. Keep the old binary or image together with its matching database backup for rollback.
