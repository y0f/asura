# Contributing to Asura

Thanks for your interest in contributing. This guide covers the basics.

## Running Locally

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

## Development Workflow

One command does everything - watches for file changes, rebuilds, and restarts the server automatically:

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

### Running tests

```bash
make test
# or
go test -race -count=1 ./...
bash scripts/test-install.sh
bash scripts/test-dev.sh
bash scripts/test-release-tag.sh
bash scripts/test-agent-deps.sh
```

Note: `-race` requires CGO on Windows. CI runs tests on Linux with race detection.

Before committing generated output, run `make generate css`. Run it again and verify `git diff` stays unchanged. `make docs` rebuilds the static documentation and its CSS; `go run ./cmd/docsgen -check` checks documentation freshness. CI verifies generation without writing back to the branch, and gates release binaries and container publication on tests, formatting, static checks and clean generated output.

`make release` builds the server and agent for Linux amd64 and arm64 and writes all four checksums to `dist/checksums.txt`. It uses the committed generated assets, so run `make generate css` before preparing a local release. CI retains a coverage profile for each Go version for 14 days.

The release workflow requires the reusable CI checks before publishing binaries or images. CI scans source on both supported Go patches and scans the four built binaries with pinned govulncheck. Publication generates GitHub build attestations for the binaries, checksum manifest and image digest; container builds also request BuildKit provenance. These publish steps run only for release tags. Action revisions, Go patches, base-image digests and the downloaded Tailwind checksum are pinned. Hosted runner images and package repositories can change; these controls do not imply a reproducible build or a SLSA level. Local `make release` creates binaries and checksums without publishing or signing them.

Release tags must be valid `v`-prefixed semantic versions, such as `v2.0.0` or `v2.0.0-rc.1`. Prereleases are marked as such on GitHub and publish only their full container version tag; stable releases also update `major.minor` and `latest`. Build metadata is retained in image tags with `+` replaced by `_`, and container tags must fit the 128-character limit. Missing listed release artifacts fail publication.

## Contributing Steps

1. Fork the repo and create a branch from `main`
2. Run `make dev` (or `.\dev.ps1` on Windows)
3. Make your changes - server auto-restarts on save
4. Run tests to verify
5. Commit with a clear message (see below)
6. Open a pull request

## Commit Messages

Keep the subject line under 72 characters.

```
Add DNS record assertion type
Fix race condition in retention worker
Update SQLite dependency to v1.35
```

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- No dependencies unless truly necessary - keep the binary small
- Error messages are lowercase, no trailing punctuation
- Table-driven tests where applicable
- Web UI templates use [templ](https://templ.guide/) - run `make generate css` after editing `.templ` files

## What to Work On

- Check open issues for `good first issue` or `help wanted` labels
- Bug fixes and test coverage improvements are always welcome
- For larger features, open an issue first to discuss the approach

## Reporting Bugs

Open an issue with:
- Go version and OS
- Steps to reproduce
- Expected vs actual behavior
- Relevant log output

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
