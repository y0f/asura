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

Keep the database on persistent local storage and preserve its encryption key. The default systemd unit permits writes under `/var/lib/asura`; changing the database path requires matching directory ownership and service write permissions. Docker stores the default database in its named data volume.

Use the [backup and restore guide](backup.html) and test a restore before upgrading. A configuration export does not include all database state. Keep the old binary or image together with its matching database backup for rollback.
