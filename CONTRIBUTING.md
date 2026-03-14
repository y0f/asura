# Contributing to Asura

Thanks for your interest in contributing. This guide covers the basics.

## Running Locally

Requires Go 1.24+ and [templ](https://templ.guide/) CLI:

```bash
go install github.com/a-h/templ/cmd/templ@v0.3.977
```

```bash
# copy and edit config
cp config.example.yaml config.yaml

# generate an API key + hash, paste the hash into config.yaml under auth.api_keys[].hash
go run ./cmd/asura --setup

# set cookie_secure: false in config.yaml (no TLS locally)
```

`config.yaml` is gitignored — it won't be committed.

## Development Workflow

One command does everything — watches for file changes, rebuilds, and restarts the server automatically:

**Linux / macOS / Git Bash:**
```bash
make dev
```

**Windows (PowerShell):**
```powershell
.\dev.ps1
```

This watches all `.go` and `.templ` files, plus runs the Tailwind CSS watcher. When you save a file, the server rebuilds and restarts within a few seconds.

Open http://localhost:8090 and log in with the API key from `--setup`.

### Manual workflow (if you prefer)

Run each in a separate terminal:

```bash
# terminal 1: watch templates + CSS
templ generate --watch
./tailwindcss -i web/tailwind.input.css -o web/static/tailwind.css --watch

# terminal 2: build and run (re-run after .go changes)
go build -o asura ./cmd/asura && ./asura -config config.yaml
```

### Running tests

```bash
make test
# or
go test -race -count=1 ./...
```

Note: `-race` requires CGO on Windows. CI runs tests on Linux with race detection.

## Contributing Steps

1. Fork the repo and create a branch from `main`
2. Run `make dev` (or `.\dev.ps1` on Windows)
3. Make your changes — server auto-restarts on save
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
- No dependencies unless truly necessary — keep the binary small
- Error messages are lowercase, no trailing punctuation
- Table-driven tests where applicable
- Web UI templates use [templ](https://templ.guide/) — run `templ generate` after editing `.templ` files

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
