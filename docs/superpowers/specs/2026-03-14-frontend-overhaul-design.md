# Frontend Architecture Overhaul

## Problem

The frontend has grown organically to 19 templ files (~13,500 lines) with significant duplication, inconsistent patterns, and UX issues that make the product feel unpolished.

### Key Issues

1. **12 native `confirm()` dialogs** — ugly browser popups for every delete
2. **Flash messages don't distinguish types** — errors show green like successes, auto-dismiss too fast for errors
3. **No shared components** — modals, delete buttons, stat cards, filter tabs copy-pasted everywhere
4. **Modal inconsistencies** — some have X buttons, some don't; different transitions/widths/overflow
5. **Sidebar is flat** — 13 items with no grouping, overwhelming for new users
6. **Monitor form is 818 lines** — all in one file, settings + assertions + config all crammed together
7. **No inline validation** — errors go to flash, user loses context of which field failed
8. **No loading states** — submit buttons don't indicate progress

## Design

### 1. Shared Components (`components.templ`)

New file with reusable templ components that replace copy-pasted patterns:

**ConfirmModal** — Single Alpine.js component in layout, triggered via `$dispatch`:
```
@click="$dispatch('confirm', {url: '/delete/123', msg: 'Delete this monitor?'})"
```
- Red destructive action button + Cancel
- Escape key closes
- Focus trap
- Replaces all 12 `confirm()` calls

**Modal** — Reusable modal wrapper:
```
@Modal("showForm", "Create Group") { ...form content... }
```
- Consistent backdrop (`bg-black/50`), max-width, overflow handling
- X close button always present
- Escape to close
- `x-transition` with consistent timing

**Toast** (replaces FlashMessage) — 3 variants:
- Success (green) — auto-dismiss 4s
- Error (red) — persists until dismissed
- Warning (amber) — auto-dismiss 6s
- Slide-in from top-right, stacks if multiple

Backend change: `setFlash(w, msg)` becomes `setToast(w, "success"|"error", msg)` using cookie format `type:message`.

**StatCard** — Reusable stat card:
```
@StatCard("Up", "42", "text-emerald-400")
```
Replaces 3 separate implementations in dashboard, logs, SLA.

**FilterTab** — Reusable filter tab button:
```
@FilterTab(href, isActive, "All")
```

**DeleteButton** — Reusable delete action that dispatches confirm:
```
@DeleteButton("/monitors/5/delete", "Delete this monitor?")
```

**EmptyState** — Consistent empty state display:
```
@EmptyState("No monitors configured", basePath + "/monitors/new", "Create one")
```

### 2. Sidebar Reorganization

Current: 13 flat items. New: 4 collapsible groups.

```
Dashboard
── Monitoring ──
  Monitors
  Groups
  Tags
── Alerting ──
  Incidents
  Notifications
  Escalation
── Operations ──
  SLA Report
  Maintenance
  Status Pages
── System ──
  Logs
  Audit
  Proxies
  Settings
```

- Group headers are small uppercase text, non-clickable
- Groups default to expanded (no Alpine state needed — just visual grouping)
- Active page highlighted within its group
- Reduces cognitive load without hiding anything

### 3. Monitor Form Improvements

**Structure: Split into logical sections with clear card boundaries:**

Card 1 — **Identity** (always visible):
- Name, Description, Type dropdown, Target
- Type changes dynamically show/hide Target and placeholder

Card 2 — **Schedule** (always visible):
- Interval, Timeout, Failure Threshold, Success Threshold
- Laid out as a 4-column grid with sensible defaults shown

Card 3 — **Type-Specific Settings** (shown based on type):
- HTTP: Method, Expected Status, Body, Headers, Auth, mTLS, Redirects, TLS options
- TCP/DNS/TLS/WS/Command/Docker/Domain/gRPC/MQTT: each in their own section
- Hidden entirely for Heartbeat and Manual types

Card 4 — **Conditions** (collapsible, starts collapsed if empty):
- Group-based AND/OR conditions
- Form mode default, JSON toggle for power users

Card 5 — **Routing & Behavior** (collapsible "Advanced"):
- Group, Tags, Proxy, Notification Channels, Escalation Policy
- SLA Target, Track Changes, Upside-Down Mode, Resend Interval
- These are secondary settings most users won't touch on first create

**UX improvements:**
- Submit button shows loading spinner + disabled state during POST
- Inline validation: required fields show red border + error text on submit attempt
- "Cancel" button always visible at bottom

### 4. Toast System Implementation

Cookie format change:
```
Current:  flash=url_encoded_message
New:      toast=success:url_encoded_message
          toast=error:url_encoded_message
```

Layout reads cookie, parses type prefix, renders appropriate variant:
- Success: green border, emerald icon, auto-dismiss 4s
- Error: red border, X icon, persists until user clicks dismiss
- Warning: amber border, triangle icon, auto-dismiss 6s

Position: top-right fixed, stacking with `flex-col gap-2`.

### 5. Form Loading States

All submit buttons get a consistent pattern:
```html
<button type="submit" class="btn-primary" x-data="{loading:false}"
  @click="loading=true" :disabled="loading"
  :class="loading && 'opacity-60 cursor-wait'">
  <span x-show="!loading">Create</span>
  <span x-show="loading">Saving...</span>
</button>
```

### 6. Files Changed

**New files:**
- `internal/web/views/components.templ` — shared components

**Modified files:**
- `internal/web/views/layout.templ` — sidebar groups, toast system, confirm modal
- `internal/web/views/monitorform.templ` — restructured cards, loading state
- `internal/web/views/monitors.templ` — use DeleteButton, components
- `internal/web/views/incidents.templ` — use DeleteButton, components
- `internal/web/views/notifications.templ` — use Modal, DeleteButton
- `internal/web/views/groups.templ` — use Modal, DeleteButton, EmptyState
- `internal/web/views/tags.templ` — use Modal, DeleteButton, EmptyState
- `internal/web/views/escalation_policies.templ` — use Modal, DeleteButton
- `internal/web/views/maintenance.templ` — use Modal, DeleteButton
- `internal/web/views/proxies.templ` — use DeleteButton
- `internal/web/views/settings.templ` — use confirm dispatch for VACUUM
- `internal/web/views/status.templ` — use DeleteButton
- `internal/web/views/dashboard.templ` — use StatCard
- `internal/web/views/sla_report.templ` — use StatCard
- `internal/web/views/requestlogs.templ` — use StatCard, FilterTab
- `internal/web/views/audit.templ` — use FilterTab
- `internal/web/handler.go` — toast cookie parsing
- `web/static/tailwind.css` — rebuild after template changes

### 7. What's NOT changing

- Backend logic, API endpoints, storage layer — untouched
- Dark/light theme system — untouched
- HTMX polling patterns — untouched
- Alpine.js as the framework — stays, just better organized
- Public status page template — untouched (separate concern)
