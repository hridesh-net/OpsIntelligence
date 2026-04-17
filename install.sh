#!/usr/bin/env bash
# OpsIntelligence installer — installs the Go binary, optional Python venv,
# and creates the ~/.opsintelligence config + datastore directories. No
# Docker required. Works for both local installs (loopback bind, SQLite
# datastore) and cloud installs (lan/0.0.0.0 bind, Postgres datastore).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/hridesh-net/OpsIntelligence/main/install.sh | bash
#   bash install.sh
#   OPSINTELLIGENCE_VERSION=v0.1.0 bash install.sh
#
# Environment:
#   OPSINTELLIGENCE_VERSION   Git tag or "latest" (default: latest)
#   INSTALL_DIR          Binary destination (default: /usr/local/bin or ~/.local/bin if not writable)
#   STATE_DIR            Config/state/datastore root (default: ~/.opsintelligence)
#   FORCE_BUILD=1        Build from source instead of downloading release
#   OPSINTELLIGENCE_INSTALL_GO_TAGS  Go build tags for source build (default: fts5). CI sets fts5,opsintelligence_localgemma
#   SKIP_VENV=1          Skip Python venv creation
#   SKIP_NODE=1          Skip Node/pnpm/TypeScript install (CI fresh-install smoke)
#   SKIP_SENSING=1       Skip optional C++ sensing build
#   SKIP_SERVICE=1       Skip launchd/systemd login service registration (macOS/Linux)
#   WITH_MEMPALACE=1     After install, run managed MemPalace bootstrap (venv + pip + mempalace init)
#                        Requires system Python 3 with venv; uses OPSINTELLIGENCE_MEMPALACE_BOOTSTRAP_PYTHON if set
#   WITH_GEMMA=1         Download Gemma-compatible GGUF and print local_intel snippet via
#                        'opsintelligence local-intel setup --state-dir'. Override source via
#                        OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_URL (and optional OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_SHA256).
#                        Optional auth token: OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_TOKEN.
#
# After install:
#   1. Open the dashboard at http://127.0.0.1:18790/dashboard/ (default port).
#      The first visit prompts you to create the initial owner account
#      (datastore-backed, RBAC-protected). Same first-run flow happens
#      whether you run locally or on a cloud server — just point at the
#      right host:port.
#   2. Or use the CLI: `opsintelligence onboard` for the interactive
#      provider/team setup, then `opsintelligence start` to run the
#      gateway + workers as a daemon.

set -eo pipefail

# ─────────────────────────────────────────────
# Config
# ─────────────────────────────────────────────
OPSINTELLIGENCE_VERSION="${OPSINTELLIGENCE_VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-}"
STATE_DIR="${STATE_DIR:-$HOME/.opsintelligence}"
VENV_DIR="$STATE_DIR/venv"
REPO_OWNER_REPO="${OPSINTELLIGENCE_REPO:-hridesh-net/OpsIntelligence}"
if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
  REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
  REPO_ROOT="$PWD"
fi

# Prefer XDG-style user bin when /usr/local is not writable
default_install_dir() {
  if [[ -n "${INSTALL_DIR:-}" ]]; then
    echo "$INSTALL_DIR"
    return
  fi
  local d="/usr/local/bin"
  if [[ -w "$d" ]] || [[ -w "$(dirname "$d")" ]]; then
    echo "$d"
  else
    echo "${XDG_BIN_HOME:-$HOME/.local/bin}"
  fi
}

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${BLUE}[opsintelligence]${NC} $*"; }
ok()   { echo -e "${GREEN}[✓]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[✗]${NC} $*" >&2; exit 1; }

# ─────────────────────────────────────────────
# OS / arch detection
# ─────────────────────────────────────────────
detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in
    x86_64)  arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    armv7*)  err "No pre-built ARMv7 release; install on arm64/amd64 or set FORCE_BUILD=1 with Go 1.24+." ;;
    *)       err "Unsupported architecture: $arch" ;;
  esac
  case "$os" in
    linux|darwin) ;;
    msys*|cygwin*|mingw*) os="windows" ;;
    *) err "Unsupported OS: $os" ;;
  esac
  echo "${os}-${arch}"
}

