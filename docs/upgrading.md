---
title: "Upgrading to v2"
nav_title: "Upgrading to v2"
description: "Upgrade Asura with a consistent backup, review v2 permissions and update probe agents. Includes verification steps and database rollback guidance."
permalink: "/upgrading.html"
---

# Upgrading to v2 {#upgrading-to-v2}

Plan an upgrade around the database, configuration and credentials together. V2 retains the single-binary deployment model and adds explicit resource actions, reusable roles and scoped access. Review permissions before exposing the upgraded instance to existing clients.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Before replacing the binary {#before-replacing-the-binary}

1. Record the installed version, service command, configuration path and persistent database path.
2. Take a consistent [database backup](backup.html) and save the matching configuration, environment files and `database.encryption_key` if used. Keep the previous binary or image reference.
3. Choose the server artifact for the operating system and architecture, verify its published checksum, and review the release notes. Use the [installation guide](getting-started.html) for the supported commands and artifact names.
4. Try the new version against a backup in an isolated environment. Prevent duplicate checks, notifications and agent connections while reviewing the restored configuration.

Asura applies database migrations during startup. A JSON configuration export is useful for transferring supported definitions, but does not preserve history or every resource needed for rollback.

## Review historical metrics {#review-historical-metrics}

Schema 41 records the source of each observation. Earlier raw history and hourly/daily
summaries remain stored as `unknown`: older versions did not record enough information
to distinguish scheduled probes from manual state changes reliably. **Verified uptime,
latency, anomaly baselines and SLA calculations restart with new probe observations.**
Windows with no verified samples show unavailable values rather than healthy defaults.

The migration preserves existing incident and state history. Raw observations and
summaries continue to follow the configured retention policy. Changing a monitor's type
does not reclassify past observations; genuine probe history remains available after a
later switch to a passive type. Review dashboards, SLA reports and monitoring queries
that previously assumed every history row was a measured check.

## Review access rules {#review-access-rules}

Visitors to password-protected status pages must sign in again after upgrading:
v2 rejects older status-page cookies and enforces a seven-day expiry on new ones.
Existing page passwords remain valid. Set or reset a page password to upgrade
its stored hash to the salted format described in [status-page access](status-pages.html#password-protection).

Startup removes heartbeat path credentials and URL userinfo, query parameters and
fragments from previously stored request history. Request counts are preserved;
new request and process logs use the same credential-safe URL projection. This
does not erase older external logs, database backups or deleted database pages.
If those copies were shared or exposed, re-enroll affected heartbeat monitors
and replace affected subscription links. Treat old copies as sensitive backups.

V2 separates reading a resource from changing it. Grants such as `monitors.update`, `monitors.pause` and `notifications.test` authorize different operations. A resource's `.write` grant covers that resource's mutation actions; it does not imply read access or access to another resource.

Reusable roles live under `auth.roles`. Keys receive roles through `role_bindings`, with an explicit global scope or selected monitor and group IDs. Monitor and group scopes restrict the monitors a binding can affect; they do not turn group selectors into permission to administer groups. The [access-control guide](access-control.html) contains the full schema, built-in roles, legacy compatibility rules, and examples.

Review every existing key against its actual job. Broad V1-era permissions used for unrelated operations may need separate V2 resource grants. Keep configuration import and secret-bearing export authority on explicit super-admin keys, and use ordinary roles for routine work. Verify both allowed operations and denied neighboring resources with representative keys.

## Review API clients and saved forms {#review-api-clients-and-saved-forms}

- Ordinary resource reads do not return stored credentials. Editing an unchanged destination can preserve supported saved secrets when those values are left blank; explicit clear controls remove them. Changing a credential destination requires deliberate replacement or clearing.
- Monitor `PUT` requests represent the monitor configuration rather than a partial patch. Preserve authorized settings when updating one field. Omitted relationship fields keep their current assignments atomically; explicitly supplied empty values clear only the relationships you are authorized to change; see [per-monitor routing](notifications.html#per-monitor-routing).
- Notification filters distinguish all events from a selected list. A selected list must contain at least one supported event. Review filters when adding new event types.
- HTTP notification destinations must return a 2xx response. Configure their final URL directly; redirects now fail delivery instead of forwarding credentials or silently accepting an undelivered notification.
- Agent jobs use explicit assignments. The upgrade converts eligible implicit assignments to explicit assignments for existing enabled agents. Newly registered agents receive no monitor jobs until assigned. Upgrade the server and agents together: v2 jobs carry a configuration revision that agent results must return.
- Point agent `--server` URLs at the final HTTPS endpoint and base path. Control requests no longer follow redirects, which could forward the agent token elsewhere.
- Heartbeat URLs are credentials. Newly created or imported heartbeat monitors show their fresh token once; ordinary reads and configuration exports do not recover it.
- Check reverse-proxy and `server.base_path` configuration, login redirects, static assets, live refresh and public status-page URLs after switching versions.

## Switch and verify {#switch-and-verify}

Stop the old process, replace its binary or pinned container image, and restart with the intended configuration and persistent storage. Do not run two server processes against the same database during the switch.

Check startup logs, authentication, permitted navigation, representative monitor results, incident history, channel configuration and status pages. Confirm that agent assignments and notification routing still match your intended setup. Test notification delivery deliberately with an authorized key and a controlled destination.

## Rollback {#rollback}

If rollback is needed, stop the new process and restore the pre-upgrade database, configuration and encryption key together with the matching previous binary or image. Preserve the failed-upgrade files separately for diagnosis. Replacing only the binary does not reverse database migrations.
