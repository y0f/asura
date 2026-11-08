---
title: "Access control"
nav_title: "Access control"
description: "Manage Asura API keys, reusable roles and fine-grained permissions. Scope access to monitors and groups, preserve credentials and migrate legacy grants."
permalink: "/access-control.html"
---

# Access control {#access-control}

Asura v2 uses the same authorization policy for API keys and web sessions. A named principal in `auth.api_keys` receives actions through reusable role bindings, explicit global permissions, or the separate `super_admin` flag. Roles and bindings live in `config.yaml`; there is no additional identity service or role database.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## Define a role and bind its scope {#define-a-role-and-bind-its-scope}

```
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

Generate a key and its hash with `asura --setup`. Keep the original key private and put only its hash in the configuration. Replace the example group and monitor IDs with existing IDs from your installation. Restart the server after changing authentication configuration. Existing sessions resolve their principal against the current configuration; removing the principal or rotating its key revokes its previous sessions.

A role has a unique `name`, optional `description`, and nonempty `permissions` list. Custom names start with a lowercase letter and contain at most 64 lowercase letters, digits, underscores or hyphens. Built-in and legacy role names are reserved. Unknown roles, unknown actions, duplicate definitions, invalid IDs and empty scopes fail configuration validation.

## Built-in roles {#built-in-roles}

| Role | Actions | Allowed binding scope |
| --- | --- | --- |
| `viewer` | Read monitors, incidents, SLA reports, request logs, notification history and maintenance windows. | All monitors, specific monitor IDs, or current members of specific groups. |
| `operator` | Viewer actions, monitor pause/resume/manual status, incident acknowledge/resolve/severity, and maintenance create/update/delete/toggle. | All monitors, specific monitor IDs, or current members of specific groups. |
| `administrator` | Every ordinary resource/action permission, including subscriber contacts and administration. | `all: true` only. It does not grant `super_admin`. |

The viewer role excludes unrelated configuration, notification-channel settings, subscriber contacts, audit records and database metadata. The operator role does not grant monitor configuration changes, credential delegation, or incident deletion. Use a custom role when those actions are needed.

## How bindings combine {#how-bindings-combine}

Each binding must explicitly choose `all: true`, or a nonempty combination of `monitor_ids` and `group_ids`. Combining `all: true` with IDs is invalid. IDs and group membership inside a binding are alternatives: either match grants that binding's actions. Group bindings follow the monitor's current membership; an explicit monitor-ID binding follows that specific monitor.

Multiple bindings add grants for the same action. They never transfer an action to another binding's objects. For example, `monitors.read` on monitor 10 and `monitors.update` on monitor 20 allow reading 10 and updating 20; they do not allow updating 10 or reading 20. Operations requiring multiple actions must satisfy every action on the affected object. There is no role inheritance, implicit read grant or deny-rule precedence.

`resource.write` is an umbrella for that resource's mutation actions only. For example, `monitors.write` includes monitor creation, editing, deletion, pause/resume, manual status, cloning and assignment. It grants neither `monitors.read` nor permissions on groups, channels, agents or other resources. Unknown actions and wildcards such as `*` or `monitors.*` are rejected.

## Which resources can be scoped {#which-resources-can-be-scoped}

Monitor and group selectors apply to `monitors`, `incidents`, `sla`, `request_logs`, `notification_history` and `maintenance`. Incidents, reports and log/history entries use the monitor they belong to. Entries without an associated monitor require a global grant. A maintenance window requires authority over every monitor it affects; an all-monitor window requires a global grant.

Other resources require `all: true`, including group administration, tags, notification channels, escalation policies, proxies, agents, status-page administration, subscriber contacts, on-call schedules, audit records, settings, database operations and aggregate metrics. A group selector grants access to its member monitors, not to administration of that group. A role containing any global-only permission cannot be bound to monitor or group IDs; split it into separate roles and bindings.

Related resource access is independent. A monitor permission does not grant channel secrets, proxy settings, agent credentials, subscriber contacts or unrelated nested names. Necessary relationship IDs may remain visible without the related resource's read permission. Changing a relationship requires the appropriate assignment authority and access to the selected related resource; unrelated edits preserve inaccessible relationships.

## Creation, cloning and moving monitors {#creation-cloning-and-moving-monitors}

A new monitor has no existing ID to match. Creating it therefore requires `monitors.create` for its destination group, or a global grant. Creating an ungrouped monitor requires a global grant. Cloning requires source read and clone authority, plus clone authority for the destination group; an ID-only clone grant cannot create another object.

Changing a monitor's group requires update and assignment authority over the current monitor and authority over the destination group. An ID-only grant permits ordinary edits that keep the group unchanged, but does not authorize moving the monitor into arbitrary groups. Removing group membership requires global destination authority. Bulk operations authorize every requested ID before changing any records; a missing or inaccessible ID prevents the complete operation.

## Global compatibility grants and migration {#global-compatibility-grants-and-migration}

Existing `api_keys[].permissions` entries remain explicit global grants. They can coexist with role bindings, and their global reach is unchanged. To restrict an existing principal, move the relevant permissions into a role binding and remove the corresponding direct grants.

| Legacy configuration | V2 interpretation |
| --- | --- |
| `role: admin` | Converted to explicit `super_admin: true` during configuration validation. |
| `role: readonly` | Preserves the legacy global read set listed below. It is broader than the new viewer role. |
| `permissions: [...]` | Global action grants. A resource's write umbrella covers only that resource's mutations. |

The frozen legacy readonly set is: `monitors.read`, `incidents.read`, `notifications.read`, `notification_history.read`, `escalation_policies.read`, `maintenance.read`, `groups.read`, `tags.read`, `proxies.read`, `agents.read`, `status_pages.read`, `on_call.read`, `request_logs.read`, `audit.read`, `settings.read`, `database.read`, `metrics.read` and `sla.read`. It excludes subscriber contacts. Future resources do not silently expand this set.

Combining the legacy `role` field with `permissions`, `role_bindings` or `super_admin` is rejected. Replace the legacy field with explicit grants instead of relying on precedence. See [Upgrading](upgrading.html) for the broader v2 permission migration and configuration backup steps.

## Privileged configuration and credentials {#privileged-configuration-and-credentials}

`super_admin` is a separate, explicit authority. Ordinary roles cannot grant it. Configuration import and full exports containing stored credentials require this authority. Keep a separate bootstrap or recovery principal for those operations. An ordinary administrator role can administer resources but cannot obtain a full secret backup.

Ordinary monitor and notification-channel reads mask stored credential fields. A hidden field left blank during a supported edit retains its saved value; explicit removal or the API's `clear_secrets` option clears it. Retaining hidden credentials while changing their recipient requires replacement or explicit clearing. Creating a heartbeat or agent can return a new credential once; later read permissions do not recover it. See the [API reference](api.html) for field-level behavior.

## API and web behavior {#api-and-web-behavior}

The web navigation shows sections for which a principal has a capability, while individual links and actions check the particular object. API collections apply scope before totals and pagination. An inaccessible object is reported as not found. These checks apply independently of whether the web UI is enabled or the application runs under a URL prefix. Live events follow the same principal and object boundaries; public status pages expose only their intentionally published content.
