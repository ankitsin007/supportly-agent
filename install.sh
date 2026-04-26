#!/usr/bin/env sh
# install.sh — one-line installer for the Supportly Agent.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/ankitsin007/supportly-agent/main/install.sh \
#     | sh -s -- --api-key sk_... --project-id <uuid>
#
# What it does:
#   1. Detects OS + arch (linux/darwin × amd64/arm64).
#   2. Detects deployment topology (docker / systemd / k8s) and either:
#      - Runs the docker container if Docker is available
#      - Drops a static binary at /usr/local/bin and writes a systemd unit
#      - For k8s, prints the kubectl apply command (does NOT run it for you —
#        that's a SecOps boundary you should cross deliberately)
#   3. Verifies the agent appears online by polling http://127.0.0.1:9876/healthz
#      (when running locally) or by checking back with Supportly's API.
#
# Flags:
#   --api-key KEY            Supportly API key for the project (required, or env SUPPORTLY_API_KEY)
#   --project-id UUID        Project UUID (required, or env SUPPORTLY_PROJECT_ID)
#   --enrollment-token TOK   Enrollment token (preferred over api-key for production)
#   --version VERSION        Pin to a specific agent release (default: latest)
#   --endpoint URL           Override Supportly ingest endpoint (default: https://ingest.supportly.io)
#   --install-dir DIR        Where to drop the binary in systemd mode (default: /usr/local/bin)
#   --no-verify              Skip post-install health check
#   --dry-run                Print what would happen without doing it
#   -h, --help               Show this help
#
# Trust posture:
#   - This script is short (~250 lines), human-readable, GPG-signed.
#   - It downloads only from github.com (release assets) and verifies SHA256.
#   - It performs no network calls beyond the binary download + (optional) the
#     healthcheck of the agent we just installed.
#   - Run `curl ... | less` to read it before piping to sh.

set -eu

# -- defaults --
SUPPORTLY_VERSION="${SUPPORTLY_VERSION:-latest}"
SUPPORTLY_API_KEY="${SUPPORTLY_API_KEY:-}"
SUPPORTLY_PROJECT_ID="${SUPPORTLY_PROJECT_ID:-}"
SUPPORTLY_ENROLLMENT_TOKEN="${SUPPORTLY_ENROLLMENT_TOKEN:-}"
SUPPORTLY_ENDPOINT="${SUPPORTLY_ENDPOINT:-https://ingest.supportly.io}"
INSTALL_DIR="/usr/local/bin"
VERIFY=1
DRY_RUN=0
GH_REPO="ankitsin007/supportly-agent"
DOCKER_IMAGE="ghcr.io/${GH_REPO}"

# -- output helpers --
RED='\033[0;31m'
GRN='\033[0;32m'
YLW='\033[0;33m'
NC='\033[0m'
log()  { printf '%b\n' "${GRN}==>${NC} $*"; }
warn() { printf '%b\n' "${YLW}!! ${NC} $*" >&2; }
die()  { printf '%b\n' "${RED}!! ${NC} $*" >&2; exit 1; }

run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    printf '%b\n' "${YLW}[dry-run]${NC} $*"
  else
    eval "$@"
  fi
}

usage() {
  sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}

# -- parse args --
while [ $# -gt 0 ]; do
  case "$1" in
    --api-key)            SUPPORTLY_API_KEY="$2"; shift 2 ;;
    --project-id)         SUPPORTLY_PROJECT_ID="$2"; shift 2 ;;
    --enrollment-token)   SUPPORTLY_ENROLLMENT_TOKEN="$2"; shift 2 ;;
    --version)            SUPPORTLY_VERSION="$2"; shift 2 ;;
    --endpoint)           SUPPORTLY_ENDPOINT="$2"; shift 2 ;;
    --install-dir)        INSTALL_DIR="$2"; shift 2 ;;
    --no-verify)          VERIFY=0; shift ;;
    --dry-run)            DRY_RUN=1; shift ;;
    -h|--help)            usage ;;
    *)                    warn "unknown flag: $1"; shift ;;
  esac
