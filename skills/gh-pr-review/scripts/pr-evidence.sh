#!/usr/bin/env bash
# pr-evidence.sh — one-shot PR evidence dump for review prep.
#
# Prints a Markdown block with metadata, CI status, changed files, and
# the last failing run's summary. Purely read-only. Safe to run in any
# repo you have gh auth for.
#
# Usage:
#   pr-evidence.sh <owner/repo> <pr-number>
#   pr-evidence.sh https://github.com/acme/api/pull/123
#
# Requires: gh, jq.
set -euo pipefail

die() { echo "pr-evidence: $*" >&2; exit 2; }

require() { command -v "$1" >/dev/null 2>&1 || die "missing dependency: $1"; }
require gh
require jq

REPO=""
PR=""
case "${1:-}" in
  https://github.com/*/pull/*)
    REPO=$(printf '%s' "$1" | sed -E 's#https://github.com/([^/]+/[^/]+)/pull/.*#\1#')
    PR=$(printf '%s' "$1" | sed -E 's#.*/pull/([0-9]+).*#\1#')
    ;;
  */*)
    REPO="$1"
    PR="${2:-}"
    ;;
  *)
    die "usage: $0 <owner/repo> <pr>  OR  $0 <pr-url>"
    ;;
esac
[[ -n "$REPO" && -n "$PR" ]] || die "could not resolve repo/pr"

printf '## Evidence — %s#%s\n\n' "$REPO" "$PR"

gh pr view "$PR" --repo "$REPO" \
  --json number,title,author,baseRefName,headRefName,headRefOid,additions,deletions,changedFiles,mergeable,isDraft,labels,url \
  --jq '
    "**\(.title)** by @\(.author.login)\n" +
    "- URL: \(.url)\n" +
    "- Base: `\(.baseRefName)` → Head: `\(.headRefName)` @ `\(.headRefOid[0:7])`\n" +
    "- Size: +\(.additions) -\(.deletions) across \(.changedFiles) files\n" +
    "- State: mergeable=\(.mergeable) draft=\(.isDraft) labels=[\([.labels[].name] | join(", "))]"
  '

echo
echo "### CI checks"
gh pr checks "$PR" --repo "$REPO" || true

echo
echo "### Files changed"
gh api "repos/$REPO/pulls/$PR/files" --paginate \
  --jq '.[] | "- `\(.filename)` (+\(.additions)/-\(.deletions), \(.status))"'

echo
echo "### Last failing run (if any)"
BRANCH=$(gh pr view "$PR" --repo "$REPO" --json headRefName --jq .headRefName)
FAILING_ID=$(gh run list --repo "$REPO" --branch "$BRANCH" --limit 25 \
  --json databaseId,conclusion \
  --jq 'map(select(.conclusion=="failure")) | .[0].databaseId // empty')
if [[ -n "$FAILING_ID" ]]; then
  gh run view "$FAILING_ID" --repo "$REPO" \
    --json databaseId,name,conclusion,displayTitle,event,url,workflowName \
    --jq '"- \(.workflowName) — \(.displayTitle)\n- URL: \(.url)"'
  echo
  echo '<details><summary>first 60 lines of failing step log</summary>'
  echo
  echo '```'
  gh run view "$FAILING_ID" --repo "$REPO" --log-failed 2>/dev/null | head -60 || true
  echo '```'
  echo '</details>'
else
  echo "- _no failing run on this branch_"
fi
