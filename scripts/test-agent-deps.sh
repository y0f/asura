#!/usr/bin/env bash
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

dependencies=$(go list -deps ./cmd/asura-agent)
if printf '%s\n' "$dependencies" | grep -E '^(database/sql($|/)|github\.com/y0f/asura/v3/internal/storage($|/)|modernc\.org/)'; then
    echo "Probe agents must not link database storage or drivers" >&2
    exit 1
fi
echo "Agent database dependency boundary passed"
