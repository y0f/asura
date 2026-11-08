# Contributing to Asura

Start with a focused change, test its behavior, and include the evidence needed to review it. The workflow below adapts the clear ownership and repeatable checks used in [Gophling](https://github.com/y0f/gophling/blob/main/.github/CONTRIBUTING.md) to Asura's server, storage and embedded interface.

## Local setup

Use Go 1.27.3 (the minimum supported patch is 1.26.10) and the [Tailwind 4.2.1 standalone CLI](https://github.com/tailwindlabs/tailwindcss/releases/tag/v4.2.1) for your operating system. Save the CLI as `tailwindcss` (`tailwindcss.exe` on Windows) in the repository root; on Unix, run `chmod +x tailwindcss`. No Node installation is needed. The build commands run the exact templ version recorded in `go.mod`.

```bash
go run ./cmd/asura --setup
```

Save the generated API Key, then create `config.yaml` using its Hash value:

```yaml
server:
  listen: "127.0.0.1:8090"
database:
  path: "asura.db"
auth:
  api_keys:
    - name: admin
      hash: "PASTE_HASH_FROM_SETUP"
      super_admin: true
  session:
    cookie_secure: false
```

`config.yaml` is gitignored - it won't be committed.

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

### Manual workflow (if you prefer)

Build generated assets before embedding them in the binary, then restart after edits:

```bash
make generate css
go build -o asura ./cmd/asura && ./asura -config config.yaml
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

Before merging, run the full race suite and the checks relevant to scripts or release tooling:

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

## Generated assets and releases

Before committing generated application output, run `make generate css`. Run it again and verify `git diff` stays unchanged. CI verifies generation without writing back to the branch, and gates release binaries and container publication on tests, documentation checks, formatting, static checks and clean generated output.

The public documentation uses the same Jekyll, kramdown and plain CSS approach as Gophling. Edit the Markdown pages, `_layouts/default.html`, `_data/navigation.yml` and `assets/site.css` under `docs`. Preserve existing page permalinks and heading IDs when changing content. Internal plans and audits are excluded from the published artifact.

Documentation development uses Ruby 3.4.12 and Bundler 4.0.24. Dependencies and checksums are locked in `docs/Gemfile.lock`; Ruby is only needed to build or preview the documentation:

```bash
gem install bundler -v 4.0.24
cd docs
bundle install
cd ..
make docs-check
```

`make docs-check` builds production HTML into `.build/docs` and checks local links, anchors, unique titles and descriptions, canonical URLs, the sitemap and the public file catalog. Build output is ignored by Git. CI and Pages use this same locked build and artifact check. To preview, run `bundle exec jekyll serve --strict_front_matter --destination ../.build/docs` from `docs`, then open `http://127.0.0.1:4000/asura/`. On PowerShell without Make, build and check directly:

```powershell
$env:JEKYLL_ENV = 'production'
Push-Location docs
bundle exec jekyll build --strict_front_matter --destination ../.build/docs
Pop-Location
go run ./cmd/docscheck -dir .build/docs
```

For a fork or another host, set the HTTPS origin in `docs/_config.yml` as `url` and the deployment path as `baseurl` (empty for the origin root). The build derives navigation, canonical links, social metadata and the sitemap from that configuration. Local compilation does not publish the site or submit it to search engines.

`make release` builds the server and agent for Linux amd64 and arm64 and writes all four checksums to `dist/checksums.txt`. It uses the committed generated assets, so run `make generate css` before preparing a local release. CI retains a coverage profile for each Go version for 14 days.

The release workflow requires the reusable CI checks before publishing binaries or images. CI scans source on both supported Go patches and scans the four built binaries with pinned govulncheck. Publication generates GitHub build attestations for the binaries, checksum manifest and image digest; container builds also request BuildKit provenance. These publish steps run only for release tags. Action revisions, Go patches, base-image digests and the downloaded Tailwind checksum are pinned. Hosted runner images and package repositories can change; these controls do not imply a reproducible build or a SLSA level. Local `make release` creates binaries and checksums without publishing or signing them.

Release tags must be valid `v`-prefixed semantic versions, such as `v2.0.0` or `v2.0.0-rc.1`. Prereleases are marked as such on GitHub and publish only their full container version tag; stable releases also update `major.minor` and `latest`. Build metadata is retained in image tags with `+` replaced by `_`, and container tags must fit the 128-character limit. Missing listed release artifacts fail publication.

## Ownership and parallel work

Use a separate branch and worktree for each worker. Give each task a common base commit, owned paths, expected behavior, dependencies and a focused test command. Workers hand off source changes, exact check outcomes and unresolved issues; one integration owner reviews and combines them.

| Area | Shared contract to coordinate |
| --- | --- |
| Checks and probe agents | Pure types in `internal/model`, agent job/result protocol |
| Storage and permissions | `storage.Store`, transaction boundaries, scopes and migration versions |
| API and web handlers | Route registration, request validation, response and secret policy |
| Interface and docs | Shared templ components, design tokens, generator inputs |
| Runtime and delivery | Startup/shutdown, configuration, CI and release wiring |

Integrate dependent sources in order and regenerate on the combined tree. Resolve generated conflicts from their source files. Never renumber a migration that has been applied. Keep private configuration and test data separate between worktrees; sharing the Go caches is fine. Run focused checks in each worker, then broader checks after integration when the combined change warrants them.

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
- Keep checker and agent dependencies independent of server storage. Preserve the dependency guard in `scripts/test-agent-deps.sh`.
- Edit `.templ` and CSS, then regenerate. Use the templ version in `go.mod` and the pinned standalone Tailwind CLI. Check Markdown documentation with the locked Jekyll build.

## Issues

For bugs, include the Asura and Go versions, operating system, reproduction steps, expected and actual behavior, and sanitized logs. Discuss substantial features or compatibility changes in an issue before opening a large pull request. Existing `good first issue` and `help wanted` labels are useful starting points.

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