PLATFORM="$(detect_platform)"
log "Detected platform: $PLATFORM"

# ─────────────────────────────────────────────
# Dependency checks
# ─────────────────────────────────────────────
need_git() {
  [[ "${FORCE_BUILD:-0}" == "1" ]] || [[ -f "$REPO_ROOT/cmd/opsintelligence/main.go" ]]
}

check_deps() {
  command -v curl >/dev/null 2>&1 || err "curl is required. Install curl and retry."
  if need_git; then
    command -v git >/dev/null 2>&1 || err "git is required for this install mode (source/build). Install git or use default release download."
  fi
}

curl_get() {
  local out="$1"
  local url="$2"
  curl -fsSL \
    --connect-timeout 25 \
    --retry 3 \
    --retry-delay 2 \
    --retry-all-errors \
    -o "$out" "$url"
}

# ─────────────────────────────────────────────
# Go binary: download pre-built or fallback to source
# ─────────────────────────────────────────────
install_binary() {
  INSTALL_DIR="$(default_install_dir)"
  export INSTALL_DIR

  if [[ "${FORCE_BUILD:-0}" == "1" ]]; then
    log "FORCE_BUILD=1: Building OpsIntelligence binary from source..."
    build_binary_from_source
    return
  fi

  local artifact="opsintelligence-${PLATFORM}"
  if [[ "$PLATFORM" == windows-* ]]; then
    artifact="${artifact}.exe"
  fi

  local release_path="download/${OPSINTELLIGENCE_VERSION}"
  if [[ "${OPSINTELLIGENCE_VERSION}" == "latest" ]]; then
    release_path="latest/download"
  fi
  local download_url="https://github.com/${REPO_OWNER_REPO}/releases/${release_path}/${artifact}"
  local tmp_bin
  tmp_bin="$(mktemp "${TMPDIR:-/tmp}/opsintelligence.XXXXXX")"

  log "Downloading pre-built binary for ${PLATFORM}..."
  mkdir -p "$INSTALL_DIR"

  if curl_get "$tmp_bin" "$download_url"; then
    chmod +x "$tmp_bin"
    mv -f "$tmp_bin" "$INSTALL_DIR/opsintelligence"
    ok "Binary installed: $INSTALL_DIR/opsintelligence"
    copy_skills_dir
  else
    rm -f "$tmp_bin"
    err "Failed to download pre-built binary from $download_url. This installer is binary-first; retry later, pin OPSINTELLIGENCE_VERSION to a release with assets, or set FORCE_BUILD=1 if you explicitly want a source build."
  fi
}

