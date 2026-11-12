---
title: "Access control"
nav_title: "Access control"
description: "Manage Asura API keys, reusable roles and fine-grained permissions. Scope access to monitors and groups, preserve credentials and use role shorthands."
permalink: "/access-control.html"
---

# Access control {#access-control}

API keys and web sessions share one authorization policy. Each principal in `auth.api_keys` receives actions through role bindings, explicit global permissions, or the separate `super_admin` flag. Roles and bindings live in `config.yaml`.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Define a role and bind its scope {#define-a-role-and-bind-its-scope}

```yaml
auth:
  roles:
    - name: production-editor
      description: Manage monitors in the production group
      permissions:
        - monitors.read
        - monitors.create
        - monitors.update
        - monitors.clone
        - monitors.assign
        - incidents.read
        - incidents.acknowledge

  api_keys:
    - name: bootstrap-admin
      hash: "${ASURA_ADMIN_KEY_HASH}"
      super_admin: true

    - name: production-team
      hash: "${ASURA_PRODUCTION_KEY_HASH}"
      role_bindings:
        - role: production-editor
          scope:
            group_ids: [2]
        - role: viewer
          scope:
            monitor_ids: [7]

    - name: global-viewer
      hash: "${ASURA_VIEWER_KEY_HASH}"
      role_bindings:
        - role: viewer
          scope:
            all: true
```

Generate a key and its hash with `asura --setup`; keep the key private and configure only its hash. Replace the example IDs with existing monitors and groups. Restart after authentication changes. Sessions resolve against the current configuration, so removing a principal or rotating its key revokes its previous sessions.

A role has a unique `name`, optional `description`, and nonempty `permissions` list. Custom names start with a lowercase letter and contain at most 64 lowercase letters, digits, underscores or hyphens. Built-in and shorthand role names are reserved. Unknown roles, unknown actions, duplicate definitions, invalid IDs and empty scopes fail configuration validation.

## Built-in roles {#built-in-roles}

| Role | Actions | Allowed binding scope |
| --- | --- | --- |
| `viewer` | Read monitors, incidents, SLA reports, request logs, notification history and maintenance windows. | All monitors, specific monitor IDs, or current members of specific groups. |
| `operator` | Viewer actions, monitor pause/resume/manual status, incident acknowledge/resolve/severity, and maintenance create/update/delete/toggle. | All monitors, specific monitor IDs, or current members of specific groups. |
| `administrator` | Every ordinary resource/action permission, including subscriber contacts and administration. | `all: true` only. It does not grant `super_admin`. |

The viewer role excludes unrelated configuration, notification-channel settings, subscriber contacts, audit records and database metadata. The operator role does not grant monitor configuration changes, credential delegation, or incident deletion. Use a custom role when those actions are needed.

## How bindings combine {#how-bindings-combine}

Each binding chooses `all: true`, or a nonempty combination of `monitor_ids` and `group_ids`. Mixing `all: true` with IDs is invalid. Either an ID or current group match grants the binding's actions. Group bindings follow current membership; monitor-ID bindings follow that specific monitor.

Multiple bindings add grants for the same action. They never transfer an action to another binding's objects. For example, `monitors.read` on monitor 10 and `monitors.update` on monitor 20 allow reading 10 and updating 20; they do not allow updating 10 or reading 20. Operations requiring multiple actions must satisfy every action on the affected object. There is no role inheritance, implicit read grant or deny-rule precedence.

`resource.write` is an umbrella for that resource's mutation actions only. For example, `monitors.write` includes monitor creation, editing, deletion, pause/resume, manual status, cloning and assignment. It grants neither `monitors.read` nor permissions on groups, channels, agents or other resources. Unknown actions and wildcards such as `*` or `monitors.*` are rejected.

## Which resources can be scoped {#which-resources-can-be-scoped}

Monitor and group selectors apply to `monitors`, `diagnostics`, `incidents`, `sla`, `request_logs`, `notification_history` and `maintenance`. Incidents, reports and log/history entries use the monitor they belong to. Entries without an associated monitor require a global grant. A maintenance window requires authority over every monitor it affects; an all-monitor window requires a global grant.

Other resources require `all: true`, including group administration, tags, notification channels, escalation policies, proxies, agents, status-page administration, subscriber contacts, on-call schedules, audit records, settings, database operations and aggregate metrics. A group selector grants access to its member monitors, not to administration of that group. A role containing any global-only permission cannot be bound to monitor or group IDs; split it into separate roles and bindings.

`metrics.read` is an explicit global operational-export permission. Its output includes monitor names, types and tags and global incident/request aggregates. It excludes credentials, targets and settings and grants no ordinary monitor API reads. Do not use it as a substitute for a narrowly scoped viewer role.

Related resource access is independent. A monitor permission does not grant channel secrets, proxy settings, agent credentials, subscriber contacts or unrelated nested names. Necessary relationship IDs may remain visible without the related resource's read permission. Changing a relationship requires the appropriate assignment authority and access to the selected related resource; unrelated edits preserve inaccessible relationships.

## Diagnostic access {#diagnostic-access}

`diagnostics.read` is a separate monitor/group-scoped permission for potentially sensitive check output and execution expressions. It is excluded from `viewer`, `operator` and the `readonly` shorthand. `administrator` includes it. Grant it explicitly only to principals who need to inspect response content or diagnostic errors.

Raw check history, content-change details and assertion/extraction definitions require both `monitors.read` and `diagnostics.read` for the same monitor. Raw incident causes and timeline messages require both `incidents.read` and `diagnostics.read` for that incident's current monitor. The two actions are intersected; a read grant for one group and diagnostic grant for another do not combine. Diagnostic access alone grants no monitor or incident access. Incident cause searches use the same diagnostic scope before counting or paginating results.

For example, a custom role can contain `permissions: [monitors.read, incidents.read, diagnostics.read]` and be bound to `scope: {group_ids: [7]}`. Keep the ordinary viewer binding if diagnostic access should cover only part of its monitor scope.

Ordinary readers retain status, timing and event timestamps. They receive safe incident summaries instead of raw errors, and no response headers, bodies, content hashes or diffs. Stored credentials and request payloads remain hidden even from diagnostic readers; diagnostic access is not a credential-backup permission. See [API diagnostic fields](api.html#diagnostic-fields) for the exact boundary.

## Creation, cloning and moving monitors {#creation-cloning-and-moving-monitors}

A new monitor has no existing ID to match. Creating it therefore requires `monitors.create` for its destination group, or a global grant. Creating an ungrouped monitor requires a global grant. Initial placement does not require `groups.read`; without that permission the form uses a neutral group-ID label. Cloning requires source read and clone authority, plus clone authority for the destination group; an ID-only clone grant cannot create another object. Copied channel, tag, proxy, escalation-policy and agent assignments also require destination assignment authority and the related resource's read permission.

Changing a monitor's group requires update and assignment authority over both the current monitor and destination group, plus `groups.read`. An ID-only grant permits ordinary edits that keep the group unchanged, but does not authorize moving the monitor into arbitrary groups. Removing group membership requires global destination authority. Bulk operations authorize every requested ID before changing any records; a missing or inaccessible ID prevents the complete operation.

Maintenance mutations require the relevant action over every old and new target. Changing active state through an update additionally requires `maintenance.toggle`; unrelated edits do not. The dedicated toggle endpoint needs only toggle authority. Omitted API `active` and `monitor_ids` preserve current values.

Status-page publication is independent from page administration. Assigning or reordering members, or expanding public exposure, requires monitor-read authority over all affected members. Publishing incidents additionally requires incident-read authority on those members. Metadata-only edits can preserve inaccessible memberships; analytics changes require `super_admin`. See [Status pages](status-pages.html).

## Direct grants and role shorthands {#direct-grants-and-role-shorthands}

`api_keys[].permissions` entries are explicit global grants. They can coexist with role bindings. To restrict a principal, move the relevant permissions into a role binding and remove the corresponding direct grants.

| Configuration | Meaning |
| --- | --- |
| `role: admin` | Shorthand for explicit `super_admin: true`. |
| `role: readonly` | A fixed global read set, listed below. It is broader than the viewer role. |
| `permissions: [...]` | Global action grants. A resource's write umbrella covers only that resource's mutations. |

The `readonly` set is: `monitors.read`, `incidents.read`, `notifications.read`, `notification_history.read`, `escalation_policies.read`, `maintenance.read`, `groups.read`, `tags.read`, `proxies.read`, `agents.read`, `status_pages.read`, `on_call.read`, `request_logs.read`, `audit.read`, `settings.read`, `database.read`, `metrics.read` and `sla.read`. It excludes subscriber contacts. Future resources do not silently expand this set.

Combining the `role` field with `permissions`, `role_bindings` or `super_admin` is rejected. Use explicit grants instead of relying on precedence.

## Privileged configuration and credentials {#privileged-configuration-and-credentials}

`super_admin` is separate from ordinary roles. Configuration import and every configuration export, including redacted exports, require it. Keep a separate bootstrap or recovery principal for those operations; the `administrator` role does not grant them.

Monitor and notification-channel reads mask stored credentials. Supported edits retain blank hidden fields; explicit removal or `clear_secrets` clears them. Ordinary editors changing a credential's recipient must replace or clear retained secrets; super administrators may deliberately retain them. Heartbeat and agent creation return new credentials once; later reads cannot recover them. See the [API reference](api.html) for field-level behavior.

## API and web behavior {#api-and-web-behavior}

The web navigation shows sections for which a principal has a capability, while individual links and actions check the particular object. API collections apply scope before totals and pagination. An inaccessible object is reported as not found. These checks apply independently of whether the web UI is enabled or the application runs under a URL prefix. Live events follow the same principal and object boundaries; public status pages expose only their intentionally published content.
