#!/usr/bin/env bash
# Rebuild templates, CSS and the embedded server when application sources change.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")"

BINARY=./asura-dev
case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) BINARY+=.exe ;; esac
NEXT="${BINARY}.next"
CONFIG=config.yaml
TAILWIND=./tailwindcss
[[ ! -x ./tailwindcss.exe ]] || TAILWIND=./tailwindcss.exe
[[ -x "$TAILWIND" ]] || { echo '[dev] Download the standalone Tailwind CLI first (see CONTRIBUTING.md).' >&2; exit 1; }
PID=""

stop_server() {
    if [[ -n "$PID" ]]; then
        kill "$PID" 2>/dev/null || true
        wait "$PID" 2>/dev/null || true
        PID=""
    fi
}
cleanup() {
    stop_server
    rm -f -- "$BINARY" "$NEXT"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

build_and_run() {
    local templ_version
    templ_version=$(go list -m -f '{{.Version}}' github.com/a-h/templ) || return 1
    echo '[dev] generating templates and CSS...'
    go run "github.com/a-h/templ/cmd/templ@${templ_version}" generate || return 1
    "$TAILWIND" -i web/tailwind.input.css -o web/static/tailwind.css || return 1
    echo '[dev] building...'
    CGO_ENABLED=0 go build -o "$NEXT" ./cmd/asura || return 1
    stop_server
    mv -f -- "$NEXT" "$BINARY" || return 1
    echo '[dev] starting server...'
    "$BINARY" -config "$CONFIG" &
    PID=$!
}

snapshot() {
    # Generated outputs are refreshed together; watch their inputs and other assets.
    { find cmd internal web -type f ! -name '*_templ.go' ! -path web/static/tailwind.css -exec cksum {} +; cksum go.mod go.sum; } | LC_ALL=C sort
}

LAST_HASH=$(snapshot)
build_and_run || echo '[dev] build failed; waiting for a source change.' >&2
echo '[dev] watching application sources and embedded assets (Ctrl+C to stop)...'
while sleep 2; do
    HASH=$(snapshot)
    if [[ "$HASH" != "$LAST_HASH" ]]; then
        LAST_HASH=$HASH
        build_and_run || echo '[dev] build failed; keeping the previous server.' >&2
    fi
done
