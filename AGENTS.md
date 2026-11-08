# Repository guide

Asura is a Go monitoring server with an embedded templ/HTMX/Alpine interface and
a separate probe agent. SQLite is the default storage backend.

## Commands

```sh
make check                 # source tests, vet, server/agent build
make generate css          # pinned templ and standalone Tailwind
make docs-check            # Jekyll build, links, anchors, metadata; requires Ruby
make test                  # full race suite; requires a supported C compiler
```

Use focused package tests while editing. The checks use temporary databases;
some tests open loopback listeners. See [CONTRIBUTING.md](CONTRIBUTING.md) for
Windows commands, prerequisites, UI verification and release checks. Report
the actual commands and outcomes, including failures or checks that could not run.

## Boundaries

- Edit `.templ` and `web/css` sources, then regenerate. Do not hand-edit
  `*_templ.go` or compiled CSS. Documentation sources are Markdown, Jekyll layouts
  and plain CSS under `docs`; build output belongs in `.build/docs`, not Git.
- Keep pure monitor types in `internal/model`. The probe agent and checkers must
  not import server storage or link either database driver.
- Enforce each required action against the current object or destination scope.
  Filter before pagination/counts; check authority and related writes within the
  same transaction. Navigation visibility is not authorization.
- Preserve secrets on omitted edits, redact ordinary reads, and keep credentials
  out of logs, screenshots, examples and error responses.
- Serialize migration numbering, shared storage contracts, routes and final asset
  generation through one integration owner. Never renumber an applied migration.
- Parallel workers use separate worktrees and explicit owned paths. Share the
  dependency commit, test evidence and unresolved issues before integration.
- Test changed behavior and meaningful failure paths. Keep the runtime small;
  justify dependencies and measure performance claims on comparable builds.
- Follow the contribution policy for the required yes/no PR assistance disclosure.
  Do not add AI tools or models as commit authors or coauthors.