build_binary_from_source() {
  command -v go >/dev/null 2>&1 || err "Go 1.24+ not found. Install Go or use a release download (unset FORCE_BUILD)."
  local build_root="$REPO_ROOT"
  local tmp_src=""
  if [[ ! -f "$build_root/cmd/opsintelligence/main.go" ]]; then
    command -v git >/dev/null 2>&1 || err "git is required for source fallback when installer is run via curl|bash"
    tmp_src="$(mktemp -d "${TMPDIR:-/tmp}/opsintelligence-src.XXXXXX")"
    local ref="${OPSINTELLIGENCE_VERSION}"
    if [[ -z "$ref" || "$ref" == "latest" ]]; then
      ref="main"
    fi
    log "Source fallback: cloning ${REPO_OWNER_REPO}@${ref}..."
    if ! git clone --depth 1 --branch "$ref" "https://github.com/${REPO_OWNER_REPO}.git" "$tmp_src" >/dev/null 2>&1; then
      warn "Failed to clone ref $ref, falling back to main"
      git clone --depth 1 --branch "main" "https://github.com/${REPO_OWNER_REPO}.git" "$tmp_src" >/dev/null 2>&1 || err "unable to clone source repo for fallback build"
    fi
    build_root="$tmp_src"
  fi
  local go_version
  go_version="$(go version | awk '{print $3}' | tr -d 'go')"
  log "Building with go $go_version..."
  INSTALL_DIR="$(default_install_dir)"
  export INSTALL_DIR
  mkdir -p "$INSTALL_DIR"

  local ver_ldflags=""
  if [[ "${OPSINTELLIGENCE_VERSION}" != "latest" && -n "${OPSINTELLIGENCE_VERSION}" ]]; then
    ver_ldflags="-X main.version=${OPSINTELLIGENCE_VERSION}"
  fi
  local tmp_build
  tmp_build="$(mktemp "${TMPDIR:-/tmp}/opsintelligence-build.XXXXXX")"
  local go_tags="${OPSINTELLIGENCE_INSTALL_GO_TAGS:-fts5}"
  (cd "$build_root" && CGO_ENABLED="${CGO_ENABLED:-1}" go build -mod=vendor -tags "$go_tags" -ldflags "-s -w ${ver_ldflags}" -o "$tmp_build" ./cmd/opsintelligence)
  install -m 0755 "$tmp_build" "$INSTALL_DIR/opsintelligence"
  rm -f "$tmp_build"
  if [[ -n "$tmp_src" ]]; then
    rm -rf "$tmp_src"
  fi
  ok "Binary compiled and installed: $INSTALL_DIR/opsintelligence"
  copy_skills_dir
}

# ─────────────────────────────────────────────
# Copy bundled skills next to binary (local repo only)
# ─────────────────────────────────────────────
copy_skills_dir() {
  local skills_src="$REPO_ROOT/skills"
  local skills_dest="$INSTALL_DIR/skills"

  if [[ ! -d "$skills_src" ]]; then
    warn "No local skills/ directory (normal for curl|bash install). Install skills with: opsintelligence skills marketplace"
    return
  fi

  log "Copying bundled skills to $skills_dest..."
  rm -rf "$skills_dest"
  cp -R "$skills_src" "$skills_dest"
  ok "Bundled skills installed: $skills_dest"
}

# ─────────────────────────────────────────────
# Node.js + pnpm (optional TS layer)
# ─────────────────────────────────────────────
setup_node() {
  [[ "${SKIP_NODE:-0}" == "1" ]] && {
    log "SKIP_NODE=1 — skipping Node.js / pnpm / TypeScript install."
    return 0
  }

  if [[ ! -f "$REPO_ROOT/package.json" ]]; then
    return
  fi

  if command -v node >/dev/null 2>&1; then
    local node_ver
    node_ver="$(node --version | tr -d 'v')"
    local node_major
    IFS='.' read -r node_major _ <<< "$node_ver"
    if [[ "$node_major" -ge 22 ]]; then
      ok "Node.js $node_ver found"
      if ! command -v pnpm >/dev/null 2>&1; then
        log "Installing pnpm..."
        npm install -g pnpm@10
      fi
      log "Installing Node dependencies..."
      (cd "$REPO_ROOT" && pnpm install --frozen-lockfile 2>/dev/null || pnpm install)
      log "Building TypeScript layer..."
      (cd "$REPO_ROOT" && pnpm build) && ok "TypeScript layer built"
    else
      warn "Node.js $node_ver < 22; TypeScript layer will not be built"
    fi
  else
    warn "Node.js not found; TypeScript layer will not be built (Go binary is fully functional)"
  fi
}

