#!/usr/bin/env bash
set -euo pipefail

# Asura – one-command VPS installer
# Usage: sudo bash install.sh

GO_VERSION="1.24.0"
GO_MIN_VERSION="1.24"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/asura"
DATA_DIR="/var/lib/asura"
SERVICE_USER="asura"

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
trap cleanup EXIT

# ── Pre-flight checks ──────────────────────────────────────────────

[[ $EUID -ne 0 ]] && error "Run this script as root (sudo bash install.sh)"

command -v systemctl >/dev/null 2>&1 || error "systemd is required"
command -v curl >/dev/null 2>&1 || error "curl is required (apt install curl)"

ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  GOARCH="amd64" ;;
    aarch64) GOARCH="arm64" ;;
    *)       error "Unsupported architecture: $ARCH" ;;
esac

# ── Version check helper ──────────────────────────────────────────

version_ge() {
    printf '%s\n%s\n' "$2" "$1" | sort -V -C
}

# ── Install Go if missing or too old ─────────────────────────────

install_go() {
    info "Installing Go ${GO_VERSION} (${GOARCH})..."
    TARBALL="go${GO_VERSION}.linux-${GOARCH}.tar.gz"

    local expected_sha
    case "$GOARCH" in
        amd64) expected_sha="dea9ca38a0b852a74e81c26134671af7c0fbe65d81b0dc1c5bfe22cf7d4c8858" ;;
        arm64) expected_sha="c3fa6d16ffa261091a5617145553c71d21435ce547e44cc6dfb7470865527cc7" ;;
        *)     error "No pinned Go checksum for ${GOARCH}" ;;
    esac

    curl -fsSL "https://go.dev/dl/${TARBALL}" -o "/tmp/${TARBALL}"

    # Verify authenticity against the pinned SHA-256 before extracting as root
    if ! echo "${expected_sha}  /tmp/${TARBALL}" | sha256sum -c - >/dev/null 2>&1; then
        rm -f "/tmp/${TARBALL}"
        error "Go tarball checksum mismatch for ${TARBALL} — refusing to install"
    fi

    rm -rf /usr/local/go
    tar -C /usr/local -xzf "/tmp/${TARBALL}"
    rm -f "/tmp/${TARBALL}"
    export PATH="/usr/local/go/bin:$PATH"
    info "Go installed: $(go version)"
}

if command -v go >/dev/null 2>&1; then
    CURRENT_GO=$(go version | grep -oP '\d+\.\d+(\.\d+)?' | head -1)
    if version_ge "$CURRENT_GO" "$GO_MIN_VERSION"; then
        info "Go already installed: $(go version)"
    else
        warn "Go ${CURRENT_GO} is too old (need >= ${GO_MIN_VERSION})"
        install_go
    fi
else
    install_go
fi

export PATH="/usr/local/go/bin:$PATH"

# ── Build ──────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VERSION=$(git -C "$SCRIPT_DIR" describe --tags --always --dirty 2>/dev/null || echo "dev")

info "Building asura ${VERSION} from ${SCRIPT_DIR}..."
cd "$SCRIPT_DIR"
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o asura ./cmd/asura
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
      role: "admin"
  session:
    lifetime: 24h
    cookie_secure: false  # Set to true after configuring TLS via reverse proxy

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
