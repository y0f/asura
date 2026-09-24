# Frontend audit and rework plan

Tracking issue: #73 ("Frontend rework").

This document audits the web UI as it exists on `main` at `38a51b5`. No code was changed.
It covers three things: what the frontend is today, every concrete quality problem found,
and an ordered rework plan in which each step ships on its own with a passing build.

The JSON API (`/api/v1/*`) is out of scope. The issue calls it solid, and the web UI does not
consume it (see §1.5). Line numbers refer to `38a51b5`.

**Baseline.** `templ generate` produces no diff against the committed `*_templ.go` files, and
`go build ./...`, `go vet ./internal/web/...` and `go test ./internal/web/...` all pass. Every
step below has to keep that true.

---

## 1. What the frontend is today

### 1.1 Stack

| Layer | Choice | Where |
|---|---|---|
| Rendering | Server-side Go templates with [templ](https://templ.guide) v0.3.1020. Generated `*_templ.go` files are committed. | `internal/web/views/*.templ` (23 files, ~5,900 lines) |
| Handlers | `net/http` handlers on `*web.Handler`. They read and write `storage.Store` directly. | `internal/web/*.go` (~4,300 lines) |
| Interactivity | Alpine.js 3.14.3 (standard build, not the CSP build) plus htmx 2.0.4 | `web/static/alpine.min.js`, `htmx.min.js` |
| Charts | uPlot 1.6.31 (response-time chart only) plus hand-built SVG strings from Go (sparklines, heatmap, uptime bars) | `web/static/uplot.iife.min.js`, `views/helpers.go`, `views/dashboard.templ` |
| Styling | Tailwind CSS v4 standalone CLI (no Node) with three layers: `tokens.css`, `base.css`, `components.css`. Output is committed. | `web/tailwind.input.css`, `web/css/*`, `web/static/tailwind.css` (80 KB) |
| Page JS | Three small files: `monitor-chart.js`, `notifications-form.js`, `sse-refresh.js`. Everything else is inline `<script>` or JS written inside Go strings. | `web/static/*.js`, `views/*.templ`, `views/*.go` |
| Assets | Embedded in the binary with `//go:embed static/*` and served with `Cache-Control: max-age=604800` | `web/embed.go`, `internal/server/routes.go:28-34` |
| Fonts | Inter and JetBrains Mono, self-hosted as woff2 | `web/static/fonts/` |
| Tests | Handler tests for monitors and notifications. No tests render views. | `internal/web/*_test.go` |

### 1.2 Structure

```
internal/web/
  handler.go            Handler, layout params, toast cookie, renderComponent (sets CSP)
  auth.go               login, TOTP, session cookie, RequireAuth / RequirePerm
  monitors.go           list/detail/form + 16 per-type settings assemblers (1,128 lines)
  <entity>.go           one file per entity (incidents, groups, tags, notifications, …)
  views/
    layout.templ        app shell: sidebar, topbar, inline shell/boost/scroll scripts
    components.templ    ConfirmModal, Toast, DeleteButton, EmptyState(+Modal), FormModal,
                        StatusPill, SeverityPill, ToolbarNewButton, SearchInput, IncidentRow,
                        MonitorRowActions, MonitorListListener, Setting* field helpers
    search.templ        command palette (client-side filter over a static nav list)
    helpers.go          status colours, formatting, markdown renderer, SVG builders
    <page>.templ        one per page; statuspage.templ is the public page (its own <html>)
web/
  tailwind.input.css    entry point plus a runtime safelist of status classes
  css/tokens.css        colour ramp, type scale, radii; dark default, light remap
  css/base.css          element styles, focus ring, scrollbars, motion, light-mode overrides
  css/components.css    card/panel, form-*, btn-*, badge-*, switch, filter-tab, cmdk
  static/               built CSS, vendored JS, page JS, fonts, logo.gif, favicon.ico
```

### 1.3 Routes (web UI)

All routes are registered in `internal/server/routes.go:36-130`. Reads require a session. Writes
also require a permission.

| Area | Pages (GET) | Mutations (POST) | Create/edit pattern |
|---|---|---|---|
| Auth | `/login`, `/login/totp` | `/login`, `/login/totp`, `/logout` | own `<html>` pages |
| Dashboard | `/` (`?type=&page=`) | none | none |
| Monitors | `/monitors`, `/monitors/new`, `/monitors/{id}`, `/monitors/{id}/edit` | create, update, delete, pause, resume, clone, status, bulk | dedicated form page |
| Chart data | `/monitors/{id}/chart` (JSON, served by the API handler) | none | none |
| Incidents | `/incidents`, `/incidents/{id}` | ack, resolve, delete | none |
| Groups | `/groups`, `/groups/{id}` | create, update, delete | modal |
| Tags | `/tags` | create, update, delete | modal |
| Notifications | `/notifications`, `/notifications/history` | create, update, delete, test | modal |
| Escalation | `/escalation-policies` | create, update, delete | modal |
| On-call | `/on-call` | create, delete, override | modal (create only) |
| SLA | `/sla`, `/sla/export` | none | none |
| Maintenance | `/maintenance` | create, delete, toggle (update route exists, no UI) | modal (create only) |
| Logs / Audit | `/logs`, `/audit` | none | none |
| Agents | `/agents` | create, delete | modal (create only) |
| Proxies | `/proxies`, `/proxies/new`, `/proxies/{id}/edit` | create, update, delete | dedicated form page |
| Status pages | `/status-pages`, `/new`, `/{id}/edit` | create, update, delete | dedicated form page |
| Settings | `/settings`, `/settings/export` | import, vacuum | inline forms |
| Live updates | `/events` (SSE) | none | none |
| Public | `/{slug}`, `/{slug}/rss`, `/{slug}/auth`, `/{slug}/subscribe` | auth, subscribe | own `<html>` |

### 1.4 Navigation and state model

- **Navigation.** Pages are full server renders. `hx-boost` is set only on the sidebar `<nav>`
  (`layout.templ:242`), which swaps `#content` and re-syncs the title and active link
  (`boostScript`, `layout.templ:124-160`). Every other link, filter, pager, form and the command
  palette (`search.templ:134`) triggers a full page load.
- **Mutations.** Plain HTML form POSTs followed by a 303 redirect. The result is passed back in a
  `toast` cookie (`kind:message`, 5 s `MaxAge`, `handler.go:123-133`), which `newLayoutParams`
  reads and `Layout` renders. The monitor form re-renders in place on a validation error. Modal
  forms always redirect.
- **Server state in the URL.** Filters, pagination and time ranges live in the query string. Each
  page builds its own query strings (§2.3).
- **Client state.**
  - Alpine `x-data` per page. Most of these objects are JavaScript assembled inside Go string
    functions such as `monitorFormXData`, `tagAlpineData`, `groupAlpineData` and
    `escalationXData`.
  - `localStorage` holds `theme` and `navc` (collapsed nav). `sessionStorage` holds
    `sidenav.scroll`.
  - One global, `window.__asuraSSE`.
- **Live data.** The dashboard grid, monitor list and monitor detail re-fetch themselves
  (`hx-get` on the same URL with `hx-select`) on `every 30s` and on an `sse:refresh` event. That
  event comes from `sse-refresh.js`, which listens on `/events`. The SSE connection closes for
  good on its first error (`sse-refresh.js:16`). After that the page relies on polling alone.
- **Permissions.** `LayoutParams.Perms` (`map[string]bool`) is checked in templates to hide write
  controls. Not all pages check it (§2.1 F-10).

### 1.5 API calls from the browser

The browser does not call `/api/v1`. It makes only these requests:

1. `GET /monitors/{id}/chart?range=…` from `monitor-chart.js`, which returns JSON points.
2. `EventSource('/events')` from `sse-refresh.js`.
3. htmx GETs of the current page URL to refresh the lists above.
4. HTML form POSTs to the routes in §1.3.

Handlers read storage directly. The `web` package and the `api` package therefore each have their
own copy of logic such as the overall status (`httputil.OverallStatus` is shared) and SLA
formatting. The rework keeps this model: it is simple, fast, and needs no JS build.

---

## 2. Problems found

Severity levels:

- **H**: wrong behaviour, data loss or a security-relevant issue.
- **M**: a real usability, accessibility or maintainability cost.
- **L**: polish.

### 2.1 Correctness bugs

| ID | Sev | Problem | Where |
|---|---|---|---|
| F-01 | H | **Errors are shown as green success toasts that disappear after 4 s.** `setFlash` always sends `kind=success`. About 49 of its 116 call sites pass failure text ("Failed to create…", "Invalid import mode", "Name is required", "Vacuum failed: …"). `setToast(w, "error", …)` is never called. The error variant built for the March overhaul is unreachable from redirects. | `internal/web/handler.go:135`; e.g. `agents.go:27,34`, `export.go:45,63,88,103`, `escalation_policies.go:49,80` |
| F-02 | H | **The agent token is shown once, in a toast that auto-dismisses after 4 s** (its cookie lives 5 s). If the user misses it, they have to delete and recreate the agent. | `internal/web/agents.go:39`, `views/components.templ:92-101` |
| F-03 | H | **HTTP multi-step monitors can't be configured in the form, and saving one erases its steps.** "HTTP Multi-Step" appears in the type picker, but no settings template exists. `_settingsAssemblers` has no `http_multi` entry, so `assembleSettings` returns `nil` in form mode and `MonitorUpdate` writes `settings = NULL`. `validate.ValidateMonitor` has no check for steps, so the save succeeds silently. | `views/monitorform.templ:191,271-287`; `internal/web/monitors.go:59-76` vs `137-…`, `247-252` |
| F-04 | H | **Invalid JSON in "Advanced (JSON)" mode is dropped silently.** `parseJSONOrForm` returns `nil` instead of an error. For settings this erases the monitor's configuration; for assertions it erases every condition. | `internal/web/monitors.go:1110-1118` |
| F-05 | M | **The response chart initialises twice.** `x-data="monitorChart()"` has an `init()` that Alpine 3 calls automatically, and `x-init="init()"` calls it again. The result is two chart fetches, two `ResizeObserver`s and two `theme-changed` listeners, and `destroy()` only cleans up one of each. | `views/monitors.templ:611`, `web/static/monitor-chart.js:85-94` |
| F-06 | M | **Wrong empty state when filters match nothing.** The empty-state check looks only at search and type. Filtering by status, group or tag with no results shows "No monitors yet / New Monitor". The "Clear" link has the opposite gap: it appears for search, status, group and sort, but not for type or tag. | `views/monitors.templ:274,414` |
| F-07 | M | **Filter links drop other filters and don't escape values.** `filterHref` keeps only `q`, and `tagFilterHref` keeps `q` and `type`. Status, group and sort are lost when a pill is clicked. All 12 `*Href` builders concatenate raw strings, so a search such as `a&b` or `x#y`, or an API key name with spaces, breaks pagination. | `views/monitors.templ:83-130`, `incidents.templ:30-54`, `audit.templ:24-51`, `requestlogs.templ:27-70`, `notifhistory.templ:24-36`, `dashboard.templ:75-81` |
| F-08 | M | **The group detail page has no pager.** `GroupDetailParams.Monitors` is a `PaginatedResult`, but no pager is rendered, so monitors past page 1 can't be reached. | `views/groups.templ:124-191` |
| F-09 | M | **Some entities can be created but never edited in the UI.** Maintenance windows have `POST /maintenance/{id}` wired, but no edit UI. On-call rotations have no edit route, although the API has `PUT`. Agents can't be renamed. Maintenance monitors are chosen by typing raw IDs ("1, 2, 3"). | `views/maintenance.templ:95-136`, `oncall.templ`, `agents.templ` |
| F-10 | M | **Write controls are shown to read-only users** and lead to a 403. Examples: dashboard "+ Add" and the empty-state "New Monitor"; Status Pages New, Edit and Delete (no `Perms` check at all); Settings Export, Import and Vacuum; the global `n` shortcut. | `views/dashboard.templ:116,168`, `status.templ:27,67,70`, `settings.templ`, `layout.templ:77-82` |
| F-11 | M | **Auto-refresh discards input in progress.** The manual-status message box sits inside `#monitor-detail`, which is swapped every 30 s, so typed text disappears. The "Refreshing…" and "Refresh failed" indicators are positioned `absolute top-0 right-0` on top of the header's action buttons. | `views/monitors.templ:430-437,494-507` |
| F-12 | H | **Stored secrets are rendered into the HTML.** The monitor form pre-fills `value=` for the basic-auth password, bearer token, OAuth2 client secret, mTLS private key, and MQTT and Redis passwords. The proxy form pre-fills `auth_pass`. The notifications page embeds every channel's full settings (bot tokens, SMTP password, PagerDuty/Opsgenie keys, webhook secrets) in `@click="editChannel({…})"` for each card. Several tokens also use `type="text"`, so they show on screen. | `views/monitorform.templ:501-546,667`, `proxies.templ:114`, `notifications.templ:53` and `notif*Fields` |
| F-13 | M | **Most static assets are never cache-busted.** Only the app shell's `tailwind.css` gets `?v=<hash>`. The login, TOTP, public status, subscribe and status-auth pages link `tailwind.css` without it. `uplot.min.css` and every JS file (`htmx`, `alpine`, `uplot`, `monitor-chart.js`, `notifications-form.js`, `sse-refresh.js`) are also unversioned. All of them are served with `max-age=604800`, so an upgrade can run with week-old CSS or JS. | `views/layout.templ:35,53-55`, `auth.templ:23`, `statuspage.templ:65`, `server/routes.go:32` |
| F-14 | M | **CI doesn't rebuild or check the committed CSS reliably.** The rebuild step only fires when a changed file matches `\.templ$\|tailwind\.input\.css$\|\.html$`. Edits to `web/css/*.css`, or to class names in `views/*.go` (which `@source` scans), therefore never rebuild `tailwind.css`. Pull requests never build the CSS, so drift is only discovered after merge. | `.github/workflows/ci.yml:24-31` |
| F-15 | M | **The public status page overstates outages and uses internal wording.** A single `down` monitor out of twenty shows "Major System Outage". Visitors see raw `up`/`down`/`paused` pills. An empty page shows the admin empty state ("No monitors yet" with a folder icon). | `views/statuspage.templ:142,286-314,322`; `internal/httputil/httputil.go:230-241` |
| F-16 | M | **Status and uptime thresholds disagree with each other.** SLA "At Risk" means under 10 % of the budget left in `slaStatusBadge`, but the budget turns yellow at 25 % in `slaBudgetColor`. There are four uptime threshold sets: `UptimeColor` (99.9/99), `UptimeBarFill` (99/95), `heatmapColor` (99.995/99/95) and the monitor detail SLA badge (10 %). The heatmap legend uses `bg-yellow-400` (#facc15), but the cells are drawn in #fbbf24. | `views/sla_report.templ:35-63`, `helpers.go:130-151,566-580`, `monitors.templ:548-551,566` |
| F-17 | M | **"Replace (overwrite all)" import runs without confirmation**, while VACUUM, which is harmless, does ask. | `views/settings.templ:62-83` |
| F-18 | L | **Small status and type mismatches.** A paused monitor shows both a `paused` status pill and a "Paused" badge. `showPercentiles` and the "Code" column have hard-coded type lists that leave out `http_multi`, `grpc`, `smtp` and others. The target placeholder map covers 11 of 17 targeted types; `smtp`, `ssh`, `redis`, `postgresql`, `udp` and `http_multi` fall back to `https://example.com`. | `views/monitors.templ:196-202,446-449,739`, `monitorform.templ:223` |
| F-19 | L | **The command palette promises search it doesn't do.** Its placeholder says "Search monitors, incidents, settings…", but it filters a hard-coded list of 18 page links. | `views/search.templ:37,147-173` |
| F-20 | L | **`capitalize` is fragile.** It computes `s[0]-32`, which corrupts any input that doesn't start with a lowercase ASCII letter. It's only safe because today's inputs are fixed. | `views/requestlogs.templ:186-191` |

### 2.2 Inconsistent components

The March spec (`docs/superpowers/specs/2026-03-14-frontend-overhaul-design.md`) was only
partly implemented. `StatCard`, `FilterTab`, loading states and inline validation never landed,
and several parallel implementations remain.

| ID | Sev | Problem | Where |
|---|---|---|---|
| C-01 | M | **Stat cards are copy-pasted about 20 times** (25 `stat-label` uses) with different padding (`py-2.5` vs `py-3`), value colours (`text-white` vs `text-muted-light` for the same kind of number) and grids. | `dashboard.templ:86-111`, `monitors.templ:516-606`, `sla_report.templ:96-109`, `requestlogs.templ:80-93` |
| C-02 | M | **There are two pill systems.** `.badge` is CSS-defined, `rounded-full` and uppercase with an 11 px literal. `StatusPill` and `SeverityPill` are `rounded-md` with classes from `helpers.go`. Both appear in the same monitor table row. | `components.css:148-165`, `components.templ:186-194`, `monitors.templ:379-382` |
| C-03 | M | **The 32 px icon action button is implemented four ways:** the `row-action` utility, the inline classes in `DeleteButton`, the inline classes repeated four times in `MonitorRowActions`, and the inline classes in the status-pages list. `icon-btn` is a fifth size. Two different "edit" glyphs are in use. | `components.templ:103-115,231-257`, `status.templ:63-69`, `components.css:134-146` |
| C-04 | M | **Page headers and back links vary.** Detail pages render their own `<h1>` (`text-xl`, `text-lg` or `text-md` depending on the page) under a topbar `<h1>` that already shows the same title. The result is two `<h1>`s per page, which also breaks the README rule "pages don't repeat [the title]". There are four back-link styles: chevron breadcrumb, icon-only, a `&lt; Channels` text link on the right, and none. | `layout.templ:345`, `monitors.templ:440-445`, `incidents.templ:170-175`, `groups.templ:129-133`, `notifhistory.templ:42`, `status.templ:89` |
| C-05 | M | **CRUD flows follow three patterns without a rule:** a dedicated page (monitors, proxies, status pages), an edit modal (tags, groups, notifications, escalation), and a create-only modal (maintenance, on-call, agents). Form widths differ too: `max-w-3xl mx-auto` in cards, `max-w-2xl` left-aligned, and full-width without a card. Submit labels vary: Create, Update, Save, "Update Status Page". | `monitorform.templ:177`, `proxies.templ:84`, `status.templ:97`, `escalation_policies.templ:138` |
| C-06 | M | **"New X" buttons:** a `ToolbarNewButton` component exists but is used 3 times. Seven pages hand-roll the same button, with `w-3` or `w-3.5` icons. | `components.templ:198-203`; `tags/groups/notifications/escalation/maintenance/oncall/agents.templ` |
| C-07 | M | **Empty states:** `EmptyState` and `EmptyStateModal` are near-duplicates that share one folder icon, whether the list is incidents, logs or audit entries. There are also about 8 ad-hoc `px-4 py-10 text-center text-xs text-muted` blocks. | `components.templ:117-147`; `dashboard.templ:164-169`, `monitors.templ:800`, `incidents.templ:225`, `monitorform.templ:692`, `status.templ:181` |
| C-08 | L | **The same pagination footer markup is repeated 8 times.** | every `PrevPageBtn` call site |
| C-09 | M | **Filter bars mix three widgets:** link pills, `onchange="this.form.submit()"` selects (9 of them, no submit fallback, no label), and `SearchInput` (fixed `w-56`). Incident status pills have their own colours (`incFilterClass`). Time ranges differ from page to page: `1h/12h/24h/7d`, `24h/7d/30d` and `1h/6h/24h/7d/30d`. | `monitors.templ:247-299`, `incidents.templ:56-91`, `audit.templ`, `requestlogs.templ`, `notifhistory.templ` |
| C-10 | L | **Hint text:** the `form-hint` utility is used twice, while `text-2xs text-muted mt-1` is hand-written 21 times. Labels come in `form-label`, `form-label-sm` and inline `text-xs text-muted-light` for checkboxes. | `components.css:22-25`, `monitorform.templ` |
| C-11 | L | **Focus styles:** `base.css` draws a box-shadow ring, and 11 elements add a different `focus-visible:ring-2 focus-visible:ring-brand/50`. `btn-press` is added by hand 69 times, although every `btn-*` variant already has an `:active` scale. | `base.css:80-90`, `components.css:114-131`, `monitorform.templ` |
| C-12 | L | **Naming differs between the sidebar, page titles and the palette:** "Logs" vs "Request Logs", "Audit" vs "Audit Log", "Escalation" vs "Escalation Policies". The status page form is titled "Status Page" for both new and edit. | `layout.templ:266-289`, handlers |

### 2.3 Duplicated logic and sources of truth

| ID | Sev | Problem | Where |
|---|---|---|---|
| D-01 | M | **Monitor types are listed in five places.** `monitorTypes` (19), `dashFilterTypes` (12, with abbreviations like "HB", "CMD", "DK" and "WS"), the `TypeLabel` switch, the form `<optgroup>`s, and the placeholder map (11). They have already drifted apart (F-18, D-01). | `monitors.templ:132`, `dashboard.templ:69-73`, `helpers.go:223-266`, `monitorform.templ:188-223` |
| D-02 | M | **Notification event names are listed in four places**, and `sse-refresh.js` has a fifth, partly overlapping list. | `notifications-form.js`, `notifications.templ:120-146`, `notifhistory.templ:58`, `sse-refresh.js:12` |
| D-03 | M | **Navigation is defined twice:** the sidebar markup and `navIndexJSON` for the palette. Audit filter lists are hard-coded and leave out `login_success`, `login_failed` and `logout`, which `auditBadgeVariant` handles, plus entities such as escalation policies, on-call and agents. | `layout.templ:242-301`, `search.templ:147-173`, `audit.templ:262-270` |
| D-04 | M | **Status colour logic is spread over seven functions and scripts:** `StatusColor`, `StatusBg`, `StatusDot`, `sparklineSVG` hex, `heatmapColor` hex, `UptimeBarFill` hex and `monitor-chart.js` hex/rgba. `paused` uses the same yellow as `degraded`, so a paused monitor looks like a problem. | `helpers.go:15-58,130-151,566-580`, `dashboard.templ:29-62`, `monitor-chart.js:45,55` |
| D-05 | M | **Alpine CRUD state is re-implemented five times** as JS inside Go strings, each with its own conventions: `editId` of `0` vs `null`, a getter vs a mutated `formAction`, and `basePath` escaped (`JSEscapeString`) vs not (`escalationXData` uses `%s`). This code can't be linted, and escaping is done by hand. | `tags.templ:14-36`, `groups.templ:101-122`, `escalation_policies.templ:149-188`, `notifications-form.js`, inline `x-data` in maintenance, on-call and agents |
| D-06 | L | **The theme bootstrap script is copied into three `<head>`s.** `[x-cloak]` is defined three times and `::selection` three different ways. The pre-paint collapsed-nav CSS appears in both `base.css:50-54` and an inline `<style>` in `layout.templ:40-52`, and the two copies differ. | `layout.templ:29,40-52`, `auth.templ:15-24`, `statuspage.templ:57-67,83-88` |
| D-07 | L | **Go handler duplication:** the 5-query "load form dependencies" block appears in all four monitor create/update error paths. | `internal/web/monitors.go:644-700,726-…` |
| D-08 | L | **The docs site keeps a hand-maintained copy of the theme tokens**, which will drift from the app. | `docs/tailwind.input.css` |

### 2.4 Accessibility gaps

| ID | Sev | Problem | Where |
|---|---|---|---|
| A-01 | M | **There's no skip link, and detail pages have two `<h1>`s** (C-04). | `layout.templ` |
| A-02 | M | **Mobile drawer:** the closed sidebar is moved off-screen with `-translate-x-full`, but it stays in the tab order (no `inert` or `visibility`). The open drawer has no focus trap and no Escape handling. | `layout.templ:192-195` |
| A-03 | M | **Dialogs** (`ConfirmModal`, `FormModal`): focus doesn't move into them, isn't trapped, and isn't restored on close. `ConfirmModal` has no accessible name and a generic "Confirm" button. Escape listeners stay on `window` while the dialog is closed. | `components.templ:9-67,154-181` |
| A-04 | M | **Toasts** have no `role="status"` or `aria-live`, and errors don't use `role="alert"`. They render in the page flow, not in a live region. | `components.templ:69-90` |
| A-05 | M | **Some checkboxes are unreachable by keyboard.** The notification channel pickers in the monitor form and escalation steps use `class="hidden"` (`display:none`) inputs. Tag toggles, chart range buttons and subscribe-type toggles have no `aria-pressed`. | `monitorform.templ:367-375`, `escalation_policies.templ:123-127`, `monitorform.templ:318-324`, `monitors.templ:615-619`, `statuspage.templ:201-211` |
| A-06 | M | **Unlabelled controls:** all 9 auto-submitting filter selects, the condition-builder inputs, the header editor rows, tag value inputs, and the status-page per-monitor sort and group inputs (which only have `title`). "Delay (minutes)" and "Channels" labels have no `for`. Login, TOTP and the status-page password form have no visible label. | `monitors.templ:253-273`, `monitorform.templ:728-753`, `components.templ:310-323`, `status.templ:173-174`, `escalation_policies.templ:115-120` |
| A-07 | M | **Colour is the only signal in places:** the current on-call person is only a blue badge, and the status dots and daily uptime bars have no text. The bars' tooltip is mouse-only and they have no text alternative; the heatmap does use `<title>`. Every sparkline carries the same `aria-label="Recent status history"`. | `oncall.templ:58-64`, `helpers.go:153-170`, `dashboard.templ:44` |
| A-08 | M | **Auto-refresh replaces DOM under the keyboard user** every 30 s, so focus is lost. There's no way to pause it, and the indicator isn't announced. | `dashboard.templ:129-131`, `monitors.templ:329-336,430-437` |
| A-09 | L | **Times are relative only** ("3h ago") and have no `<time datetime>` or absolute tooltip. The audit log is the exception. | `helpers.go:60-96` and every list |
| A-10 | L | **Small targets:** the 12 px "soft" checkbox, 24 px pager buttons and the 24 px sidebar collapse toggle. Much tabular data is set in 11 px (`text-2xs`). | `monitorform.templ:753`, `layout.templ:197-204,417-427` |
| A-11 | L | **Status-page switches:** clicking the visible text doesn't toggle the switch, because only an `aria-label` is on the input. Table headers lack `scope`, and the incidents action column header is empty. | `status.templ:130-143,262-270`, `incidents.templ:105` |

### 2.5 Responsive behaviour

| ID | Sev | Problem | Where |
|---|---|---|---|
| R-01 | M | **Tables scroll twice on phones.** Below 768 px, `base.css` turns every `main table` into `display:block; white-space:nowrap`, and every table also sits in `overflow-x-auto` with a `min-w-[600–800px]`. That gives nested scroll containers, and row actions end up off-screen. No list has a stacked small-screen layout. | `base.css:93-98`; all list templates |
| R-02 | M | **Form and stat grids don't collapse on small screens:** `grid-cols-2` for the HTTP method/status, auth, OAuth, MQTT and PostgreSQL fields, proxy auth and maintenance start/end; `grid-cols-3` for the SLA stats, monitor SLA and percentiles. Inputs end up about 140 px wide on a 360 px screen. | `monitorform.templ:452,500,509,513,661,665,811`, `proxies.templ:107`, `maintenance.templ:112`, `sla_report.templ:96`, `monitors.templ:572,593` |
| R-03 | M | **Filter strips are hard to scroll.** Up to 20 type pills plus every tag sit in a horizontal scroller whose scrollbar is hidden (`no-scrollbar`), so on desktop without a trackpad there's no visible way to scroll. | `monitors.templ:284-299`, `dashboard.templ:119-128` |
| R-04 | L | **The monitor detail header** can hold 8 buttons (Pause, Clone, Edit, Delete, and Up/Degraded/Down/Message), which wrap unpredictably. `SearchInput` has a fixed `w-56`. The sticky save bar's `-mx-5` depends on `main`'s padding. | `monitors.templ:453-507`, `components.templ:211`, `monitorform.templ:429` |
| R-05 | L | **Content widths are inconsistent:** `main` is `max-w-6xl`, but settings, status pages and the audit panel cap themselves at `max-w-5xl`, and forms use `3xl` or `2xl`. There are 59 arbitrary bracket values in templates (`w-[18px]` ×17, `py-[7px]`, `max-w-[200px]` ×4, …), which the README forbids in spirit. | various |

### 2.6 Dead code and drift

- `setFlash` is a thin wrapper that hides the toast kind (F-01). Two parallel fields feed
  toasts: `LayoutParams.ToastMsg` and `LayoutParams.Error`.
- `DashboardParams.Degraded` and `.Paused` are computed and passed in, but never rendered.
- `SettingsParams.GoVersion` is never used; the template calls `runtime.Version()` itself.
- `MonitorFormParams.FollowRedirects` is set by the handler, but no template reads it.
- There are one-line wrappers that add nothing: `mtlsEnabled()`, `ipFilterFormBase()`, and
  `JSEscapeString` (an alias for the escaper).
- `ParseDNS` ignores its `json.Unmarshal` error. `headersToJSON` iterates a map, so header order
  changes on every edit (`internal/web/monitors.go:108-118`).
- uPlot JS and CSS load on every page, render-blocking in `<head>` together with htmx, although
  only monitor detail uses them (`layout.templ:35,53-54`).
- `web/README.md` rules are broken in practice: hard-coded design values (§2.5 R-05, hex in
  Go/JS), repeated titles (C-04), and styling copied between pages (C-01…C-07).
- The March spec is partly implemented and not marked as such, so it reads as current.

### 2.7 Styling that reads as generic AI output

These patterns are individually harmless, but together they give the UI the stock
"AI-generated SaaS template" look the issue describes.

- **Decorative atmosphere with no information in it.**
  - Two blue radial "glow" gradients are fixed behind every page (`base.css:29-39`).
  - The public page adds an SVG fractal-noise texture and another glow layer
    (`statuspage.templ:89-104`), plus an `animate-ping` halo on the overall status dot.
- **Glassmorphism and heavy shadow everywhere:**
  - `backdrop-blur-md` on the topbar
  - `backdrop-filter: blur(5px)` on every modal backdrop
  - `bg-surface-50/95 backdrop-blur-xl ring-1 ring-white/5` on the palette
  - `shadow-2xl shadow-black/50` on every popover
- **A gradient toggle track with a glow** (`components.css:180-183`), press-scale animation on
  every button (`btn-press` ×69), a pulse dot on down monitors and on online agents, and a
  chevron on every sidebar link that only appears on hover.
- **Default Tailwind status recipes:** `emerald/red/yellow-500/10` fill, `/20` border and `-400`
  text on every pill, banner and toast, plus uppercase, letter-spaced "eyebrow" labels on
  every section, pill and table header. The default tag colour is Tailwind indigo `#6366f1`,
  unrelated to the brand blue.
- **Everything is a 14 px-radius card with a hairline border, and every page opens with a row of
  stat tiles**, whether they help or not ("Visitors 24h" on an uptime dashboard, "Requests 24h"
  duplicated from Logs).
- **Stock copy:** "All systems operating normally" (shown even while monitors are down, because
  it only reflects incidents), "No spam, ever.", "Infrastructure Monitoring", "Quick search…".
  Code comments narrate aesthetics ("A whisper of brand-blue depth…", "Vercel-style ⌘K
  palette", "sophisticated layered neutrals").
- **Brand assets:** the logo is a 570-byte GIF, and the collapsed rail shows `favicon.ico` scaled
  up as the brand mark (`layout.templ:208,211`).
- **Theme default:** the app defaults to `dark` instead of `system`, and the public status page
  inherits the admin's `localStorage` theme rather than the visitor's OS preference.

### 2.8 Adjacent notes (not frontend bugs, but they constrain the rework)

- The CSP allows `'unsafe-inline' 'unsafe-eval'` (`handler.go:112-113`). That is required today
  by the standard Alpine build, inline `<script>` blocks and Go-generated `x-data` strings.
  Moving JS into files (step 6) is what makes the Alpine CSP build and a stricter CSP possible
  later.
- There are no CSRF tokens. Mutations rely on `SameSite=Lax` session cookies plus
  `form-action 'self'`. That is acceptable for POST-only mutations, but it should stay that way
  (no state-changing GETs).
- Proxies, status pages, agents and settings import/export all sit behind `monitors.write`, a
  coarse permission. The UI should reflect whatever the backend decides; it shouldn't invent
  finer checks.

---

## 3. Rework plan

### Principles

- **Keep the stack:** templ + htmx + Alpine + Tailwind standalone, with no Node and no SPA.
  The problems above come from inconsistency and missing structure, not from the choice of
  technology, and the server-rendered model suits an ops tool embedded in one binary.
- **Structure first, restyle last.** Visual changes land once, in shared components, instead
  of in 23 page files.
- **Every step is one or more PRs, and each PR is green on its own.**
  - Checks: `templ generate` (no diff), `go vet ./...`, `go test -race ./...`, and
    `make css` with no diff.
  - Plus the page smoke checklist introduced in step 0.
- **No behaviour change unless the step says so.** Each step lists what users will notice.

Rough sizes: S is under a day, M is 1–3 days, L is more than 3 days.

### Step 0: Safety net (S–M)

No user-visible change.

1. **CI:**
   - Download Tailwind in the `test` job on pull requests too.
   - Run `make css` and `git diff --exit-code web/static/tailwind.css docs/static/docs.css`.
   - Run `templ generate` and `git diff --exit-code` so generated files can't drift.
   - Fix the rebuild trigger to include `web/css/`, `internal/web/views/*.go` and `docs/`
     (F-14).
2. **View smoke tests** (`internal/web/views/render_test.go`):
   - Render every exported page component with fixture params and parse the output with
     `golang.org/x/net/html`, which is already a dependency.
   - Assert: no render error; exactly one `<h1>`; every `input`, `select` and `textarea` that
     isn't `type=hidden` has a `<label for>`, `aria-label` or `aria-labelledby`; every
     `button` and `a` has an accessible name; no `value=` on `type=password`.
   - Start with an allowlist of today's violations. Each later step shrinks it.
3. Add `docs/frontend-smoke.md`: a 10-minute manual checklist covering dark and light themes,
   360 px width, keyboard-only use, and read-only vs write keys.

Done when CI fails on stale CSS or generated files, and the render tests run in `go test`.

### Step 1: Correctness fixes (M, one PR per item)

Users notice fewer surprises. Nothing is restyled.

- **1a. Toast kinds (F-01):** replace `setFlash` with `h.toastOK` and `h.toastErr`. Convert all
  failure paths to errors. Error toasts persist (already implemented). Stop echoing raw
  `err.Error()` to users (`export.go:45`).
- **1b. One-time secrets (F-02):** after `AgentCreate`, render a result page (or a panel on the
  agents list) that shows the token with a copy button and "you won't see this again". Don't
  use a toast or a cookie.
- **1c. Multi-step monitors (F-03, F-04):**
  - Until a real step editor exists, `http_multi` is JSON-only: the form forces advanced mode
    for that type and says so.
  - `parseJSONOrForm` returns an error on invalid JSON, and the handler re-renders the form
    with a field error (HTTP 422) instead of saving `nil`.
  - Add a handler test: editing a multi-step monitor in form mode must not change its
    settings.
- **1d.** Chart double init (F-05): remove `x-init="init()"`.
- **1e.** Monitor list filters (F-06, F-07):
  - Correct the empty-state and "Clear" conditions.
  - Add `views.QueryURL(base, url.Values)` and route all 12 `*Href` builders through it, so
    values are escaped and every active filter is preserved. Unit-test it.
- **1f.** Group detail pagination (F-08).
- **1g.** Permission gating (F-10): add a `can(p, "perm")` helper and use it everywhere there's a
  write control, including the `n` shortcut. Extend the render tests to render a read-only
  fixture and assert that no write actions appear.
- **1h.** Cache busting (F-13): compute a content hash per embedded file at startup
  (`assetVersion` generalised to a map) and add a `views.Asset(path)` helper. Use it in all
  five `<head>`s.
- **1i.** Confirm destructive import (F-17). Use `partial` / `major` wording on the public page
  (F-15). Fix `capitalize` (F-20). Fix the double "Paused" (F-18).

### Step 2: Stop rendering secrets (M)

Users notice that secret fields show "Saved, leave blank to keep" instead of the value.

- Monitor settings (HTTP auth, OAuth2, mTLS key, MQTT, Redis), proxy password, and notification
  channel secrets become write-only.
- Handlers keep the stored value when the field comes back blank, and offer an explicit
  "clear" checkbox where clearing is valid. This is the same pattern as the status page
  password (`status.templ:218-226`).
- The notifications page stops embedding `ToJSON(ch)` with settings. Edit loads a
  server-rendered form (see step 7) or a redacted JSON blob.
- Tokens use `type="password"` with a reveal toggle.
- Tests: the render test asserts that no stored secret string from the fixtures appears in the
  output HTML.

### Step 3: One source of truth for enumerations and nav (S–M)

No visual change, except that the dashboard type filter shows full labels.

- Add `views/catalog.go` with:
  - `MonitorTypes`: value, label, category, target placeholder, `HasStatusCode`,
    `HasPercentiles`, `HasSettingsForm`.
  - `NotificationEvents`, `AuditActions`, `AuditEntities`.
  - `NavItems`: href, key, label, group, icon.
- Sidebar, palette, filters, form `<optgroup>`s, `TypeLabel`, audit filters and
  `sse-refresh.js` all read from the catalog. The SSE event list is passed through a `data-`
  attribute.
- Add a test that fails if a type listed in `validate` has no catalog entry, or has
  `HasSettingsForm` without both a settings template and an assembler. That test would have
  caught F-03.
- Page titles and sidebar labels come from `NavItems` (C-12).

### Step 4: Semantic status tokens (S–M)

Small visual change: paused becomes neutral, and colours become consistent.

- In `tokens.css`, add `--color-ok`, `--color-warn`, `--color-crit`, `--color-info`,
  `--color-neutral` and `--color-maint`, each with a light-theme remap, plus
  `bg-ok/10`-style utilities.
- Add one Go mapping `StatusTone(status) Tone` and one `UptimeTone(pct)` with a single
  threshold set. Delete `StatusColor`, `StatusBg`, `StatusDot`, `UptimeBarFill`, the
  `heatmapColor` hex values and the sparkline hex values.
- Go SVG builders emit `fill="var(--color-ok)"`. `monitor-chart.js` reads the colours from CSS
  variables. Legends use the same tokens (F-16, D-04).
- Remove the runtime class safelist from `tailwind.input.css`, since tones become fixed
  classes.

### Step 5: Component layer (L, split per page group)

Visual change: pages look like one product. Done in about four PRs: shell + lists, detail
pages, forms, and settings/misc.

Add to `components.templ` / `components.css`, each with a render test:

| Component | Replaces |
|---|---|
| `PageHeader(title, back, actions…)` | per-page `<h1>` and back links; the topbar becomes the only `<h1>` (C-04, A-01) |
| `StatCard(label, value, tone, sub)` | ~20 copy-pasted tiles (C-01) |
| `Pill(tone, text)` | `.badge-*` plus `StatusPill`/`SeverityPill` (C-02) |
| `IconButton(kind, label, href \| form)` | `row-action`, the inline copies, `icon-btn` (C-03) |
| `Pager(result, url)` | 8 footers (C-08) |
| `FilterBar` = `FilterPills` + labelled `FilterSelect` with a `<noscript>` submit button | mixed filter widgets (C-09, A-06) |
| `EmptyState(kind, message, action)` with an icon per kind | two components, one folder icon, ad-hoc blocks (C-07) |
| `Field(label, hint, error){input}`, `SecretField`, `CheckboxField`, `Toggle` | label/hint drift, unlabelled toggles (C-10, A-11) |
| `DataTable` shell with `scope`d headers and an empty-state slot | per-page table markup |
| `FormPage(title, action, submitLabel, cancelHref){…}` | three form layouts (C-05) |

Delete the superseded helpers, the arbitrary bracket values and the inline
`focus-visible:ring-*` overrides as each page migrates. Shrink the step 0 allowlist.

### Step 6: Move Alpine code out of Go strings (M)

No visual change.

- Put the code in `web/static/js/app.js`, loaded with `defer` once and registered through
  `Alpine.data(...)`:
  - `shell`, `cmdPalette`, `confirmDialog`, `toastStack`
  - a single `crudDialog` factory (open, edit, formAction), which replaces the five CRUD
    objects (D-05)
  - `monitorForm`, `conditionBuilder`, `tagPicker`, `escalationSteps`, `monitorChart`
- Server data reaches it only through `data-*` attributes or
  `<script type="application/json" id=…>` blocks. Remove the JS emitted from Go strings,
  `JSEscapeString`, and hand-built `x-data`.
- Load uPlot only on monitor detail. Defer htmx. Dedupe the theme bootstrap into one templ
  component used by all heads (D-06).
- This unblocks a later switch to the Alpine CSP build and dropping `'unsafe-eval'` (§2.8).

### Step 7: Consistent create/edit flows (M–L)

Behaviour change: pages you can bookmark, and edit where it was missing.

- **Rule:** single-field entities (tag, group) use a modal. Everything else uses a `FormPage`
  route (`/x/new`, `/x/{id}/edit`): notification channels, escalation policies, maintenance,
  on-call, agents.
- Add the missing edit flows: maintenance (the route already exists), on-call (a new web route
  backed by the existing store update), and agent rename (F-09).
- Build a `MonitorPicker` component (search plus checkbox list, as on the status page form) to
  replace raw IDs in maintenance.
- **Validation:** invalid input re-renders the form with HTTP 422, keeps the values, shows
  errors per field, and shows a summary `role="alert"`. Redirect-with-toast is only for
  success. Extract `loadMonitorFormDeps` (D-07).
- **Submit buttons** get a pending state (disabled, "Saving…"), done once in `FormPage`.

### Step 8: Shell and navigation (M)

Behaviour change: faster in-app navigation and an honest palette.

- **Boost everything:** move `hx-boost` to `<body>`, exclude downloads and external links,
  and keep a single `afterSettle` sync. Pagers, filters, palette navigation and redirects after
  forms then all use partial swaps.
- **Palette:** either rename it to "Go to…" (cheap), or add `GET /search?q=` returning an HTML
  fragment of matching monitors, incidents and status pages (F-19). The recommendation is to
  do the cheap option in this step and the search endpoint later.
- **Drawer:**
  - `inert` while closed
  - focus moves to the first link on open
  - Escape closes it
  - focus returns to the toggle
  - add a skip link (A-01, A-02)
- **Live refresh:**
  - pause polling while focus or a dirty input is inside the region
  - use `hx-preserve` or move input controls out of refreshed regions (F-11, A-08)
  - reconnect SSE with backoff instead of closing it forever
  - announce refresh failures politely

### Step 9: Accessibility pass (M)

Behaviour change for assistive technology and keyboard users only.

- **Dialogs:** initial focus, focus trap and focus return in `confirmDialog`, `crudDialog` and
  the palette; `aria-labelledby` on the confirm dialog; action-specific button text ("Delete
  monitor") (A-03). The trap is hand-rolled, about 30 lines, to avoid a new vendored plugin.
- **Toasts:** a fixed live region (`role="status"`, plus `role="alert"` for errors) (A-04).
- **Checkboxes and toggles:** replace `class="hidden"` checkboxes with `sr-only peer` inputs and
  styled labels; add `aria-pressed` to toggles (A-05).
- **Labels and text alternatives:** label every control (A-06). Give uptime bars, the heatmap
  and sparklines text equivalents (a visually hidden summary plus per-day `<title>`s). Mark the
  current on-call person in text (A-07).
- **Times and targets:** a `Time(t)` component that renders
  `<time datetime title="absolute">relative</time>` (A-09), and minimum 32 px targets (A-10).
- **Tests:** by this step the render-test allowlist should be empty.

### Step 10: Responsive pass (M)

Visual change on small screens.

- Remove the global `main table{display:block}` override. `DataTable` gets a stacked-row layout
  below `sm` for primary lists (monitors, incidents, notifications history); secondary tables
  keep a single horizontal scroller (R-01).
- All form grids become `grid-cols-1 sm:grid-cols-2`, and stat grids collapse to one or two
  columns (R-02).
- Replace the type pill strip with a labelled Type select (tags stay as pills, wrapping instead
  of scrolling) (R-03).
- On the monitor detail header, keep Edit as the primary action and move the rest into an
  overflow menu below `md`. Manual-status controls go in their own card (R-04).
- Set one content width rule: lists use the full `max-w-6xl`, forms use `max-w-3xl` (R-05).

### Step 11: Visual identity (M–L)

Visual change. This is the "quality bar" step, done last so it lands once in the shared
components.

**Remove:**

- body radial glows, the public noise and glow layers, and backdrop blurs (except a plain
  translucent modal scrim)
- the gradient and glow switch, `btn-press`, the hover chevrons on nav links, and `animate-ping`
- most uppercase eyebrows, which become sentence-case section titles

**Denser, calmer surfaces:**

- fewer bordered cards
- lists as the primary unit
- tabular numbers
- a 12–13 px base for data and 14 px for body text
- status shown by a leading tone mark plus a text label, not a filled pill everywhere

**Dashboard:**

- lead with the problem: down and degraded monitors first, then open incidents, then the rest
- one summary line ("42 monitors · 1 down · 2 degraded") instead of six tiles
- drop Requests and Visitors (they stay on Logs)

**Brand and copy:**

- an SVG logo and mark, replacing `logo.gif` and the favicon in the rail
- default tag colour from the brand palette
- specific empty states and error messages, removing stock phrases (§2.7)
- rewrite comments that narrate aesthetics

**Theme and docs:**

- default theme `system`
- update `web/README.md` to describe the components and rules as they actually are

### Step 12: Public status page (M)

Visual change on the public page.

- Separate CSS entry (`web/public.input.css`) scanning only `statuspage.templ`, for a much
  smaller download. Cache-bust it with the step 1h helper.
- Public wording: Operational, Degraded, Partial outage, Major outage, Maintenance. No raw
  `up`/`down`/`paused` (F-15).
- Accessible uptime history: a focusable summary per monitor ("99.95 % over 90 days, 2 days
  with downtime"), per-day `<title>`, and a keyboard-reachable tooltip.
- Absolute incident dates. Theme follows the visitor's OS preference and ignores the admin's
  `localStorage`.
- Subscribe form: labelled inputs, `required` on the active field, and `aria-pressed` on the
  toggle.

### Step 13: Cleanup and documentation (S)

No user-visible change.

- Delete the dead fields and helpers in §2.6.
- Generate the docs token block from `web/css/tokens.css`, or `@import` a shared tokens file
  that has both the `data-theme` and `.dark` selectors (D-08).
- Mark `docs/superpowers/specs/2026-03-14-frontend-overhaul-design.md` as superseded by this
  document.
- Final `web/README.md` pass.

### Dependency order

```
0 ──► 1 ──► 2
      │
      └──► 3 ──► 4 ──► 5 ──► 6 ──► 7 ──► 8 ──► 9 ──► 10 ──► 11 ──► 12 ──► 13
```

- Step 1 items are independent of each other.
- Steps 2 and 3 can run in parallel after step 1.
- Steps 9 and 10 can run in parallel after step 8.
- Step 12 only needs steps 1, 4 and 5, so it can be pulled earlier if the public page is the
  priority.

### Decisions for the maintainer

1. **Boost the whole app (step 8)** or drop `hx-boost` entirely. The recommendation is to boost
   the whole app: it's already half-adopted and gives partial navigation for free.
2. **Palette:** keep it as "go to page", or add the server-side `/search` fragment. The
   recommendation is to rename first and add search later.
3. **Default theme:** `system` (recommended) or keep `dark`.
4. **CRUD rule in step 7:** is the modal/page split above acceptable, or should everything be a
   page?

---

## 4. Progress on this branch

After the audit, this branch implemented a first large slice of the plan. The target look is
flat and dense:

- Inter on a near-black neutral ramp (`#101010` canvas, `#181818` cards, `#242424`/`#323232`
  borders) with 1px borders, 4px radii and 32px controls
- bold page headings with a muted subtitle
- one accent: yellow for active and focus states in dark, violet for the filled primary button
  and in light
- colour otherwise reserved for status

`web/README.md` describes the design system as built.

### Done

| Plan step | What landed |
|---|---|
| 0 | New `generated` CI job: fails when `*_templ.go`, `tailwind.css` or `docs.css` are stale. Rebuild trigger now covers `web/css/`, view Go files and docs (F-14). `make css` also builds the docs CSS. `internal/web/views/render_test.go` renders all 27 pages (write and read-only keys) and asserts: one `<h1>`, labelled controls, named buttons and links, no password values, no write actions for read-only keys. |
| 1 | Fixed: F-01, F-02, F-04, F-05, F-06, F-07, F-08, F-10, F-11, F-13, F-15, F-16, F-17, F-18, F-19 (renamed to "Go to…"), F-20. See the list below the table. |
| 2 | F-12. Stored secrets are no longer rendered: monitor auth, OAuth and mTLS secrets, MQTT and Redis passwords, proxy password, and notification channel tokens and webhook URLs. Fields say "Saved. Leave blank to keep it." and handlers merge blanks with the stored value (`views/secrets.go`, tested). The JSON editors show redacted settings. |
| 3 (partial) | Status wording and status/uptime colours come from `StatusTone`, `StatusLabel`, `UptimeTone` and `slaState`. Filter and pager URLs are built with `url.Values`. Audit filters cover every action and entity actually written. |
| 4 | Semantic tone tokens (`ok`, `warn`, `major`, `crit`, `info`) with light remaps. No palette classes or hex colours remain in templates, Go helpers or chart JS. Paused is neutral. Legend and SVG colours match. |
| 5 | `PageHeader`, `StatCard`, `Pager`, `RelTime`, `ToolbarNewButtonClick` and a dot-and-label `StatusPill`. One badge system, one row-action style. Every page was rewritten onto them, which removes the duplicate `<h1>`s and the four back-link styles. |
| 8 (partial) | Skip link. The mobile drawer is `inert` while closed and closes with Escape. Desktop topbar removed (the sidebar carries the brand). All static assets are content-hashed. htmx and uPlot are deferred. |
| 9 (partial) | Dialogs: initial focus (FormModal) and focus return (ConfirmModal), `alertdialog` with an accessible name. Toasts are a fixed live region (`role=status`, or `role=alert` for errors). Keyboard-reachable channel checkboxes. `aria-pressed` on toggles. Labels on every filter and condition-builder control. `<time>` with absolute UTC on hover. Text summary for the public uptime bars. On-call "on call now" is shown in text. |
| 10 (partial) | Removed the global `main table {display:block}` hack. Monitor and incident tables drop secondary columns on small screens. Forms and stat grids collapse to one column. The 19 type pills are now a Type select. Stat values shrink on phones. |
| 11 | Removed the body glows, public noise and glow layers, glass blur, gradient switch, press-scale animation, nav chevrons, uppercase eyebrows and the indigo tag default. The dashboard leads with problems (sorted by status) and a one-line summary; the request and visitor tiles are gone. Wordmark instead of the GIF logo. Rewritten copy. |
| 12 (partial) | Public status page: visitor wording (Operational, Degraded, Outage, Partial/Major outage), follows the OS theme, absolute incident times, labelled subscribe form with `aria-pressed`, cache-busted CSS. |

Correctness fixes from step 1:

- **Toasts (F-01):** failures now show as persistent error toasts, and raw vacuum errors are no
  longer echoed.
- **Agent token (F-02):** shown once in a panel with a copy button.
- **Invalid JSON (F-04):** rejected with an error, and the typed text is kept.
- **Multi-step monitors (F-03):** JSON-only in the form, and a form-mode save never drops stored
  steps.
- **Refresh and reliability (F-05, F-11, F-13):** the chart initialises once, the manual-status
  input survives auto-refresh, and all static assets are cache-busted.
- **Lists (F-06, F-07, F-08):** correct empty states, filters are preserved and escaped, and the
  group detail page paginates.
- **Permissions (F-10):** write controls are hidden from read-only keys.
- **Public page and SLA (F-15, F-16):** partial vs major outage wording, and one SLA and uptime
  threshold set.
- **Other:** import-replace asks for confirmation (F-17), no double "Paused" (F-18), the palette
  reads "Go to…" (F-19), and `capitalize` is rune-safe (F-20).

### Still open

- **F-09:** there are no edit flows for maintenance windows, on-call rotations or agents, and no
  monitor picker for maintenance (step 7).
- **Step 6:** Alpine state is still assembled in Go strings for the tag, group, escalation and
  monitor-form dialogs.
- **Step 7:** field-level validation errors (HTTP 422 with per-field messages) and pending-state
  submit buttons.
- **Step 8:** app-wide `hx-boost`, pausing polling while the user is typing, SSE reconnect with
  backoff, and a server-side search for the palette.
- **Step 9:** focus trapping inside dialogs and the mobile drawer.
- **Step 12:** a separate, smaller CSS bundle for the public page.
- **Step 13:** generating the docs theme from `web/css/tokens.css` (D-08).