# ─────────────────────────────────────────────
# Python venv for tool sandbox
# ─────────────────────────────────────────────
setup_venv() {
  [[ "${SKIP_VENV:-0}" != "1" ]] || return 0

  if [[ -x "$VENV_DIR/bin/python" ]]; then
    ok "Python venv already exists: $VENV_DIR (skipping recreate)"
    "$VENV_DIR/bin/pip" install --quiet --upgrade pip 2>/dev/null || true
    return 0
  fi

  local python_bin=""
  for candidate in python3.12 python3.11 python3.10 python3; do
    if command -v "$candidate" >/dev/null 2>&1; then
      python_bin="$candidate"
      break
    fi
  done

  if [[ -z "$python_bin" ]]; then
    warn "Python 3 not found — auto-tool sandbox will use system Python if available"
    return
  fi

  local py_ver
  py_ver="$("$python_bin" --version 2>&1 | awk '{print $2}')"
  log "Creating Python venv at $VENV_DIR (Python $py_ver)..."
  "$python_bin" -m venv "$VENV_DIR"
  "$VENV_DIR/bin/pip" install --quiet --upgrade pip
  ok "Python venv ready: $VENV_DIR"
}

# ─────────────────────────────────────────────
# C++ sensing (optional)
# ─────────────────────────────────────────────
build_sensing() {
  [[ "${SKIP_SENSING:-0}" != "1" ]] || return 0
  if [[ ! -d "$REPO_ROOT/sensing" ]]; then
    return
  fi
  if ! command -v cmake >/dev/null 2>&1; then
    warn "cmake not found — C++ sensing layer will not be built"
    return
  fi

  local jobs
  jobs="$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 2)"

  log "Building C++ sensing layer (${jobs} parallel jobs)..."
  cmake -S "$REPO_ROOT/sensing" -B "$REPO_ROOT/sensing/build" \
    -DCMAKE_BUILD_TYPE=Release -DCMAKE_EXPORT_COMPILE_COMMANDS=ON >/dev/null
  cmake --build "$REPO_ROOT/sensing/build" --parallel "$jobs" >/dev/null
  ok "C++ sensing layer built"
}

# ─────────────────────────────────────────────
# Config + datastore directory scaffold
# ─────────────────────────────────────────────
# The Go binary creates ops.db (the RBAC/audit/sessions datastore)
# itself on first start, but we pre-create the parent dirs so a
# headless cloud install never races on permissions. Agent memory and
# the ops-plane datastore stay in separate sub-trees by design.
setup_config() {
  log "Setting up config + datastore directory: $STATE_DIR"
  mkdir -p "$STATE_DIR"/{memory,tools,logs,security,channels}
  mkdir -p "$STATE_DIR"/skills/{bundled,custom}
  mkdir -p "$STATE_DIR"/workspace/public
  # Datastore lives at $STATE_DIR/ops.db (sqlite default). Postgres
  # users override datastore.dsn in opsintelligence.yaml; this dir is
  # still needed for migration scratch space + audit log fallback.
  mkdir -p "$STATE_DIR"/datastore
}

# MemPalace (Python) — not part of the Go binary; optional one-shot bootstrap via CLI.
setup_mempalace() {
  [[ "${WITH_MEMPALACE:-0}" == "1" ]] || return 0

  local bin="$INSTALL_DIR/opsintelligence"
  if [[ ! -x "$bin" ]]; then
    warn "WITH_MEMPALACE=1 but binary not executable at $bin — skipping MemPalace"
    return 0
  fi

  log "WITH_MEMPALACE=1: bootstrapping MemPalace under $STATE_DIR/mempalace/ (may download PyPI packages)..."
  if "$bin" --log-level info mempalace setup --state-dir "$STATE_DIR"; then
    ok "MemPalace managed venv ready (enable in opsintelligence.yaml: memory.mempalace.managed_venv + auto_start + enabled)"
  else
    warn "MemPalace bootstrap failed — install continues. Retry: WITH_MEMPALACE=1 bash install.sh or $bin mempalace setup --state-dir \"$STATE_DIR\""
  fi
}

