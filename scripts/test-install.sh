#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/install.sh"

TEST_DIR=$(mktemp -d)
trap 'rm -rf "$TEST_DIR"' EXIT
TEST_TOOLS="$TEST_DIR/tools"
mkdir -p "$TEST_TOOLS"
for name in awk basename bash cat chmod dirname grep install mkdir sort; do
    ln -s "$(type -P "$name")" "$TEST_TOOLS/$name"
done

# No real toolchain installation, build, or systemd command is used.
write_go() {
    mkdir -p "$1"
    cat > "$1/go" <<EOF
#!/usr/bin/env bash
set -eu
[[ \$GOTOOLCHAIN == local ]] || exit 91
printf '%s %s %s\n' "\$0" "\$GOTOOLCHAIN" "\$*" >> "\$INSTALL_TEST_LOG"
if [[ \$1 == version ]]; then
    echo 'go version $2 linux/amd64'
else
    [[ \$CGO_ENABLED == 0 && \$GOWORK == off && -z \$GOFLAGS && -z \$GOEXPERIMENT ]] || exit 92
    [[ \$GOAMD64 == v1 && \$GOARM64 == v8.0 ]] || exit 92
    if [[ \$1 == run ]]; then
        [[ "\$*" == 'run ./cmd/notices -check -verify' ]] || exit 92
        [[ "\$(type -P go)" == "\$0" ]] || exit 92
        printf '%s\n' "\$0" > "\$INSTALL_TEST_VERIFIED_WITH"
    else
        [[ \$1 == build && \$GOOS == linux && \$GOARCH == amd64 ]] || exit 92
        printf '%s\n' "\$0" > "\$INSTALL_TEST_BUILT_WITH"
    fi
fi
EOF
    chmod +x "$1/go"
}

selection_case() (
    name=$1 version=$2 expected=$3
    case_dir="$TEST_DIR/$name"
    mkdir -p "$case_dir/project" "$case_dir/other"
    printf 'module fixture\n\ngo 1.26.10\n\ntoolchain go1.27.3\n' > "$case_dir/project/go.mod"
    SCRIPT_DIR="$case_dir/project"
    export INSTALL_TEST_LOG="$case_dir/go.log"
    export INSTALL_TEST_BUILT_WITH="$case_dir/built-with"
    export INSTALL_TEST_VERIFIED_WITH="$case_dir/verified-with"
    export GOTOOLCHAIN=go1.99.0+auto
    export PATH="$case_dir/first:$case_dir/shadow:$TEST_TOOLS"
    if [[ "$version" != missing ]]; then
        write_go "$case_dir/first" "$version"
    fi
    if [[ "$name" == shadow ]]; then
        write_go "$case_dir/shadow" go1.25.0
    fi
    if [[ "$name" == relative ]]; then
        cd "$case_dir"
        export PATH="first:$TEST_TOOLS"
    fi
    # A shell function must not replace the external executable being checked.
    go() { exit 93; }
    install_go() {
        printf 'install\n' > "$case_dir/installed"
        write_go "$case_dir/downloaded" go1.27.3
        GO_BIN="$case_dir/downloaded/go"
    }

    select_go
    if [[ "$expected" == keep ]]; then
        [[ ! -e "$case_dir/installed" ]]
        [[ "$GO_BIN" == "$case_dir/first/go" ]]
    else
        [[ -e "$case_dir/installed" ]]
        [[ "$GO_BIN" == "$case_dir/downloaded/go" ]]
    fi
    # Build still uses the verified absolute path after both cwd and PATH change.
    write_go "$case_dir/later-shadow" go1.25.0
    cd "$case_dir/other"
    export PATH="$case_dir/later-shadow:$TEST_TOOLS"
    export GOFLAGS='-tags unreviewed' GOWORK='/unreviewed/workspace' GOEXPERIMENT='unreviewed' CGO_ENABLED=1
    GOARCH=amd64
    verify_notices
    build_asura fixture
    [[ "$(cat "$INSTALL_TEST_VERIFIED_WITH")" == "$GO_BIN" ]]
    [[ "$(cat "$INSTALL_TEST_BUILT_WITH")" == "$GO_BIN" ]]
    echo "PASS $name"
)

selection_case minimum go1.26.10 keep
selection_case minimum-patch go1.26.11 keep
selection_case preferred go1.27.3 keep
selection_case preferred-patch go1.27.4 keep
selection_case newer go1.28.0 keep
selection_case shadow go1.27.3 keep
selection_case relative go1.27.3 keep
selection_case missing missing install
selection_case old-minimum go1.26.0 install
selection_case old-patch go1.26.9 install
selection_case old-preferred go1.27.2 install
selection_case prerelease go1.28rc1 install
selection_case malformed unknown install

