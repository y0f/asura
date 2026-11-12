#!/usr/bin/env bash
set -euo pipefail
script="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/release-tag.sh"
checks=0

accept() {
    local tag=$1 image_tag=$2 minor=$3 prerelease=$4 output
    output=$(bash "$script" "$tag")
    [[ $output == "$(printf 'image_tag=%s\nminor=%s\nprerelease=%s' "$image_tag" "$minor" "$prerelease")" ]] || {
        echo "Incorrect release metadata for $tag: $output" >&2; exit 1;
    }
    checks=$((checks + 1))
}

accept v3.0.0 3.0.0 3.0 false
accept v3.3.10 3.3.10 3.3 false
accept v3.0.0-rc.1 3.0.0-rc.1 3.0 true
accept v3.0.0-0 3.0.0-0 3.0 true
accept v3.0.0-01a 3.0.0-01a 3.0 true
accept v3.0.0-x-y-z.-- 3.0.0-x-y-z.-- 3.0 true
accept v3.0.0+build.001 3.0.0_build.001 3.0 false
accept v3.0.0-beta.1+build.001 3.0.0-beta.1_build.001 3.0 true
metadata=$(printf '%0122d' 0)
accept "v3.0.0+$metadata" "3.0.0_$metadata" 3.0 false

for tag in '' vnext 3.0.0 v0.0.0 v2.9.9 v4.0.0 v30.0.0 v3 v3.0 v03.0.0 v3.00.0 v3.0.00 v3.0.0-01 v3.0.0-a.01 \
    v3.0.0- v3.0.0-a..b v3.0.0-a. v3.0.0+ v3.0.0+b..c v3.0.0+b. \
    v3.0.0-a_b v3.0.0++b 'v3.0.0$(false)' $'v3.0.0\n' "v3.0.0+$(printf '%0123d' 0)"; do
    if bash "$script" "$tag" >/dev/null 2>&1; then
        echo "Invalid release tag accepted: $tag" >&2; exit 1
    fi
    checks=$((checks + 1))
done
echo "$checks release tag checks passed"
