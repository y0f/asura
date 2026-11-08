#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

dependencies=$(go list -deps ./cmd/asura)
if printf '%s\n' "$dependencies" | grep -E '^github\.com/jackc/'; then
    echo "Default server must not link the optional PostgreSQL driver" >&2
    exit 1
fi

dependencies=$(go list -tags postgres -deps ./cmd/asura-agent)
if printf '%s\n' "$dependencies" | grep -E '^(database/sql($|/)|github\.com/y0f/asura/v2/internal/storage($|/)|modernc\.org/|github\.com/jackc/)'; then
    echo "Probe agent must stay independent of both database backends" >&2
    exit 1
fi
echo "Optional PostgreSQL dependency boundary passed"
