#!/usr/bin/env bash
# OpsIntelligence uninstaller
# Removes the binary, system service (launchd/systemd), Plano Docker container,
# and optionally all user data (config, memory, skills, cron, security logs).
#
# Usage:
#   bash uninstall.sh              # removes binary & service, keeps ~/.opsintelligence data
#   bash uninstall.sh --purge      # removes everything including config and memory
#   bash uninstall.sh --keep-data  # same as default (explicit alias)
#   bash uninstall.sh -y --purge   # non-interactive purge
#   bash uninstall.sh --keep-mempalace  # keep $STATE_DIR/mempalace (managed Python venv + world)

set -eo pipefail

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
STATE_DIR="${STATE_DIR:-$HOME/.opsintelligence}"
PURGE=false
AUTO_CONFIRM=false
KEEP_MEMPALACE=false

# ─────────────────────────────────────────────
# Colors
# ─────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${CYAN}[opsintelligence]${NC} $*"; }
ok()   { echo -e "${GREEN}  ✓${NC} $*"; }
warn() { echo -e "${YELLOW}  !${NC} $*"; }
err()  { echo -e "${RED}  ✗${NC} $*" >&2; exit 1; }
step() { echo -e "\n${BOLD}$*${NC}"; }

for arg in "$@"; do
  case "$arg" in
    --purge)     PURGE=true ;;
    --keep-data) PURGE=false ;;
    -y|--yes)    AUTO_CONFIRM=true ;;
    --keep-mempalace) KEEP_MEMPALACE=true ;;
    --help|-h)
      echo ""
      echo "Usage: bash uninstall.sh [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  --purge      Also remove ~/.opsintelligence (config, memory, skills, cron_jobs.json, security logs)"
      echo "  --keep-data  Keep user data (default behaviour)"
      echo "  --keep-mempalace  Keep $STATE_DIR/mempalace (managed MemPalace venv + world)"
      echo "  -y, --yes    Auto-confirm deletion without prompting"
      echo "  --help       Show this help"
      echo ""
      exit 0
      ;;
  esac
done

# ─────────────────────────────────────────────
# Header
# ─────────────────────────────────────────────
echo ""
echo -e "${BOLD}${CYAN}"
echo "  ╭─────────────────────────────────────╮"
echo "  │   OpsIntelligence Uninstaller            │"
echo "  ╰─────────────────────────────────────╯"
echo -e "${NC}"
echo ""

if $PURGE; then
  warn "PURGE mode — state under $STATE_DIR and caches will be deleted"
  echo ""
fi

