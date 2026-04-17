#!/usr/bin/env bash
# apply-and-test.sh — check out a PR in a disposable worktree, run the
# repo's lint + test + build conventions, and clean up. Never touches
# the caller's current branch or working tree.
#
# Usage:
#   apply-and-test.sh <owner/repo> <pr>
#   apply-and-test.sh <pr-url>
#
# The script auto-detects common toolchains:
#   - Go     (go.mod)         go vet ./... && go test ./... -count=1
#   - Node   (package.json)   npm ci && npm test --silent && npm run lint --if-present
#   - Python (pyproject.toml) ruff check . && pytest -x -q
#   - Make   (Makefile)       make test  (if no language detected)
#
# Exit codes:
#   0 = all checks green
#   1 = checks failed (expected — use this to build the review)
#   2 = usage / environment error
set -uo pipefail

die() { echo "apply-and-test: $*" >&2; exit 2; }

require() { command -v "$1" >/dev/null 2>&1 || die "missing dependency: $1"; }
require git
require gh

REPO=""
PR=""
case "${1:-}" in
  https://github.com/*/pull/*)
    REPO=$(printf '%s' "$1" | sed -E 's#https://github.com/([^/]+/[^/]+)/pull/.*#\1#')
    PR=$(printf '%s' "$1" | sed -E 's#.*/pull/([0-9]+).*#\1#')
    ;;
  */*)
    REPO="$1"; PR="${2:-}"
    ;;
  *)
    die "usage: $0 <owner/repo> <pr>  OR  $0 <pr-url>"
    ;;
esac
[[ -n "$REPO" && -n "$PR" ]] || die "could not resolve repo/pr"

# The script may be invoked from anywhere; create a disposable clone if
# we are not already inside the target repo.
TMP=$(mktemp -d -t pr-$PR-XXXX)
cleanup() {
  local rc=$?
  if [[ -d "$TMP" ]]; then
    if [[ -n "${INSIDE_REPO:-}" ]]; then
      git worktree remove "$TMP" --force >/dev/null 2>&1 || true
    else
      rm -rf "$TMP"
    fi
  fi
  exit $rc
}
trap cleanup EXIT

INSIDE_REPO=""
if git -C "$PWD" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  ORIGIN_REPO=$(git -C "$PWD" remote get-url origin 2>/dev/null \
    | sed -E 's#.*github.com[:/]##; s#\.git$##' || true)
  if [[ "$ORIGIN_REPO" == "$REPO" ]]; then
    INSIDE_REPO=1
    git worktree add "$TMP" --detach >/dev/null
    cd "$TMP"
    gh pr checkout "$PR" --repo "$REPO"
  fi
fi

if [[ -z "$INSIDE_REPO" ]]; then
  # Shallow clone + PR fetch — cheap and safe from anywhere.
  cd "$TMP"
  git clone --depth 1 "https://github.com/$REPO.git" repo >/dev/null 2>&1
  cd repo
  git fetch origin "pull/$PR/head:pr-$PR" --depth 50 >/dev/null
  git switch "pr-$PR" >/dev/null
fi

echo "── pr-$PR at $(git rev-parse HEAD) ─────────────"
git log --oneline -5
echo "──────────────────────────────────────────────"

rc=0
run() {
  echo
  echo "$ $*"
  if ! "$@"; then rc=1; fi
}

if [[ -f go.mod ]]; then
  run go vet ./...
  run go test ./... -count=1
  [[ -f Makefile ]] && run make -n build >/dev/null && run make build
elif [[ -f package.json ]]; then
  [[ -f package-lock.json ]] && run npm ci --silent || run npm install --silent
  run npm run lint --if-present --silent
  run npm test --silent
elif [[ -f pyproject.toml || -f setup.py ]]; then
  if command -v ruff >/dev/null 2>&1; then run ruff check .; fi
  if command -v pytest >/dev/null 2>&1; then run pytest -x -q; fi
elif [[ -f Makefile ]]; then
  run make test
else
  echo "apply-and-test: no known build/test convention detected"
  echo "apply-and-test: add a project-specific Makefile target and re-run"
  rc=2
fi

echo
if [[ $rc -eq 0 ]]; then
  echo "✓ all checks green"
else
  echo "✗ checks FAILED — use this output as review evidence"
fi
exit $rc
