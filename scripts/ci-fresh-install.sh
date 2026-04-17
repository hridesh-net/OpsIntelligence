#!/usr/bin/env bash
# Fresh-install smoke: install.sh (source build) + doctor with minimal fixture.
# Used by GitHub Actions and locally: ./scripts/ci-fresh-install.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export FORCE_BUILD="${FORCE_BUILD:-1}"
export SKIP_VENV="${SKIP_VENV:-1}"
export SKIP_NODE="${SKIP_NODE:-1}"
export SKIP_SENSING="${SKIP_SENSING:-1}"
export SKIP_SERVICE="${SKIP_SERVICE:-1}"
export OPSINTELLIGENCE_INSTALL_GO_TAGS="${OPSINTELLIGENCE_INSTALL_GO_TAGS:-fts5,opsintelligence_localgemma}"

TMP_ROOT="${TMP_ROOT:-$(mktemp -d "${TMPDIR:-/tmp}/opsintelligence-fresh.XXXXXX")}"
export INSTALL_DIR="${INSTALL_DIR:-$TMP_ROOT/bin}"
export STATE_DIR="${STATE_DIR:-$TMP_ROOT/state}"
mkdir -p "$INSTALL_DIR" "$STATE_DIR"

bash install.sh

export PATH="$INSTALL_DIR:$PATH"
CFG="$ROOT/internal/config/testdata/doctor/valid_minimal.yaml"
opsintelligence doctor --config "$CFG" --skip-network --log-level error
