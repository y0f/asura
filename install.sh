#!/usr/bin/env bash
set -euo pipefail

# Asura – one-command VPS installer
# Usage: sudo bash install.sh

GO_VERSION="1.27.3"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR="/usr/local/bin"
LICENSE_DIR="/usr/share/licenses/asura"
CONFIG_DIR="/etc/asura"
DATA_DIR="/var/lib/asura"
SERVICE_USER="asura"
GO_INSTALL_ROOT="/usr/local/go"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[+]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[-]${NC} $*"; exit 1; }

cleanup() {
    if [[ $? -ne 0 ]]; then
        echo -e "\n${RED}[-]${NC} Installation failed. Check the output above for details."
        echo -e "${YELLOW}[!]${NC} Partial state may remain — review before re-running."
    fi
}

version_ge() {
    printf '%s\n%s\n' "$2" "$1" | sort -V -C
}

local_go_version() {
    local output
    output=$(GOTOOLCHAIN=local "$1" version 2>/dev/null) || return 1
    [[ "$output" =~ ^go\ version\ go([0-9]+\.[0-9]+\.[0-9]+)\  ]] || return 1
    printf '%s\n' "${BASH_REMATCH[1]}"
}

supported_go_version() {
    version_ge "$1" "$GO_MIN_VERSION" || return 1
    # The newer supported release line also needs its security patches.
    if [[ "${1%.*}" == "${GO_VERSION%.*}" ]]; then
        version_ge "$1" "$GO_VERSION" || return 1
    fi
}

select_go() {
    GO_MIN_VERSION=$(awk '$1 == "go" { print $2; exit }' "${SCRIPT_DIR}/go.mod")
    [[ "$GO_MIN_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || error "Could not read the required Go version from ${SCRIPT_DIR}/go.mod"
    version_ge "$GO_VERSION" "$GO_MIN_VERSION" || error "Pinned Go ${GO_VERSION} is older than go.mod requires (${GO_MIN_VERSION})"

    GO_BIN=$(type -P go || true)
    if [[ -n "$GO_BIN" ]]; then
        GO_BIN="$(cd "$(dirname "$GO_BIN")" && pwd)/$(basename "$GO_BIN")"
    fi
    local current_go
    if [[ -z "$GO_BIN" ]] || ! current_go=$(local_go_version "$GO_BIN") || ! supported_go_version "$current_go"; then
        warn "Installing a patched Go toolchain (minimum ${GO_MIN_VERSION}, preferred ${GO_VERSION})"
        install_go
    fi

    current_go=$(local_go_version "$GO_BIN") || error "Could not verify Go at ${GO_BIN}"
    supported_go_version "$current_go" || error "Go ${current_go} at ${GO_BIN} is not supported"
    info "Using Go ${current_go}: ${GO_BIN}"
}

stage_go_install() (
    info "Installing Go ${GO_VERSION} (${GOARCH})..."
    local tarball="go${GO_VERSION}.linux-${GOARCH}.tar.gz"

    local expected_sha
    case "$GOARCH" in
        amd64) expected_sha="65efbc80fdc1fa2cd2631cad5b481777a68e8725f34e89f50fc6ac714e379b40" ;;
        arm64) expected_sha="a06e99309dbcbdca1a8ccd2282c3ee146e55930886e26b4f1193269587165f49" ;;
        *)     error "No pinned Go checksum for ${GOARCH}" ;;
    esac

    local stage installed=0
    stage=$(mktemp -d "${GO_INSTALL_ROOT}.stage.XXXXXX") || error "Could not create Go staging directory"
    cleanup_go_stage() {
        local status=$?
        trap - EXIT HUP INT TERM
        if [[ "$installed" == 0 && ( -e "$stage/previous" || -L "$stage/previous" ) ]]; then
            if [[ -e "$GO_INSTALL_ROOT" || -L "$GO_INSTALL_ROOT" ]] || ! mv -T -- "$stage/previous" "$GO_INSTALL_ROOT"; then
                warn "Previous Go installation preserved at ${stage}/previous; restore it before retrying"
                return 1
            fi
        fi
        rm -rf -- "$stage" || return 1
        return "$status"
    }
    trap cleanup_go_stage EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM

    curl -fsSL "https://go.dev/dl/${tarball}" -o "$stage/$tarball" || error "Go download failed"

    # Verify authenticity against the pinned SHA-256 before extracting as root
    if ! echo "${expected_sha}  ${stage}/${tarball}" | sha256sum -c - >/dev/null 2>&1; then
        error "Go tarball checksum mismatch for ${tarball} — refusing to install"
    fi

    tar -C "$stage" -xzf "$stage/$tarball" || error "Could not extract Go archive"
    local staged_version
    staged_version=$(local_go_version "$stage/go/bin/go") || error "Could not verify staged Go executable"
    [[ "$staged_version" == "$GO_VERSION" ]] || error "Staged Go version does not match ${GO_VERSION}"

    if [[ -e "$GO_INSTALL_ROOT" || -L "$GO_INSTALL_ROOT" ]]; then
        mv -T -- "$GO_INSTALL_ROOT" "$stage/previous" || error "Could not preserve the previous Go installation"
    fi
    mv -T -- "$stage/go" "$GO_INSTALL_ROOT" || error "Could not install staged Go"
    installed=1
)

