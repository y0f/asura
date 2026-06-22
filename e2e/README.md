# End-to-end specs

Browser-level regression specs. These are **developer-only** tools: they are not
part of the Go build, the binary, or CI, and the runtime stays dependency-free.
They require [Playwright](https://playwright.dev/) and a running Asura instance.

## flash.spec.mjs

Guards against cold-load flash/reflow in the app shell. Alpine.js loads deferred,
so the page paints before it initialises; this spec delays the Alpine bundle,
measures the sidebar geometry on the first painted frame (before `window.Alpine`
exists) and again once Alpine settles, and fails if they differ — for both the
expanded and collapsed nav states.

Run it against a local instance:

```sh
# 1. start an instance (any API key works; cookie_secure can be false for http)
./asura -config config.yaml

# 2. point the spec at it and run with Playwright available on NODE_PATH
ASURA_BASE=http://127.0.0.1:8099 ASURA_KEY=ak_your_key node e2e/flash.spec.mjs
```

Exits non-zero on any geometry mismatch. Tune the Alpine delay with
`ALPINE_DELAY_MS` (default 700).