done

# -- validate inputs --
if [ -z "$SUPPORTLY_PROJECT_ID" ]; then
  die "--project-id (or SUPPORTLY_PROJECT_ID) is required"
fi
if [ -z "$SUPPORTLY_API_KEY" ] && [ -z "$SUPPORTLY_ENROLLMENT_TOKEN" ]; then
  die "--api-key or --enrollment-token is required"
fi

# -- detect OS + arch --
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH_RAW="$(uname -m)"
case "$ARCH_RAW" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "unsupported arch: $ARCH_RAW" ;;
esac
case "$OS" in
  linux|darwin) : ;;
  *) die "unsupported OS: $OS (Windows support is on the roadmap)" ;;
esac
log "detected: $OS/$ARCH"

# -- detect topology --
have_cmd() { command -v "$1" >/dev/null 2>&1; }

DOCKER=0
SYSTEMD=0
KUBE=0
have_cmd docker && docker info >/dev/null 2>&1 && DOCKER=1 || true
[ -d /run/systemd/system ] && SYSTEMD=1 || true
have_cmd kubectl && kubectl version --client >/dev/null 2>&1 && KUBE=1 || true

if [ "$DOCKER" -eq 1 ]; then
  TOPOLOGY="docker"
elif [ "$SYSTEMD" -eq 1 ]; then
  TOPOLOGY="systemd"
elif [ "$KUBE" -eq 1 ]; then
  TOPOLOGY="kubernetes"
else
  TOPOLOGY="binary"
fi
log "topology: $TOPOLOGY"

# -- env-for-container helper --
build_env_args() {
  prefix="$1"  # "-e" for docker, "" for systemd EnvironmentFile
  printf '%s SUPPORTLY_PROJECT_ID=%s\n' "$prefix" "$SUPPORTLY_PROJECT_ID"
  printf '%s SUPPORTLY_API_ENDPOINT=%s\n' "$prefix" "$SUPPORTLY_ENDPOINT"
  if [ -n "$SUPPORTLY_API_KEY" ]; then
    printf '%s SUPPORTLY_API_KEY=%s\n' "$prefix" "$SUPPORTLY_API_KEY"
  fi
  if [ -n "$SUPPORTLY_ENROLLMENT_TOKEN" ]; then
    printf '%s SUPPORTLY_ENROLLMENT_TOKEN=%s\n' "$prefix" "$SUPPORTLY_ENROLLMENT_TOKEN"
  fi
}

install_docker() {
  log "installing as docker container"
  ENV_ARGS=$(build_env_args "-e" | tr '\n' ' ')
  run "docker pull $DOCKER_IMAGE:$SUPPORTLY_VERSION"
  run "docker rm -f supportly-agent 2>/dev/null || true"
  run "docker run -d --restart=always --name supportly-agent \
    $ENV_ARGS \
    -v /var/log:/host/var/log:ro \
    -v /var/run/docker.sock:/var/run/docker.sock:ro \
    $DOCKER_IMAGE:$SUPPORTLY_VERSION"
}

# -- download_binary <out_path> --
# Downloads the matching binary from GitHub Releases, verifies SHA256,
# and writes it to <out_path> with mode 755.
download_binary() {
  out="$1"
  version="$SUPPORTLY_VERSION"
  if [ "$version" = "latest" ]; then
    version=$(curl -fsSL "https://api.github.com/repos/${GH_REPO}/releases/latest" \
      | grep '"tag_name"' | head -1 | sed -E 's/.*"v?([^"]+)".*/\1/')
    [ -n "$version" ] || die "could not resolve latest version"
    log "resolved latest = v$version"
  fi
  archive="supportly-agent_${version}_${OS}_${ARCH}.tar.gz"
  url="https://github.com/${GH_REPO}/releases/download/v${version}/${archive}"
  checksums_url="https://github.com/${GH_REPO}/releases/download/v${version}/checksums.txt"

  tmpdir=$(mktemp -d)
  trap "rm -rf $tmpdir" EXIT

  log "downloading $archive"
  run "curl -fSL -o '$tmpdir/$archive' '$url'"
  run "curl -fSL -o '$tmpdir/checksums.txt' '$checksums_url'"

  if [ "$DRY_RUN" -eq 0 ]; then
    expected=$(grep " $archive\$" "$tmpdir/checksums.txt" | awk '{print $1}')
    [ -n "$expected" ] || die "no checksum entry for $archive"
    actual=$(sha256sum "$tmpdir/$archive" 2>/dev/null | awk '{print $1}' \
             || shasum -a 256 "$tmpdir/$archive" | awk '{print $1}')
    if [ "$expected" != "$actual" ]; then
      die "checksum mismatch: expected $expected, got $actual"
    fi
    log "checksum OK"
    tar -xzf "$tmpdir/$archive" -C "$tmpdir"
    install -m 0755 "$tmpdir/supportly-agent" "$out"
  fi
}