install_go() {
    stage_go_install || return
    GO_BIN="${GO_INSTALL_ROOT}/bin/go"
}

build_asura() {
    # Use the same executable and local toolchain that select_go verified.
    GOTOOLCHAIN=local GOWORK=off GOFLAGS= GOEXPERIMENT= CGO_ENABLED=0 GOAMD64=v1 GOARM64=v8.0 GOOS=linux GOARCH="$GOARCH" \
        "$GO_BIN" build -trimpath -ldflags="-s -w -X main.version=$1" -o asura ./cmd/asura
}

verify_notices() {
    # Child go commands must use the executable selected above, even when an
    # obsolete installation or relative PATH entry originally shadowed it.
    GOTOOLCHAIN=local GOWORK=off GOFLAGS= GOEXPERIMENT= CGO_ENABLED=0 GOAMD64=v1 GOARM64=v8.0 \
        PATH="$(dirname "$GO_BIN"):$PATH" "$GO_BIN" run ./cmd/notices -check -verify
}

install_notices() {
    install -d -m 755 "$LICENSE_DIR"
    install -m 644 "$SCRIPT_DIR/LICENSE" "$SCRIPT_DIR/THIRD_PARTY_NOTICES.txt" "$LICENSE_DIR/"
}

# Sourcing exposes the toolchain helpers without installing a service.
[[ "${BASH_SOURCE[0]}" != "$0" ]] && return

trap cleanup EXIT

[[ $EUID -ne 0 ]] && error "Run this script as root (sudo bash install.sh)"

command -v systemctl >/dev/null 2>&1 || error "systemd is required"
command -v curl >/dev/null 2>&1 || error "curl is required (apt install curl)"

ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  GOARCH="amd64" ;;
    aarch64) GOARCH="arm64" ;;
    *)       error "Unsupported architecture: $ARCH" ;;
esac

select_go

VERSION=$(git -C "$SCRIPT_DIR" describe --tags --always --dirty 2>/dev/null || echo "dev")

info "Building asura ${VERSION} from ${SCRIPT_DIR}..."
cd "$SCRIPT_DIR"
verify_notices
build_asura "$VERSION"
install_notices
install -m 755 asura "${INSTALL_DIR}/asura"
rm -f asura
info "Binary installed to ${INSTALL_DIR}/asura"

# ── System user ────────────────────────────────────────────────────

if id "$SERVICE_USER" &>/dev/null; then
    info "User '${SERVICE_USER}' already exists"