# Local Intel (Gemma GGUF) — optional one-shot model bootstrap via CLI.
setup_local_intel() {
  [[ "${WITH_GEMMA:-0}" == "1" ]] || return 0

  local bin="$INSTALL_DIR/opsintelligence"
  if [[ ! -x "$bin" ]]; then
    warn "WITH_GEMMA=1 but binary not executable at $bin — skipping local-intel setup"
    return 0
  fi

  log "WITH_GEMMA=1: bootstrapping Gemma GGUF under $STATE_DIR/models/ (download may be large)..."
  local args=(--log-level info local-intel setup --state-dir "$STATE_DIR")
  if [[ -n "${OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_URL:-}" ]]; then
    args+=(--url "$OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_URL")
  fi
  if [[ -n "${OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_SHA256:-}" ]]; then
    args+=(--sha256 "$OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_SHA256")
  fi
  if "$bin" "${args[@]}"; then
    ok "Local-intel GGUF prepared (merge printed snippet into opsintelligence.yaml)"
  else
    warn "Local-intel bootstrap failed — install continues. Retry: WITH_GEMMA=1 bash install.sh or $bin local-intel setup --state-dir \"$STATE_DIR\""
  fi
}

# ─────────────────────────────────────────────
# PATH hint
# ─────────────────────────────────────────────
path_hint() {
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) return 0 ;;
  esac
  echo ""
  warn "$INSTALL_DIR is not on your PATH."
  case "${SHELL:-}" in
    */zsh)
      echo -e "  Add to ${BOLD}~/.zshrc${NC}:"
      echo -e "    ${BOLD}export PATH=\"$INSTALL_DIR:\$PATH\"${NC}"
      ;;
    */bash|*)
      echo -e "  Add to ${BOLD}~/.bashrc${NC} or ${BOLD}~/.profile${NC}:"
      echo -e "    ${BOLD}export PATH=\"$INSTALL_DIR:\$PATH\"${NC}"
      ;;
  esac
}

# ─────────────────────────────────────────────
# Verify installation
# ─────────────────────────────────────────────
verify() {
  local bin="$INSTALL_DIR/opsintelligence"
  if [[ ! -x "$bin" ]]; then
    warn "Expected binary missing or not executable: $bin"
    return
  fi
  if ver_out="$("$bin" version 2>&1)"; then
    ok "Installation verified: $ver_out"
  else
    warn "Binary present but 'opsintelligence version' failed — try running: $bin version"
  fi

  if command -v opsintelligence >/dev/null 2>&1; then
    local resolved_bin
    resolved_bin="$(command -v opsintelligence)"
    if [[ "$resolved_bin" != "$bin" ]]; then
      echo ""
      warn "Another opsintelligence on PATH shadows this install: $resolved_bin"
      warn "Remove the old binary or reorder PATH so $INSTALL_DIR comes first."
      echo ""
    fi
  else
    path_hint
  fi
}

# Register launchd (macOS) or systemd user unit (Linux) so OpsIntelligence starts after login.
install_login_service() {
  [[ "${SKIP_SERVICE:-0}" == "1" ]] && {
    log "SKIP_SERVICE=1 — skipping login service registration."
    return 0
  }

  local bin="$INSTALL_DIR/opsintelligence"
  [[ -x "$bin" ]] || bin="$INSTALL_DIR/opsintelligence.exe"
  [[ -x "$bin" ]] || return 0

  case "$PLATFORM" in
    darwin-*|linux-*)
      log "Registering OpsIntelligence login service (auto-start after sign-in)…"
      export OPSINTELLIGENCE_STATE_DIR="$STATE_DIR"
      if "$bin" service install; then
        ok "Login service installed. (Set SKIP_SERVICE=1 to skip on future runs.)"
        if [[ "$PLATFORM" == linux-* ]]; then
          echo ""
          echo -e "  ${YELLOW}Headless Linux?${NC} User services may need: ${BOLD}sudo loginctl enable-linger \"\$USER\"${NC}"
        fi
      else
        warn "Login service registration failed — run manually after fixing PATH or systemd:"
        echo -e "    ${BOLD}$bin service install${NC}"
      fi
      ;;
    windows-*)
      log "Windows: use Task Scheduler for login start:"
      "$bin" service install 2>/dev/null || true
      ;;
    *)
      warn "Unknown platform for auto service: $PLATFORM"
      ;;
  esac
}

