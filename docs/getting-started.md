---
title: "Getting started"
nav_title: "Getting started"
description: "Install Asura, create your first uptime monitor and configure alerts. Start with Docker Compose or build the Go server from source."
permalink: "/getting-started.html"
---

# Getting started {#getting-started}

Asura runs as a Go server with an embedded web interface and SQLite storage. Choose Docker Compose, the Linux service installer, or a local source build.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Docker Compose {#docker-compose}

```
git clone https://github.com/y0f/asura.git
cd asura
cp config.docker.example.yaml config.yaml
docker compose build
docker compose run --rm --no-deps asura --setup
```

Save the generated **API Key**. Put its **Hash** and **Encryption Key** in a local `.env` file, and back up the encryption key separately:

```
ASURA_ADMIN_KEY_HASH=your_generated_hash
ASURA_ENCRYPTION_KEY=your_generated_encryption_key
```

```
docker compose up -d
curl --fail http://127.0.0.1:8090/api/v1/health
```

Open `http://127.0.0.1:8090` and sign in with the API Key. The example provides one super administrator. It listens on all container interfaces while Compose publishes only on the host's loopback address. The non-root process writes its SQLite database to the named `asura-data` volume. Container replacement and `docker compose down` preserve this volume; `docker compose down --volumes` deletes it.

If startup fails, check `docker compose logs asura`. A missing or malformed hash fails configuration validation; a valid hash for a different key allows startup but rejects your login. The local example uses HTTP cookies; configure HTTPS and `auth.session.cookie_secure: true` before public access, following [deployment](deployment.html).

## Linux service {#linux-service}

The installer supports Ubuntu or Debian with systemd on amd64 and arm64. Run it from the source version you intend to install:

```
git clone https://github.com/y0f/asura.git
cd asura
sudo bash install.sh
```

It selects a patched Go compiler, builds the server, creates its service account, and installs the systemd unit. On a fresh installation it prints an admin API key and writes `/etc/asura/config.yaml`. Existing configuration and data are preserved. The default database directory is `/var/lib/asura`, and the binary is `/usr/local/bin/asura`.

```
sudo systemctl status asura
curl --fail http://127.0.0.1:8090/api/v1/health
sudo journalctl -u asura -n 50 --no-pager
```

The service listens on `127.0.0.1:8090`. Its generated configuration requires HTTPS for web-session cookies, so complete the [reverse proxy setup](deployment.html) before signing in through the browser.

## Build and run locally {#build-and-run-locally}

From a cloned repository, use Go 1.27.4 and the [Tailwind 4.2.1 standalone CLI](https://github.com/tailwindlabs/tailwindcss/releases/tag/v4.2.1) for your system. Save the CLI as `tailwindcss` in the repository root (`tailwindcss.exe` on Windows), and make it executable on Unix. Build commands use the templ version in `go.mod`; Node and a separately installed templ CLI are unnecessary. CI also tests Go 1.26.11.

```
make build
./asura --setup
```

Save the API Key and use its Hash in a minimal `config.yaml`:

```
server:
  listen: "127.0.0.1:8090"
database:
  path: "asura.db"
auth:
  api_keys:
    - name: admin
      hash: "PASTE_HASH_FROM_SETUP"
      super_admin: true
  session:
    cookie_secure: false
```

```
./asura -config config.yaml
```

Sign in at `http://127.0.0.1:8090`. This cookie setting is for local HTTP development. Add operational limits and other settings from [configuration](configuration.html), and create separate roles and keys using [access control](access-control.html).

## Development and checks {#development-and-checks}

```
# Linux, macOS, or Git Bash
make dev

# Windows PowerShell
.\dev.ps1
```

The watcher finishes template and CSS generation before rebuilding embedded assets and restarting. It detects Go, template, stylesheet and embedded-asset changes. If generation or compilation fails, the previous server keeps running.

See [CONTRIBUTING.md](https://github.com/y0f/asura/blob/main/CONTRIBUTING.md) for source checks, race-test prerequisites, Windows commands and release preparation.

## Release binaries and probe agents {#release-binaries-and-probe-agents}

Each release provides server and probe-agent binaries for Linux amd64 and arm64. Download the required binaries, `LICENSE`, `THIRD_PARTY_NOTICES.txt` and `checksums.txt` from the same release. Keep both license files with the binaries, then verify the downloaded files:

```
sha256sum --check --ignore-missing checksums.txt
```

When build attestations are available, also verify the artifact's repository identity:

```
gh attestation verify ./asura-linux-amd64 --repo y0f/asura
```

For a local release build, run `make generate css` followed by `make release`. This creates all four binaries, both license files and a checksum manifest covering those six files under `dist/` without publishing them. Preserve configuration, encryption keys and database backups when replacing a deployed binary.

Probe agents use a separate agent token created in Asura. After enabling agent checks and choosing monitor assignments, run the binary for the probe's architecture:

```
chmod +x asura-agent-linux-amd64
ASURA_AGENT_TOKEN=YOUR_AGENT_TOKEN ./asura-agent-linux-amd64 --server https://monitor.example.com
```

The agent reads its token from `ASURA_AGENT_TOKEN` or from a file given with `--token-file`, never from the command line, where other local users could read it. Include the configured base path in the final server URL; agent control requests do not follow redirects. Private-network targets are denied by default; `--allow-private` enables them for a trusted probe. `--interval` defaults to `30s`, and `--concurrency` to `10`; both must be positive. Use `--version` to inspect the installed agent. See [the API reference](api.html) for agent administration.
