# Asura frontend

Server-rendered [templ](https://templ.guide) + [HTMX](https://htmx.org) + [Alpine.js](https://alpinejs.dev),
styled with a self-hosted [Tailwind CSS v4](https://tailwindcss.com) standalone CLI. No Node, no npm.

## Layout

```
web/
  tailwind.input.css      # entry: @import tailwindcss + fonts + @source + design-system layers
  css/
    tokens.css            # colours, status tones, type scale, radii (dark default, light remap)
    base.css              # element base styles: body, focus ring, scrollbars, tables, motion
    components.css        # component utilities (card, panel, form-*, btn-*, badge, switch, filter-tab …)
  static/
    tailwind.css          # BUILT output (committed; CI fails if it is stale)
    fonts/                # Inter + JetBrains Mono (woff2, self-hosted)
    *.js                  # htmx, alpine, uplot, small page scripts
  embed.go                # //go:embed static/* - assets are baked into the binary

internal/web/views/       # templ templates
  layout.templ            # app shell: sidebar, mobile top bar, command palette
  components.templ        # PageHeader, StatCard, StatusPill, Pager, RelTime, dialogs, toasts, buttons
  helpers.go              # status tones and labels, formatting, SVG sparkline/heatmap/uptime bars
  secrets.go              # which settings keys are secrets; redaction and merge-on-save
  assets.go               # content-hashed static URLs (cache busting)
  render_test.go          # renders every page and checks structure and accessibility invariants
  *.templ                 # one file per page
  statuspage.templ        # the public status page (its own <html>)
```

## Design system

The look is flat and dense: a near-black neutral ramp, a blue accent, 1px borders, 4px radii,
32px controls, bold page headings, and colour otherwise reserved for status.

**Theme.** `data-theme` on `<html>` (`dark` default, `light` remap) is resolved before first paint
by `themeScript`. Every neutral utility (`bg-surface`, `text-muted`, `border-line`, …) resolves to a
CSS variable, so both themes share one set of classes. The public status page follows the
visitor's OS preference.

**Colour.**
- Neutrals: `surface` (canvas) → `surface-50` (cards, inputs, sidebar) → `surface-100` (dialogs) →
  `surface-200` (hover, selected) → `surface-300` (strong controls); `line` / `line-light` borders;
  `white` / `muted-light` / `muted` text.
- Accent: blue is the primary colour. `brand` marks active navigation, focus rings, links and
  selection; `brand-button` is the single filled primary button colour.
- Status tones: `ok`, `warn`, `major`, `crit`, `info`. Use them through `StatusTone`,
  `UptimeTone`, `StatusPill` and the `text-*` / `bg-*` utilities. Never use raw Tailwind palette
  colours (`emerald-400`, `red-500`, …) or hex values in templates, Go or JS.

**Type scale.** One source of truth in `tokens.css`. Use the named steps, never `text-[Npx]`:

| token | px | use |
|------|----|-----|
| `text-2xs` | 11 | kbd, dense meta |
| `text-xs` | 12 | captions, hints, badges |
| `text-sm` | 14 | body, table cells, controls |
| `text-md` | 16 | card and section titles |
| `text-lg` | 18 | form section headings |
| `text-xl` | 20 | dashboard section headings |
| `text-2xl` | 24 | stat values |
| `text-3xl` | 30 | page heading (`PageHeader`) |

**Components.** Reach for these before writing markup:
- Page structure: `PageHeader` (the page's single `<h1>`, back link, subtitle, actions) and
  `StatCard`.
- Status and time: `StatusPill` (dot + label), `SeverityPill`, `Pager`, `RelTime` (relative time
  with the absolute UTC time on hover).
- Actions and empty states: `ToolbarNewButton` / `ToolbarNewButtonClick`, `DeleteButton`,
  `EmptyState` / `EmptyStateModal`.
- Dialogs and messages: `FormModal`, `ConfirmModal` (via `$dispatch('confirm', …)`), `Toast`.
- Utility classes: `card` / `panel` / `card-pad`, `form-label` / `form-input` / `form-select` /
  `form-checkbox` / `form-hint`, `btn-primary` / `btn-secondary` / `btn-danger` (+ `btn-sm`),
  `row-action`, `icon-btn`, `badge` (+ `badge-*`), `dot`, `switch`, `filter-tab`, `th`.

## Build

```bash
make css      # build + minify web/static/tailwind.css and docs/static/docs.css
make watch    # rebuild on save during development
make generate # templ generate (regenerate *_templ.go after editing *.templ)
```

CI runs both and fails the build if the committed output differs.

## House rules

- Each page renders exactly one `<h1>`, through `PageHeader`.
- Status wording and colour come from the helpers (`StatusTone`, `StatusLabel`, `UptimeTone`), so
  every page agrees on what "degraded" or "at risk" means.
- Hide write actions from read-only keys with `p.Can("perm")`.
- Never render a stored secret into HTML. Add new secret settings keys to `secrets.go`.
- Build query strings with `net/url` (`url.Values`), never by concatenation.
- Every control needs a label (`<label for>`, a wrapping label, or `aria-label`); icon-only buttons
  need `aria-label`. `render_test.go` enforces this.
- Reference static files with `Asset(basePath, name)` so they are cache-busted.
- Run `make generate && make css` after touching templates or CSS.
