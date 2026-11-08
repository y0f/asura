#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

tag=${1:-}
pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?(\+([0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*))?$'
if [[ ! $tag =~ $pattern ]]; then
    echo "Release tag must be v-prefixed semantic version 2.0.0" >&2
    exit 1
fi
major=${BASH_REMATCH[1]}
minor="${major}.${BASH_REMATCH[2]}"
prerelease=${BASH_REMATCH[5]}

# A release must be consumable under the module path in this checkout. Go
# requires a matching /vN suffix for major versions two and above.
repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
module_path=$(awk '$1 == "module" { print $2; exit }' "$repo/go.mod")
if [[ $module_path =~ /v([2-9]|[1-9][0-9]+)$ ]]; then
    if [[ $major != "${BASH_REMATCH[1]}" ]]; then
        echo "Release major must match the Go module path $module_path" >&2
        exit 1
    fi
elif [[ -z $module_path || ( $major != 0 && $major != 1 ) ]]; then
    echo "Release major does not match the Go module path" >&2
    exit 1
fi

IFS=. read -r -a identifiers <<< "$prerelease"
for identifier in "${identifiers[@]}"; do
    if [[ $identifier =~ ^0[0-9]+$ ]]; then
        echo "Numeric prerelease identifiers cannot have leading zeroes" >&2
        exit 1
    fi
done

# '+' is not valid in a container tag; '_' cannot occur in SemVer, so this
# mapping preserves the distinction between prerelease and build metadata.
image_tag=${tag#v}
image_tag=${image_tag//+/_}
if (( ${#image_tag} > 128 )); then
    echo "Release version exceeds the container tag length limit" >&2
    exit 1
fi
printf 'image_tag=%s\nminor=%s\nprerelease=%s\n' "$image_tag" "$minor" "$([[ -n $prerelease ]] && echo true || echo false)"
