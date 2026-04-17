#!/usr/bin/env bash
# post-review.sh — safe wrapper around POST /repos/:o/:r/pulls/:pr/reviews.
#
# Validates the payload, prints a preview, and requires an explicit
# "yes" on stdin before submitting. Designed to be invoked by the agent
# after the user has confirmed the review content.
#
# Usage:
#   post-review.sh <owner/repo> <pr> <review.json>
#   post-review.sh <owner/repo> <pr> <review.json> --yes    # non-interactive
#
# The JSON file must follow GitHub's Pulls API schema:
#   https://docs.github.com/en/rest/pulls/reviews?apiVersion=2022-11-28#create-a-review-for-a-pull-request
#
# Required keys: body, event (APPROVE|REQUEST_CHANGES|COMMENT).
# Optional: comments[], commit_id.
set -euo pipefail

die() { echo "post-review: $*" >&2; exit 2; }

require() { command -v "$1" >/dev/null 2>&1 || die "missing dependency: $1"; }
require gh
require jq

REPO="${1:-}"; PR="${2:-}"; JSON="${3:-}"; CONFIRM="${4:-}"
[[ -n "$REPO" && -n "$PR" && -n "$JSON" ]] \
  || die "usage: $0 <owner/repo> <pr> <review.json> [--yes]"
[[ -f "$JSON" ]] || die "review file not found: $JSON"

jq -e type < "$JSON" >/dev/null || die "review file is not valid JSON"
EVENT=$(jq -r '.event // empty' < "$JSON")
case "$EVENT" in
  APPROVE|REQUEST_CHANGES|COMMENT) ;;
  "")                              die "review.json is missing required 'event' field";;
  *)                               die "invalid event '$EVENT' (expected APPROVE|REQUEST_CHANGES|COMMENT)";;
esac
jq -e '.body | type == "string" and length > 0' < "$JSON" >/dev/null \
  || die "review.json needs a non-empty 'body'"

echo "── Review preview ─────────────────────────────────────────"
echo "repo:        $REPO"
echo "pr:          #$PR"
echo "event:       $EVENT"
echo "body chars:  $(jq -r '.body | length' < "$JSON")"
echo "comments:    $(jq '.comments // [] | length' < "$JSON")"
echo
jq '.comments // [] | .[] | "  - \(.path):\(.line // (.start_line // 0))  \((.body // "")[0:70])"' -r < "$JSON"
echo "───────────────────────────────────────────────────────────"

if [[ "$CONFIRM" != "--yes" ]]; then
  printf 'Submit this review to %s#%s? [type "yes" to confirm] ' "$REPO" "$PR"
  read -r ans
  [[ "$ans" == "yes" ]] || { echo "aborted."; exit 1; }
fi

RESP=$(gh api "repos/$REPO/pulls/$PR/reviews" --method POST --input "$JSON")
URL=$(printf '%s' "$RESP" | jq -r '.html_url // .url')
SUBMITTED=$(printf '%s' "$RESP" | jq -r '.submitted_at // "draft"')
echo "submitted ($SUBMITTED): $URL"
