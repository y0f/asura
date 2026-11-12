---
title: "Self-hosted uptime monitoring and status pages"
nav_title: "Overview"
description: "Open-source uptime monitoring with alerts, incident tracking, SLA reports and public status pages. Self-host Asura as a single Go binary."
permalink: "/"
---

# Self-hosted uptime monitoring for websites, APIs and network services {#self-hosted-uptime-monitoring-for-websites-apis-and-network-services}

Asura runs availability checks, records incidents and sends alerts from infrastructure you control. It runs as a single Go binary with SQLite storage and includes a web interface, an API and public status pages. Reusable roles and scoped permissions control what each API key or web session can see and change.

## Start with a guide {#start-with-a-guide}

| Task | Guide |
| --- | --- |
| Install Asura and create your first monitor | [Getting started](getting-started.html) |
| Define roles and restrict access | [Access control](access-control.html) |
| Choose checks and define success conditions | [Monitors](monitors.html) |
| Send alerts to your team | [Notifications](notifications.html) |
| Escalate unresolved incidents | [Escalation policies](escalation-policies.html) |
| Publish service availability | [Status pages](status-pages.html) |
| Run Asura behind a proxy or in Docker | [Deployment](deployment.html) |
| Protect and restore your data | [Backup and restore](backup.html) |

## Configure and integrate {#configure-and-integrate}

The [configuration reference](configuration.html) covers server, storage, authentication and monitoring settings. The [API reference](api.html) describes endpoints and the permissions needed to use them. Read the [architecture guide](architecture.html) for an overview of the application.

The web interface is rendered by Go and enhanced with HTMX and Alpine.js. Static assets are included in the binary; no separate frontend server is required.

Asura is available under the [MIT license](https://github.com/y0f/asura/blob/main/LICENSE). Source code and releases are on [GitHub](https://github.com/y0f/asura).