# ─────────────────────────────────────────────
# Stop process via PID file (preferred)
# ─────────────────────────────────────────────
stop_via_pidfile() {
  local pf="$STATE_DIR/opsintelligence.pid"
  [[ -f "$pf" ]] || return 0
  local pid
  pid="$(tr -d ' \t\r\n' <"$pf" 2>/dev/null || true)"
  if [[ -n "$pid" ]] && [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
    log "Stopping process from PID file (PID $pid)..."
    kill -TERM "$pid" 2>/dev/null || true
    local n=0
    while kill -0 "$pid" 2>/dev/null && [[ $n -lt 30 ]]; do
      sleep 0.2
      n=$((n + 1))
    done
    if kill -0 "$pid" 2>/dev/null; then
      kill -KILL "$pid" 2>/dev/null || true
    fi
    ok "Stopped opsintelligence (PID $pid)"
  fi
  rm -f "$pf"
}

# ─────────────────────────────────────────────
# Step 1: Stop running daemon & remove service
# ─────────────────────────────────────────────
stop_daemon() {
  step "1. Stopping OpsIntelligence daemon and removing system service..."

  stop_via_pidfile

  if command -v opsintelligence >/dev/null 2>&1; then
    opsintelligence service uninstall 2>/dev/null && ok "Service removed via 'opsintelligence service uninstall'" || true
    opsintelligence stop 2>/dev/null && ok "Daemon stopped via 'opsintelligence stop'" || true
  fi

  # ── macOS launchd ────────────────────────────────────────────────────────
  local plist="$HOME/Library/LaunchAgents/com.opsintelligence.agent.plist"
  local sys_plist="/Library/LaunchDaemons/com.opsintelligence.agent.plist"

  if [[ -f "$plist" ]]; then
    launchctl bootout "gui/$(id -u)/com.opsintelligence.agent" 2>/dev/null || \
      launchctl unload "$plist" 2>/dev/null || true
    rm -f "$plist"
    ok "Removed macOS launchd agent: $plist"
  fi

  if [[ -f "$sys_plist" ]]; then
    if command -v sudo >/dev/null 2>&1; then
      sudo launchctl bootout "system/com.opsintelligence.agent" 2>/dev/null || \
        sudo launchctl unload "$sys_plist" 2>/dev/null || true
      sudo rm -f "$sys_plist"
      ok "Removed macOS system launchd daemon: $sys_plist"
    else
      warn "Need sudo to remove $sys_plist"
    fi
  fi

  # ── Linux systemd (user) ─────────────────────────────────────────────────
  local user_service="$HOME/.config/systemd/user/opsintelligence.service"

  if command -v systemctl >/dev/null 2>&1; then
    if systemctl --user is-active --quiet opsintelligence 2>/dev/null; then
      systemctl --user stop opsintelligence 2>/dev/null && ok "Stopped systemd user service"
    fi
    if systemctl --user is-enabled --quiet opsintelligence 2>/dev/null; then
      systemctl --user disable opsintelligence 2>/dev/null && ok "Disabled systemd user service"
    fi
    if [[ -f "$user_service" ]]; then
      rm -f "$user_service"
      systemctl --user daemon-reload 2>/dev/null || true
      ok "Removed systemd user service file: $user_service"
    fi

    if systemctl is-active --quiet opsintelligence 2>/dev/null; then
      if command -v sudo >/dev/null 2>&1; then
        sudo systemctl stop opsintelligence 2>/dev/null
        sudo systemctl disable opsintelligence 2>/dev/null
        ok "Stopped system-level systemd service"
      fi
    fi
    local sys_service="/etc/systemd/system/opsintelligence.service"
    if [[ -f "$sys_service" ]]; then
      if command -v sudo >/dev/null 2>&1; then
        sudo rm -f "$sys_service"
        sudo systemctl daemon-reload 2>/dev/null || true
        ok "Removed system-level systemd service file"
      fi
    fi
  fi

  # Exact process name only (avoids killing unrelated processes matching "opsintelligence" in argv)
  if command -v pgrep >/dev/null 2>&1; then
    local p
    p="$(pgrep -nx opsintelligence 2>/dev/null || true)"
    if [[ -n "$p" ]]; then
      kill -TERM $p 2>/dev/null || true
      sleep 0.5
      if kill -0 $p 2>/dev/null; then
        kill -KILL $p 2>/dev/null || true
      fi
      ok "Stopped remaining opsintelligence process(es): $p"
    fi
  fi
}

# ─────────────────────────────────────────────
# Step 2: Stop & remove Plano Docker container
# ─────────────────────────────────────────────
remove_plano() {
  step "2. Removing Plano smart routing proxy (if running)..."

  if ! command -v docker >/dev/null 2>&1; then
    warn "Docker not installed — skipping Plano cleanup"
    return
  fi

  if docker ps -a --format '{{.Names}}' 2>/dev/null | grep -q '^plano$'; then
    docker stop plano 2>/dev/null && ok "Stopped Plano container"
    docker rm   plano 2>/dev/null && ok "Removed Plano container"
  else
    warn "No Plano container found (may already be removed)"
  fi

  if $PURGE; then
    if docker images katanemo/plano --format '{{.ID}}' 2>/dev/null | grep -q .; then
      docker rmi katanemo/plano 2>/dev/null && ok "Removed Plano Docker image (katanemo/plano)"
    fi
  else
    warn "Plano Docker image kept (run with --purge to also remove it)"
  fi
}

# ─────────────────────────────────────────────
# Step 3: Remove binary and bundled skills
# ─────────────────────────────────────────────
remove_binary() {
  step "3. Removing OpsIntelligence binary..."

  local candidates=(
    "$INSTALL_DIR/opsintelligence"
    "$HOME/.local/bin/opsintelligence"
    "/usr/bin/opsintelligence"
    "/usr/local/bin/opsintelligence"
    "/opt/opsintelligence/opsintelligence"
  )

  local found=false
  for bin in "${candidates[@]}"; do
    if [[ -f "$bin" ]]; then
      local dir
      dir="$(dirname "$bin")"
      if [[ -w "$dir" && -w "$bin" ]]; then
        rm -f "$bin"
        ok "Removed binary: $bin"
        if [[ -d "$dir/skills" ]]; then
          rm -rf "$dir/skills"
          ok "Removed bundled skills: $dir/skills"
        fi
      else
        if command -v sudo >/dev/null 2>&1; then
          sudo rm -f "$bin"
          ok "Removed binary (via sudo): $bin"
          if [[ -d "$dir/skills" ]]; then
            sudo rm -rf "$dir/skills"
            ok "Removed bundled skills (via sudo): $dir/skills"
          fi
        else
          warn "Cannot remove $bin — permission denied and sudo not available"
        fi
      fi
      found=true
    fi
  done

  if ! $found; then
    warn "Binary not found in standard locations (may already be removed)"
  fi

  rm -f "$STATE_DIR/opsintelligence.pid" 2>/dev/null || true
}

# ─────────────────────────────────────────────
# Step 4: Remove shell completions
# ─────────────────────────────────────────────
remove_completion() {
  step "4. Removing shell completions..."

  local completions=(
    "/usr/local/share/bash-completion/completions/opsintelligence"
    "/usr/share/bash-completion/completions/opsintelligence"
    "$HOME/.local/share/bash-completion/completions/opsintelligence"
    "$HOME/.zsh/completions/_opsintelligence"
    "$HOME/.config/fish/completions/opsintelligence.fish"
  )
  local removed=false
  for f in "${completions[@]}"; do
    if [[ -f "$f" ]]; then
      rm -f "$f" 2>/dev/null || sudo rm -f "$f" 2>/dev/null || true
      ok "Removed: $f"
      removed=true
    fi
  done
  if ! $removed; then
    warn "No shell completions found (skipped)"
  fi
}

# ─────────────────────────────────────────────
# Step 4b: Managed MemPalace (Python venv under state_dir — not the Go binary)
# ─────────────────────────────────────────────
remove_mempalace_managed() {
  if $KEEP_MEMPALACE; then
    return 0
  fi
  local mp="${STATE_DIR}/mempalace"
  if [[ -d "$mp" ]]; then
    rm -rf "$mp"
    ok "Removed managed MemPalace directory: $mp"
  fi
}

# ─────────────────────────────────────────────
# Step 5: Remove user data (--purge only)
# ─────────────────────────────────────────────
remove_user_data() {
  step "5. Removing user data..."

  if [[ ! -d "$STATE_DIR" ]]; then
    warn "State directory not found: $STATE_DIR"
    return
  fi

  echo ""
  echo -e "${RED}${BOLD}  ⚠ This will permanently delete:${NC}"
  echo -e "  ${RED}• $STATE_DIR/opsintelligence.yaml     (config)${NC}"
  echo -e "  ${RED}• $STATE_DIR/memory/             (memory)${NC}"
  echo -e "  ${RED}• $STATE_DIR/skills/             (skills)${NC}"
  echo -e "  ${RED}• $STATE_DIR/tools/              (generated tools)${NC}"
  echo -e "  ${RED}• $STATE_DIR/cron_jobs.json      (persistent cron jobs)${NC}"
  echo -e "  ${RED}• $STATE_DIR/security/           (audit logs)${NC}"
  echo     "  • $STATE_DIR/logs/"
  echo     "  • $HOME/.cache/opsintelligence/"
  echo ""

  if ! $AUTO_CONFIRM; then
    read -r -p "  Type 'yes' to confirm permanent deletion: " confirm
    if [[ "$confirm" != "yes" ]]; then
      warn "Skipped data deletion — your data is intact"
      return
    fi
  fi

  rm -rf "$STATE_DIR"
  rm -rf "$HOME/.cache/opsintelligence" 2>/dev/null || true
  ok "Removed user data: $STATE_DIR and caches"
}

# ─────────────────────────────────────────────
# Main
# ─────────────────────────────────────────────
stop_daemon
remove_plano
remove_binary
remove_completion

if $PURGE; then
  remove_user_data
else
  remove_mempalace_managed
  echo ""
  if $KEEP_MEMPALACE; then
    warn "Config and data at ${BOLD}$STATE_DIR${NC} were kept (including ${BOLD}mempalace/${NC})."
  else
    warn "Config and data at ${BOLD}$STATE_DIR${NC} were kept (managed ${BOLD}mempalace/${NC} removed unless you used ${BOLD}--keep-mempalace${NC})."
  fi
  warn "Remove everything:  ${BOLD}bash uninstall.sh --purge${NC}"
  warn "Remove Plano image: ${BOLD}docker rmi katanemo/plano${NC}"
fi

echo ""
echo -e "${GREEN}${BOLD}  ✓ OpsIntelligence uninstalled.${NC}"
echo ""
