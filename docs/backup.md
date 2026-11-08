---
title: "Backup and restore"
nav_title: "Backup and restore"
description: "Back up and restore Asura safely. Preserve the database, configuration and encryption key, and understand configuration export limits."
permalink: "/backup.html"
---

<span id="backup-restore"></span>

# Backup and restore {#backup-and-restore}

A recoverable Asura backup includes a consistent SQLite database, the configuration used to open it, and any external secrets or files referenced by that configuration. Use a database backup for disaster recovery and rollback. Use the JSON export in Settings to transfer portable configuration between installations.

<span id="best-practices"></span>

## Back up the database and configuration {#back-up-the-database-and-configuration}

1. Find the database file from `database.path` in your active configuration. For Docker, use the persistent data volume, not the container's disposable filesystem.
2. Create a consistent database snapshot with SQLite's backup command, or stop Asura cleanly before copying its database files.
3. Save `config.yaml`, environment or secret files, and any referenced TLS certificates or keys. Preserve `database.encryption_key` when configured: restoring the database without the matching key will not recover encrypted credentials.
4. Record the Asura version and keep a compatible binary or container image reference with the backup.

With the SQLite CLI installed, this example creates a snapshot while Asura is running. Replace both paths with your actual database and backup destination:

```
sqlite3 /path/to/asura.db ".backup '/backup/asura.db'"
```

SQLite's [online backup mechanism](https://sqlite.org/backup.html) produces a consistent snapshot; the [CLI reference](https://sqlite.org/cli.html) documents `.backup`. An ordinary copy of a live database file can miss changes in its WAL. For an offline file copy, stop Asura and preserve any remaining database companion files together with the database.

## Verify and restore {#verify-and-restore}

Test the backup in a separate directory with a copy of the matching configuration and key. Keep the test instance isolated from production check targets, agents and notification destinations until you have reviewed its configuration. Confirm that the database opens, expected monitors and history are present, and stored credentials can be read.

For a production restore, stop Asura first and preserve the current database and configuration separately. Restore into a clean data directory, with the correct file ownership and encryption key; do not mix the restored database with WAL or shared-memory files from another database. Start the matching Asura version and verify its logs and state before resuming normal operation.

<span id="export"></span>

## JSON configuration export {#json-configuration-export}

Settings offers full and redacted v2 exports. Both require `super_admin: true`; ordinary resource permissions do not grant configuration transfer access. IDs in an export belong to that file. Import resolves their relationships to the destination's IDs.

| Resource | Portable configuration |
| --- | --- |
| Monitors | Definitions, protocol settings, assertions, groups, tags, channel routing, proxy references, escalation references, and explicit agent references |
| Shared resources | Groups, tags, proxies, notification channels, escalation policies and their steps |
| Operations | Maintenance schedules, active state and monitor selections; on-call rotations, cadence anchors and overrides |
| Status pages | Page settings, branding, password protection, monitor membership and display order |

History, sessions, audit records and status-page subscribers stay in the database backup. Authentication, roles, server settings and other YAML configuration stay in the configuration backup. Agent enrollment tokens and heartbeat credentials are never portable export data.

A full export contains credentials for its portable resources, including channel and proxy settings and status-page password hashes. Store it as a secret. A redacted export is for sharing: it removes targets, settings, assertions, channel settings, proxy credentials and page password hashes. Import rejects redacted files. Review names, descriptions, branding and other free-form content before sharing either kind of file.

```
curl --fail --show-error -H "X-API-Key: ${ASURA_API_KEY}" \
  https://example.com/api/v1/export > configuration.json

curl --fail --show-error -H "X-API-Key: ${ASURA_API_KEY}" \
  'https://example.com/api/v1/export?redact_secrets=true' > configuration-redacted.json
```

Add your configured `server.base_path` before `/api/v1` when Asura is hosted below the domain root.

<span id="import"></span>

## JSON configuration import {#json-configuration-import}

Import transfers supported configuration into an instance. It does not restore historical database state. Test the file on an isolated instance and take a database backup before replacing existing configuration.

| Mode | Purpose |
| --- | --- |
| `merge` | Create imported items and retain uniquely matching existing items. Resources match by name; status pages match by slug. Ambiguous existing names cause the import to fail. |
| `replace` | Delete the existing portable configuration and replace it with the file. This also deletes related monitoring and notification history, and status-page subscribers. Notification history is removed when its channel is deleted, including entries without a monitor. Take a database backup first. |

```
curl --fail --show-error -X POST -H "X-API-Key: ${ASURA_API_KEY}" \
  -H "Content-Type: application/json" --data-binary @configuration.json \
  'https://example.com/api/v1/import?mode=merge'
```

Import validates and writes the configuration in one transaction. A failure rolls back the import, including a replacement's deletions. The success response reports created and skipped entries. Review the resulting configuration and check targets before allowing normal monitoring and notification delivery. Import requires the same explicit super-admin authority as export.

### Heartbeat enrollment {#heartbeat-enrollment}

Each newly imported heartbeat monitor receives a fresh credential. The web import result shows its new ping URL once; the API response returns `heartbeats` entries containing `monitor_id`, `monitor_name` and the one-time `token`. Update the corresponding job with this new credential. A heartbeat skipped during a merge keeps its existing credential. Exports cannot recover either credential.

### Agent references {#agent-references}

Agent references resolve to already enrolled destination agents with unique matching names, or to an explicit source-to-destination agent mapping supplied with the import. Import never creates or enables agents or transfers agent tokens. Review every mapping before importing: assignment delegates eligible monitor data to that agent.

### Older export files {#older-export-files}

V1 files can resolve the name-based relationships they contain. V1 maintenance selections with numeric monitor IDs are rejected because those files did not include the monitor IDs needed to translate the selections safely. Review and recreate those selections explicitly, or export v2 configuration from an upgraded copy of the original database.