else
    useradd --system --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
    info "Created system user '${SERVICE_USER}'"
fi

# ── Directories ────────────────────────────────────────────────────

mkdir -p "$CONFIG_DIR"
mkdir -p "$DATA_DIR"
chown "$SERVICE_USER":"$SERVICE_USER" "$DATA_DIR"
chmod 700 "$DATA_DIR"

# ── Generate config ────────────────────────────────────────────────

if [[ -f "${CONFIG_DIR}/config.yaml" ]]; then
    warn "Config already exists at ${CONFIG_DIR}/config.yaml — skipping generation"
    ADMIN_KEY="(existing config preserved)"
else
    SETUP_OUTPUT=$("${INSTALL_DIR}/asura" -setup)
    ADMIN_KEY=$(echo "$SETUP_OUTPUT" | grep 'API Key' | awk '{print $NF}')
    ADMIN_HASH=$(echo "$SETUP_OUTPUT" | grep 'Hash' | awk '{print $NF}')

    cat > "${CONFIG_DIR}/config.yaml" <<'YAML'
server:
  listen: "127.0.0.1:8090"
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s
  rate_limit_per_sec: 10
  rate_limit_burst: 20

  # Serve under a sub-path (e.g. "/asura" for https://example.com/asura)
  # base_path: "/asura"

  # Public URL for notification links (optional, auto-detected if unset)
  # external_url: "https://example.com/asura"

  # Trusted reverse proxy IPs/CIDRs for X-Forwarded-For / X-Real-IP
  # trusted_proxies:
  #   - "127.0.0.1"
  #   - "::1"

database:
  path: "PLACEHOLDER_DATA_DIR/asura.db"
  max_read_conns: 4
  retention_days: 90
  retention_period: 1h

auth:
  api_keys:
    - name: "admin"
      hash: "PLACEHOLDER_HASH"
      super_admin: true
  session:
    lifetime: 24h
    cookie_secure: true  # Secure cookies; set false only for direct HTTP access without a TLS reverse proxy

monitor:
  workers: 10
  default_timeout: 10s
  default_interval: 60s
  failure_threshold: 3
  success_threshold: 1

logging:
  level: "info"
  format: "text"
YAML

    sed -i "s|PLACEHOLDER_DATA_DIR|${DATA_DIR}|g" "${CONFIG_DIR}/config.yaml"
    sed -i "s|PLACEHOLDER_HASH|${ADMIN_HASH}|g" "${CONFIG_DIR}/config.yaml"

    chmod 640 "${CONFIG_DIR}/config.yaml"
    chown root:"$SERVICE_USER" "${CONFIG_DIR}/config.yaml"
    info "Config written to ${CONFIG_DIR}/config.yaml"
fi

# ── Systemd service ───────────────────────────────────────────────

cp "${SCRIPT_DIR}/asura.service" /etc/systemd/system/asura.service
systemctl daemon-reload
systemctl enable asura
systemctl restart asura
info "Systemd service installed and started"

# ── Done ───────────────────────────────────────────────────────────

echo ""
echo "============================================"
echo "  Asura installed successfully!"
echo "============================================"
echo ""
echo "  Admin API key : ${ADMIN_KEY}"
echo "  Config file   : ${CONFIG_DIR}/config.yaml"
echo "  Database      : ${DATA_DIR}/asura.db"
echo "  Binary        : ${INSTALL_DIR}/asura"
echo ""
echo "  Health check  : curl http://127.0.0.1:8090/api/v1/health"
echo "  Service       : systemctl status asura"
echo ""
echo "  IMPORTANT: Asura binds to 127.0.0.1 by default (not exposed)."
echo "  Set up a reverse proxy (nginx/caddy) to expose it securely."
echo "  See README.md for reverse proxy configuration examples."
echo ""
echo "  Save your admin API key — it cannot be recovered."
echo ""