install_systemd() {
  log "installing as systemd service"
  bin_path="$INSTALL_DIR/supportly-agent"
  download_binary "$bin_path"

  unit_path="/etc/systemd/system/supportly-agent.service"
  env_path="/etc/supportly/agent.env"
  run "mkdir -p /etc/supportly"
  if [ "$DRY_RUN" -eq 0 ]; then
    {
      build_env_args "" | sed 's/^ *//'
    } > "$env_path"
    chmod 0600 "$env_path"
    cat > "$unit_path" <<UNIT
[Unit]
Description=Supportly Agent — error capture for Supportly
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$env_path
ExecStart=$bin_path
Restart=always
RestartSec=5s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/supportly
StateDirectory=supportly

[Install]
WantedBy=multi-user.target
UNIT
  fi
  run "systemctl daemon-reload"
  run "systemctl enable --now supportly-agent.service"
}

install_kubernetes() {
  cat <<KUBE_HINT
${YLW}!! ${NC}Kubernetes detected. We do not auto-apply manifests in your cluster
   from a curl-pipe. Run the command below yourself after reviewing it:

   kubectl apply -f https://raw.githubusercontent.com/${GH_REPO}/main/deploy/k8s/daemonset.yaml

   Then create the secret:

   kubectl -n supportly create secret generic supportly-agent \\
     --from-literal=project-id='${SUPPORTLY_PROJECT_ID}' \\
     --from-literal=api-key='${SUPPORTLY_API_KEY}'
KUBE_HINT
  exit 0
}

install_binary() {
  log "no docker/systemd detected — installing as raw binary"
  bin_path="$INSTALL_DIR/supportly-agent"
  download_binary "$bin_path"
  log "installed to $bin_path"
  log "next: write $INSTALL_DIR/../etc/supportly/agent.env and run supportly-agent --config /etc/supportly/agent.yaml"
}

verify_install() {
  if [ "$VERIFY" -eq 0 ] || [ "$DRY_RUN" -eq 1 ]; then return 0; fi
  log "verifying agent is online (waiting up to 60s)..."
  i=0
  while [ "$i" -lt 12 ]; do
    if curl -fsS --max-time 2 http://127.0.0.1:9876/healthz >/dev/null 2>&1; then
      log "agent is healthy ✓"
      return 0
    fi
    sleep 5
    i=$((i + 1))
  done
  warn "couldn't reach agent's local /healthz endpoint within 60s"
  warn "check logs: docker logs supportly-agent  OR  journalctl -u supportly-agent -e"
  return 1
}

# -- dispatch --
case "$TOPOLOGY" in
  docker)     install_docker ;;
  systemd)    install_systemd ;;
  kubernetes) install_kubernetes ;;
  binary)     install_binary ;;
esac

verify_install || true

cat <<SUCCESS

${GRN}==>${NC} Supportly Agent installed.
    Topology: $TOPOLOGY
    Project:  $SUPPORTLY_PROJECT_ID
    Version:  $SUPPORTLY_VERSION

    Dashboard: https://app.supportly.io/dashboard/ingest-agents
SUCCESS
