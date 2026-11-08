---
title: "Escalation policies"
nav_title: "Escalation policies"
description: "Escalate unresolved Asura incidents with timed notification steps. Configure policy delays, repeats and monitor assignments."
permalink: "/escalation-policies.html"
---

# Escalation Policies {#escalation-policies}

Escalation policies define time-based notification chains for incidents. When an incident is created on a monitor that has an escalation policy assigned, notifications are sent to different channels at increasing delay intervals until the incident is acknowledged or resolved.

<details class="contents">
<summary>On this page</summary>
<div markdown="1">

* Contents
{:toc}

</div>
</details>

## How it works {#how-it-works}

1. A monitor goes down and an incident is created
2. If the monitor has an escalation policy, Asura starts the escalation chain
3. Due steps queue notifications to their enabled channels
4. When the incident is acknowledged or resolved, the escalation stops
5. If **repeat** is enabled, the chain schedules step 1 again after the last step

The runner checks due steps every 30 seconds. The first delay starts when the chain is created; each later delay starts after the previous step's notification admission succeeds. A zero delay makes a step due on the next runner tick, rather than sending synchronously. Repeating chains wait at least five minutes, or the first step's configured delay if longer, before starting another cycle.

Escalation notifications use `incident.escalated` and bypass channel event filters and the monitor's ordinary channel selection. Disabled channels still receive nothing. Active maintenance holds the step, and a failed maintenance lookup also leaves it pending until a later successful lookup.

A notification admission failure retains the step for retry. If only some recipients were admitted, those recipients may receive the repeated step too. Queue admission is not proof of delivery; the bounded, in-memory dispatcher does not provide durable or exactly-once delivery. See [delivery behavior](notifications.html#delivery-and-shutdown).

## Creating a policy {#creating-a-policy}

In the web UI, navigate to **Escalation Policies** in the sidebar. Click **New Policy** to create one.

Each policy has:

- **Name** : a descriptive label
- **Description** : optional notes
- **Enabled** : toggle the policy on or off
- **Repeat** : restart from step 1 after the last step
- **Steps** : ordered list of notification escalations

Each step specifies a **delay in minutes** (0 = next runner tick) and one or more **notification channels** to send to.

## Assigning to a monitor {#assigning-to-a-monitor}

When creating or editing a monitor, select an escalation policy from the **Escalation Policy** dropdown. Only enabled policies with at least one step will trigger escalation.

## API {#api}

### List policies {#list-policies}

```
GET /api/v1/escalation-policies
```

### Get policy {#get-policy}

```
GET /api/v1/escalation-policies/{id}
```

### Create policy {#create-policy}

```
POST /api/v1/escalation-policies

{
  "name": "Critical Alert Chain",
  "description": "3-step escalation for production monitors",
  "enabled": true,
  "repeat": false,
  "steps": [
    { "delay_minutes": 0, "notification_channel_ids": [1] },
    { "delay_minutes": 5, "notification_channel_ids": [1, 2] },
    { "delay_minutes": 15, "notification_channel_ids": [1, 2, 3] }
  ]
}
```

### Update policy {#update-policy}

```
PUT /api/v1/escalation-policies/{id}
```

The same fields are available as on create. Omitted fields are preserved. A name, description, enabled or repeat edit does not require access to channel assignments when `steps` is omitted.

### Delete policy {#delete-policy}

```
DELETE /api/v1/escalation-policies/{id}
```

Monitors referencing a deleted policy will have their escalation policy set to null.

## Permissions {#permissions}

Escalation policies use dedicated permissions:

- `escalation_policies.read` : view policies (included in the `readonly` role)
- `escalation_policies.create`, `escalation_policies.update`, `escalation_policies.delete`: individual mutations
- `escalation_policies.write`: all three mutations; does not grant read access

Creating a policy or changing its steps also requires `notifications.read`, because it assigns notification channels. Explicitly unchanged steps do not need new assignment authority. A policy read without that permission uses channel IDs rather than names. Assigning a policy to a monitor requires `monitors.assign` on that monitor and `escalation_policies.read`.