# All paths and executables below belong to the disposable fixture.
installation_case() (
    local failure=$1 case_dir="$TEST_DIR/install-$1"
    mkdir -p "$case_dir"
    GO_INSTALL_ROOT="$case_dir/go"
    GOARCH=amd64
    export INSTALL_TEST_LOG="$case_dir/go.log"
    export INSTALL_TEST_BUILT_WITH="$case_dir/built-with"
    if [[ "$failure" != fresh ]]; then
        write_go "$GO_INSTALL_ROOT/bin" go1.26.0
        printf 'preserve me\n' > "$GO_INSTALL_ROOT/old-marker"
    fi

    curl() {
        [[ "$failure" != download ]] || return 22
        local archive=${@: -1}
        [[ "$archive" == "$GO_INSTALL_ROOT".stage.*/go*.tar.gz ]] || return 94
        [[ "$(stat -c %a "$(dirname "$archive")")" == 700 ]] || return 95
        printf 'fixture archive\n' > "$archive"
    }
    sha256sum() { [[ "$failure" != checksum ]]; }
    tar() {
        [[ "$1" == -C && "$3" == -xzf ]] || return 96
        [[ "$2" == "$GO_INSTALL_ROOT".stage.* ]] || return 97
        [[ "$failure" != extraction ]] || return 2
        local version=go1.27.3
        [[ "$failure" != version ]] || version=go1.27.2
        write_go "$2/go/bin" "$version"
        if [[ "$failure" == executable ]]; then
            printf '#!/usr/bin/env bash\nexit 1\n' > "$2/go/bin/go"
        fi
    }
    mv() {
        local src=$3 dst=$4
        if [[ "$src" == "$GO_INSTALL_ROOT" && "$failure" == backup ]]; then
            return 73
        fi
        if [[ "$src" == "$GO_INSTALL_ROOT".stage.*/go ]]; then
            if [[ "$failure" == replacement || "$failure" == rollback ]]; then
                return 74
            fi
            if [[ "$failure" == interrupted ]]; then
                kill -TERM "$BASHPID"
            fi
        fi
        if [[ "$src" == "$GO_INSTALL_ROOT".stage.*/previous && "$failure" == rollback ]]; then
            return 75
        fi
        command mv "$@"
    }

    set +e
    (
        set -e
        install_go
        [[ "$GO_BIN" == "$GO_INSTALL_ROOT/bin/go" ]]
    ) > "$case_dir/output" 2>&1
    code=$?
    set -e

    if [[ "$failure" == success || "$failure" == fresh ]]; then
        [[ "$code" == 0 ]]
        [[ "$(local_go_version "$GO_INSTALL_ROOT/bin/go")" == 1.27.3 ]]
        [[ ! -e "$GO_INSTALL_ROOT/old-marker" ]]
    elif [[ "$failure" == rollback ]]; then
        [[ "$code" != 0 ]]
        local previous
        previous=$(find "$case_dir" -path '*/previous/old-marker')
        [[ -n "$previous" && "$(cat "$previous")" == 'preserve me' ]]
        [[ "$(local_go_version "$(dirname "$previous")/bin/go")" == 1.26.0 ]]
        grep -q 'Previous Go installation preserved' "$case_dir/output"
    else
        [[ "$code" != 0 ]]
        [[ "$(cat "$GO_INSTALL_ROOT/old-marker")" == 'preserve me' ]]
        [[ "$(local_go_version "$GO_INSTALL_ROOT/bin/go")" == 1.26.0 ]]
    fi
    if [[ "$failure" != rollback ]]; then
        [[ -z "$(find "$case_dir" -maxdepth 1 -name 'go.stage.*' -print -quit)" ]]
    fi
    echo "PASS install-$failure"
)

for failure in fresh success download checksum extraction version executable backup replacement interrupted rollback; do
    installation_case "$failure"
done

# A replacement must be validated again, including its local toolchain version.
set +e
(
    set -e
    SCRIPT_DIR="$TEST_DIR/recheck"
    mkdir -p "$SCRIPT_DIR"
    printf 'go 1.26.10\n' > "$SCRIPT_DIR/go.mod"
    export PATH="$TEST_TOOLS"
    export INSTALL_TEST_LOG="$TEST_DIR/recheck.log"
    install_go() {
        write_go "$TEST_DIR/bad-replacement" go1.27.2
        GO_BIN="$TEST_DIR/bad-replacement/go"
    }
    select_go
) > "$TEST_DIR/recheck.output" 2>&1
code=$?
set -e
[[ "$code" -ne 0 ]]
grep -q 'is not supported' "$TEST_DIR/recheck.output"
echo 'PASS replacement-recheck'

# Exercise only the copy helper against a synthetic private destination.
(
    SCRIPT_DIR="$TEST_DIR/notice-source"
    LICENSE_DIR="$TEST_DIR/notice-install"
    mkdir -p "$SCRIPT_DIR"
    printf 'Project license fixture\n' > "$SCRIPT_DIR/LICENSE"
    printf 'Dependency notice fixture\n' > "$SCRIPT_DIR/THIRD_PARTY_NOTICES.txt"
    install_notices
    cmp "$SCRIPT_DIR/LICENSE" "$LICENSE_DIR/LICENSE"
    cmp "$SCRIPT_DIR/THIRD_PARTY_NOTICES.txt" "$LICENSE_DIR/THIRD_PARTY_NOTICES.txt"
    [[ "$(stat -c %a "$LICENSE_DIR/LICENSE")" == 644 ]]
    [[ "$(stat -c %a "$LICENSE_DIR/THIRD_PARTY_NOTICES.txt")" == 644 ]]
    [[ "$(stat -c %a "$LICENSE_DIR")" == 755 ]]
    echo 'PASS installed-notices'
)