# ─────────────────────────────────────────────
# Main
# ─────────────────────────────────────────────
usage() {
  sed -n '2,40p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit 0
}

main() {
  for arg in "$@"; do
    case "$arg" in
      -h|--help) usage ;;
    esac
  done

  echo ""
  echo -e "${BOLD}  ╔═══════════════════════════════════╗${NC}"
  echo -e "${BOLD}  ║     OpsIntelligence Installer     ║${NC}"
  echo -e "${BOLD}  ╚═══════════════════════════════════╝${NC}"
  echo ""

  check_deps
  install_binary
  setup_node
  setup_venv
  build_sensing
  setup_config
  verify
  setup_mempalace
  setup_local_intel
  install_login_service

  echo ""
  echo -e "${GREEN}${BOLD}OpsIntelligence installed successfully!${NC}"
  echo ""
  echo -e "  Get started:"
  echo -e "    ${BOLD}opsintelligence --help${NC}"
  echo -e "    ${BOLD}opsintelligence onboard${NC}               (first-time CLI setup)"
  echo -e "    ${BOLD}opsintelligence start${NC}                 (run gateway + workers)"
  echo -e "    ${BOLD}opsintelligence skills marketplace${NC}    (browse skills)"
  echo -e "    ${BOLD}opsintelligence skills add github${NC}     (install a skill)"
  echo -e "    ${BOLD}opsintelligence agent${NC}                 (interactive REPL)"
  echo ""
  echo -e "  Dashboard: ${BOLD}http://127.0.0.1:18790/dashboard/${NC}"
  echo -e "             First visit prompts you to create the initial"
  echo -e "             ${BOLD}owner${NC} account (datastore-backed, RBAC-protected)."
  echo -e "             For cloud installs, set ${BOLD}gateway.bind: lan${NC} +"
  echo -e "             ${BOLD}gateway.tls.cert/key${NC} and use the public hostname."
  echo ""
  echo -e "  Config:    ${BOLD}$STATE_DIR/opsintelligence.yaml${NC}"
  echo -e "  Datastore: ${BOLD}$STATE_DIR/ops.db${NC} (sqlite, default)"
  echo -e "  Binary:    ${BOLD}$INSTALL_DIR/opsintelligence${NC}"
  if [[ "${WITH_MEMPALACE:-0}" == "1" ]]; then
    echo -e "  MemPalace: ${BOLD}bootstrapped under $STATE_DIR/mempalace/${NC} (merge printed YAML into opsintelligence.yaml)"
  else
    echo -e "  MemPalace: ${BOLD}optional${NC} — ${BOLD}WITH_MEMPALACE=1 bash install.sh${NC} or ${BOLD}opsintelligence mempalace setup --state-dir \"$STATE_DIR\"${NC}"
  fi
  if [[ "${WITH_GEMMA:-0}" == "1" ]]; then
    echo -e "  Local Intel: ${BOLD}GGUF prepared under $STATE_DIR/models/${NC} (merge printed local_intel YAML into opsintelligence.yaml)"
  else
    echo -e "  Local Intel: ${BOLD}optional${NC} — ${BOLD}WITH_GEMMA=1 bash install.sh${NC} or ${BOLD}opsintelligence local-intel setup --state-dir \"$STATE_DIR\"${NC}"
  fi
  if [[ "${SKIP_SERVICE:-0}" != "1" ]] && [[ "$PLATFORM" == darwin-* || "$PLATFORM" == linux-* ]]; then
    echo -e "  Login service: ${BOLD}installed${NC} (auto-start after sign-in; ${BOLD}SKIP_SERVICE=1${NC} to skip next time)"
  fi
  echo ""
}

main "$@"
