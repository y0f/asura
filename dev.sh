#!/usr/bin/env bash
# dev.sh — single-command local development
# Watches .go, .templ, and .css files. Rebuilds and restarts on change.
# Usage: ./dev.sh
# Stop:  Ctrl+C

set -e

BINARY="./asura-dev"
CONFIG="config.yaml"
TAILWIND="./tailwindcss"
PID=""

cleanup() {
    [ -n "$PID" ] && kill "$PID" 2>/dev/null
    [ -n "$TEMPL_PID" ] && kill "$TEMPL_PID" 2>/dev/null
    [ -n "$TW_PID" ] && kill "$TW_PID" 2>/dev/null
    rm -f "$BINARY" "$BINARY.exe"
    exit 0
}
trap cleanup INT TERM

build_and_run() {
    [ -n "$PID" ] && kill "$PID" 2>/dev/null && wait "$PID" 2>/dev/null
    sleep 0.3
    echo "[dev] building..."
    templ generate 2>&1 | tail -1
    if go build -o "$BINARY" ./cmd/asura 2>&1; then
        echo "[dev] starting server on $(grep 'listen:' "$CONFIG" | head -1 | awk '{print $2}' | tr -d '\"')"
        "$BINARY" -config "$CONFIG" &
        PID=$!
    else
        echo "[dev] build failed"
        PID=""
    fi
}

# Start tailwind watcher in background
if [ -x "$TAILWIND" ] || [ -x "${TAILWIND}.exe" ]; then
    TW="${TAILWIND}"
    [ -x "${TAILWIND}.exe" ] && TW="${TAILWIND}.exe"
    $TW -i web/tailwind.input.css -o web/static/tailwind.css --watch &>/dev/null &
    TW_PID=$!
    echo "[dev] tailwind watcher started"
fi

# Initial build
build_and_run

# Watch for .go and .templ file changes
echo "[dev] watching for changes... (Ctrl+C to stop)"
LAST_HASH=""
while true; do
    # Hash all .go and .templ files to detect changes
    HASH=$(find cmd internal -name '*.go' -o -name '*.templ' 2>/dev/null | sort | xargs stat -c '%Y%n' 2>/dev/null || find cmd internal -name '*.go' -o -name '*.templ' 2>/dev/null | sort | xargs stat -f '%m%N' 2>/dev/null || echo "")
    if [ -n "$HASH" ] && [ "$HASH" != "$LAST_HASH" ] && [ -n "$LAST_HASH" ]; then
        echo ""
        echo "[dev] change detected, rebuilding..."
        build_and_run
    fi
    LAST_HASH="$HASH"
    sleep 2
done
