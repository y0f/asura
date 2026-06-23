# Asura frontend

Server-rendered [templ](https://templ.guide) + [HTMX](https://htmx.org) + [Alpine.js](https://alpinejs.dev),
styled with a self-hosted [Tailwind CSS v4](https://tailwindcss.com) standalone CLI. No Node, no npm.

## Layout

```
web/
  tailwind.input.css      # entry: @import tailwindcss + fonts + @source + design-system layers
  css/
    tokens.css            # design tokens: color ramp, brand, type scale, radii (dark = default, light remap)
    base.css              # element base styles: body, focus rings, scrollbars, tables, motion, theme plumbing
    components.css        # reusable component utilities/classes (buttons, inputs, cards, badges, switch …)
  static/
    tailwind.css          # BUILT output (committed; CI rebuilds on push to main)
    fonts/                # Inter + JetBrains Mono (woff2, self-hosted)
    *.js                  # htmx, alpine, uplot, small page scripts
  embed.go                # //go:embed static/* — assets are baked into the binary

internal/web/views/       # templ templates
  layout.templ            # app shell: sidebar, top bar, command palette (the only full <html> for the app)
  components.templ        # shared components: Toast, FormModal, FormCard, StatusPill, EmptyState, buttons …
  helpers.go              # presentational Go helpers (status colours, formatting, SVG sparkline/heatmap)
  *.templ                 # one file per page (dashboard, monitors, monitorform, incidents, settings, …)
  statuspage.templ        # the public, embeddable status page (its own <html>)
```

Separation of concerns: **tokens** (what the theme is) → **base** (how raw elements look) →
**components** (reusable UI). Templates compose components and never hard-code design values.

## Design system

**Theme** is driven by `data-theme` on `<html>` (`dark` default, `light` remap), resolved before first
paint by an inline head script so there is no flash. Every neutral utility (`bg-surface`, `text-muted`,
`border-line`, …) resolves to a CSS variable, so both themes share one set of classes.

**Color** — blue is the primary (`brand`). Neutrals are a 5-step elevation ramp
(`surface` canvas → `surface-50/100` cards → `surface-200/300` controls) plus `line`/`line-light`
borders and `white`/`muted-light`/`muted` text. Status colors stay semantic (emerald/red/yellow/blue).

**Type scale** — one source of truth in `tokens.css`. Use the named steps, **never** `text-[Npx]`:

| token | px | use |
|------|----|-----|
| `text-2xs` | 11 | micro labels, kbd, meta |
| `text-xs` | 12 | captions, table cells |
| `text-sm` | 13 | body / secondary |
| `text-base` | 14 | primary body, inputs |
| `text-md` | 15 | card titles, emphasis |
| `text-lg` | 18 | section headings |
| `text-xl` | 22 | page headings |
| `text-2xl` | 28 | large stats |
| `text-3xl` | 34 | hero metrics |

**Component utilities** (in `components.css`) — reach for these instead of ad-hoc classes:
`card` / `panel` / `card-pad` (surfaces), `eyebrow` (section labels), `form-label` / `form-input` /
`form-select` / `form-checkbox` / `form-hint` (forms), `btn-primary` / `btn-secondary` / `btn-danger` /
`btn-success` / `btn-warning` / `btn-sm` / `btn-press` (buttons), `icon-btn` / `row-action` (icon actions),
`badge` + `badge-*` (pills), `switch`, `filter-tab` + `filter-active`/`filter-inactive`, `th`, `stat-label`.

## Build

```bash
make css      # one-shot: build + minify web/static/tailwind.css
make watch    # rebuild on save during development
make generate # templ generate (regenerate *_templ.go after editing *.templ)
```

Raw CLI (what the targets run):

```bash
./tailwindcss -i web/tailwind.input.css -o web/static/tailwind.css --minify
templ generate
```

`web/static/tailwind.css` is committed and embedded; CI rebuilds it on push to `main`. The docs site has
its own mirror of this theme in `docs/tailwind.input.css` → `docs/static/docs.css`.

## House rules

- Never hard-code a font size — use a `text-*` scale token. Same for the component utilities above.
- New reusable UI → add a component in `components.templ` (markup) and/or `components.css` (style); don't
  copy-paste styling between pages.
- The persistent top bar already shows the page title — pages don't repeat it as an in-page heading.
- Keep the a11y floor: visible focus ring (provided by `base.css`), `aria-label` on icon-only controls,
  `alt` on images, reduced-motion respected.
- Run `make css` after touching any template or CSS so the committed output stays in sync.
