#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/install.sh"

TEST_DIR=$(mktemp -d)
trap 'rm -rf "$TEST_DIR"' EXIT
TEST_TOOLS="$TEST_DIR/tools"
mkdir -p "$TEST_TOOLS"
for name in awk basename bash cat chmod dirname grep mkdir sort; do
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
    [[ \$1 == build && \$CGO_ENABLED == 0 ]] || exit 92
    printf '%s\n' "\$0" > "\$INSTALL_TEST_BUILT_WITH"
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
    build_asura fixture
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

# A failed download or checksum must stop before removing an installed toolchain.
for failure in download checksum; do
    failure_log="$TEST_DIR/$failure.log"
    set +e
    (
        set -e
        GOARCH=amd64
        curl() {
            [[ "$failure" != download ]] || return 22
        }
        sha256sum() { return 1; }
        rm() { printf 'rm %s\n' "$*" >> "$failure_log"; }
        tar() { printf 'tar %s\n' "$*" >> "$failure_log"; }
        install_go
        echo 'unexpected success'
    ) > "$TEST_DIR/$failure.output" 2>&1
    code=$?
    set -e
    [[ "$code" -ne 0 ]]
    if [[ -e "$failure_log" ]]; then
        ! grep -qE '/usr/local|^tar ' "$failure_log"
    fi
    echo "PASS $failure-failure"
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
