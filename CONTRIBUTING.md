# Contributing to Asura

Start with a focused change, test its behavior, and report the checks actually run. This workflow adapts the ownership and repeatable checks used in [Gophling](https://github.com/y0f/gophling/blob/main/.github/CONTRIBUTING.md).

## Local setup

Use Go 1.27.4 (the minimum supported patch is 1.26.11) and the [Tailwind 4.2.1 standalone CLI](https://github.com/tailwindlabs/tailwindcss/releases/tag/v4.2.1) for your operating system. Save the CLI as `tailwindcss` (`tailwindcss.exe` on Windows) in the repository root; on Unix, run `chmod +x tailwindcss`. No Node installation is needed. The build commands run the exact templ version recorded in `go.mod`.

Run `go run ./cmd/asura --setup`, save the generated API Key, and create the minimal local `config.yaml` from [getting started](docs/getting-started.md#build-and-run-locally) using its Hash. The configuration file is gitignored. Disable secure cookies only for local HTTP development.

## Development workflow

The development watcher regenerates assets, rebuilds and restarts the server after edits:

**Linux / macOS / Git Bash:**
```bash
make dev
```

**Windows (PowerShell):**
```powershell
.\dev.ps1
```

This watches application sources, stylesheets and embedded assets. It finishes template and CSS generation before building the server, so CSS-only changes appear after the automatic restart. A failed generation or build leaves the previous server running.

Open http://localhost:8090 and log in with the API key from `--setup`.

### Manual workflow

Build generated assets before embedding them in the binary, then restart after edits:

```bash
make build
./asura -config config.yaml
```

## Check the change

While editing, run the package tests that cover the behavior you changed. For example:

```bash
go test ./internal/assertion
go test ./internal/storage -run TestMonitorClone
go test ./internal/web/views
```

Run the source check before handing off a change:

```bash
make check
```

This runs `go vet` and the Go tests, and builds both commands using committed assets. It needs no running database service, Docker or Ruby. Tests use temporary SQLite databases and some open loopback listeners. On Windows without Make, run the commands directly:

```powershell
go vet ./...
go test ./...
go build ./cmd/asura ./cmd/asura-agent
```

Before merging, run the full race suite. Run the additional fixtures when their scripts or build contracts change:

```bash
make test                         # go test -race -count=1 ./... after generation
bash scripts/test-install.sh
bash scripts/test-dev.sh
bash scripts/test-release-tag.sh
bash scripts/test-agent-deps.sh
```

Race detection requires CGO and a supported C compiler; CI runs it on Linux. If your platform lacks that prerequisite, report the race suite as unrun and use the CI result. Do not remove race checks or extend timeouts to conceal blocked I/O or missing cleanup. Keep a regression for a reproduced bug and test meaningful failure paths, including denied access and transaction rollback.

For UI changes, inspect populated and empty/error states in light and dark themes, desktop and narrow mobile layouts. Exercise keyboard focus, dialogs, form errors and live updates with representative permissions. Screenshots should show the implemented interface with synthetic data. Record the browser and checks performed; a unit test does not establish visual quality or accessibility conformance.

Changes to SQL or migrations need real storage tests: fresh creation, supported upgrades, rollback on failure, and the affected queries or concurrent transitions. Use isolated fixtures, never an existing development or production database. Include configuration, agent and API compatibility tests when their contracts change.

For a resource-limited machine, set `GOMAXPROCS=1`, use `go test -p 1`, and run heavy suites serially. Give Docker fixtures explicit CPU and memory caps, unique names and temporary data; stop them when finished. Do not delete a shared volume to fix a test.

## Optional PostgreSQL backend

`make build-postgres` regenerates the interface and builds `asura-postgres` with `-tags postgres`. Standard release and Docker builds remain SQLite-only. The agent must not link either database driver, even when the PostgreSQL tag is present.

For changes to shared SQL or the optional backend, use a disposable PostgreSQL 18.6 test server. Set `ASURA_TEST_POSTGRES_DSN` to its administrative `postgres` database URL: the tests create isolated databases and a temporary role, then remove them. Never use a development or production database service as this fixture. The application itself is tested with a database-owning role without superuser, database-creation or role-creation privileges.

```bash
: "${ASURA_TEST_POSTGRES_DSN:?Set the disposable PostgreSQL fixture URL}"
go vet -tags postgres ./...
go test -tags postgres -race -count=1 -timeout=5m ./internal/storage -run '^TestPostgres'
bash scripts/test-postgres-deps.sh
```

CI runs these contracts against both supported Go patches. They reuse storage behavior tests and cover schema parity, current scopes, failed-write rollback, read snapshots, ownership loss, startup cleanup and driver-error privacy. Run the ordinary SQLite suite too when shared queries change. Keep PostgreSQL DDL explicit; coordinate its baseline and future migrations with the SQLite schema version rather than translating SQL strings.

## Generated assets and releases

Before committing generated application output, run `make generate css`. Run it again and verify `git diff` stays unchanged. CI verifies generation without writing back to the branch, and gates release binaries and container publication on tests, documentation checks, formatting, static checks and clean generated output.

Documentation uses Jekyll, kramdown and plain CSS. Follow [docs/README.md](docs/README.md) for the locked Ruby setup, preview and `make docs-check`. Edit Markdown, layouts, navigation and styles under `docs`; preserve permalinks and heading IDs. The checker validates links, anchors, titles, descriptions, canonical URLs, the sitemap and the public file catalog. CI and Pages use the same locked build and checker; local builds do not publish anything.

`make release` verifies the reviewed dependency notices, builds the server and agent for Linux amd64 and arm64, and copies `LICENSE` and `THIRD_PARTY_NOTICES.txt` beside the four binaries. `dist/checksums.txt` covers all six files. It uses the committed generated assets, so run `make generate css` before preparing a local release. CI retains a coverage profile for each Go version for 14 days.

Dependency or toolchain changes require reviewing the applicable upstream license texts and generated-source notices in [licenses/README.md](licenses/README.md). Run `make notices` to assemble the combined notice and `make notices-check` to verify it against the audited runtime module graph. These files accompany release binaries; Docker images place them in `/usr/share/licenses/asura/`.

CI scans source on both supported Go patches and all four release binaries with pinned govulncheck. Release tags publish only after reusable CI passes. Publication generates GitHub build attestations for the release files and image digest; container builds also request BuildKit provenance. Action revisions, Go patches, base-image digests and the Tailwind checksum are pinned, but hosted runners and package repositories can change: these controls do not establish reproducible builds or a SLSA level. Local `make release` neither publishes nor signs artifacts.

Release tags must be valid `v`-prefixed semantic versions matching the Go module's major version, such as `v2.0.0` or `v2.0.0-rc.1` for `github.com/y0f/asura/v2`. Prereleases are marked as such on GitHub and publish only their full container version tag; stable releases also update `major.minor` and `latest`. Build metadata is retained in image tags with `+` replaced by `_`, and container tags must fit the 128-character limit. Missing listed release artifacts fail publication.

## Ownership and parallel work

Use separate branches/worktrees with a common base, owned paths, dependencies and focused checks. Coordinate shared types, storage contracts, migration versions, routes and generators through one integration owner. Hand off exact outcomes and unresolved issues. Integrate dependencies in order, regenerate on the combined tree, and resolve generated conflicts from their sources. Never renumber an applied migration. Keep private configuration and test data separate; Go caches can be shared.

## Pull requests and review

Keep each pull request centered on a concrete problem. Explain the resulting behavior, compatibility or migration effects, and commands actually run with their outcomes. Include failed or skipped checks and why they failed or could not run. Suggested commands are not test evidence. Review the complete diff for secrets, accidental generated drift and unrelated changes before submitting it.

Use concise commit subjects, preferably `fix(area): ...`, `feat(area): ...` or `docs: ...`, describing the change. Keep the human contributor as the author. Follow the [PR template](.github/PULL_REQUEST_TEMPLATE.md), including its required assistance declaration.

### AI assistance

Every pull request answers **yes or no** to AI assistance. If yes, identify the tools and the parts they helped with, including design, code, tests, documentation or review; include model names when known. Update that account if the scope changes during review. Deterministic generators, formatters and linters do not count as AI assistance.

The contributor is responsible for understanding and reviewing the whole change, verifying its behavior and security assumptions, and checking the source and license of incorporated code. Automated review provides feedback, not a certification. Do not include private prompts, transcripts or credentials in the declaration. Do not add AI tools or models as commit authors or `Co-authored-by` trailers; assistance belongs in the PR description.

## Code conventions

- Use `gofmt` and `go vet`; return errors with enough context to diagnose failures. Never replace a failed read with a successful empty result unless that behavior is deliberate and documented.
- Keep dependencies and runtime work small. Explain additions; measure changes before claiming lower memory use, faster responses or better scale.
- Enforce permissions in request and persistence paths, including nested reads, counts, bulk operations and current ownership. Hidden controls alone are not access control.
- Keep stored credentials out of ordinary responses, logs and examples. Preserve omitted credentials on edits and test explicit replacement or clearing.
- Keep checker and agent dependencies independent of server storage; preserve `scripts/test-agent-deps.sh`.

## Issues

For bugs, include the Asura and Go versions, operating system, reproduction steps, expected and actual behavior, and sanitized logs. Discuss substantial features or compatibility changes in an issue before opening a large pull request. Existing `good first issue` and `help wanted` labels are useful starting points.

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
