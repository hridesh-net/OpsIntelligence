# gh-pr-review — `git` + `gh` command reference

Everything this skill needs, in one page, grouped by review phase.
Copy the snippets; substitute `$REPO` (`owner/repo`) and `$PR`
(integer) where they appear.

## 0. Environment

```bash
# Required
export PATH="$PATH"         # gh and git must be on PATH
export GH_TOKEN="${GH_TOKEN:-$OPSINTEL_GITHUB_TOKEN}"

# Optional: per-call override
gh --repo owner/repo ...    # works on every subcommand below

# Authenticate (one-time)
gh auth login --scopes "repo,read:org"
gh auth status
```

---

## 1. Identify / enumerate PRs

```bash
# List open PRs, newest first
gh pr list --repo "$REPO" --state open --limit 20

# My open PRs across all repos I have access to
gh search prs --author "@me" --state open

# PRs waiting for my review
gh search prs --review-requested "@me" --state open

# Ready-to-merge candidates (green CI, no requested changes)
gh pr list --repo "$REPO" --search "is:open review:approved status:success"

# PR metadata in one shot (shell-friendly)
gh pr view "$PR" --repo "$REPO" \
  --json number,title,author,baseRefName,headRefName,headRefOid,additions,deletions,changedFiles,mergeable,isDraft,labels,url
```

---

## 2. Diff inspection

```bash
# Summary: which files changed
gh pr diff "$PR" --repo "$REPO" --name-only | sort

# Full unified diff (respect $PAGER; use -R for colors)
gh pr diff "$PR" --repo "$REPO" | less -R

# Stats only
gh pr view "$PR" --repo "$REPO" --json additions,deletions,changedFiles --jq .

# Per-file via API (stable line numbers for comments)
gh api "repos/$REPO/pulls/$PR/files" \
  --jq '.[] | {path: .filename, adds: .additions, dels: .deletions, status: .status, patch: (.patch // "") | split("\n") | .[0:10] | join("\n")}'

# Apply the patch as a dry-run
gh pr diff "$PR" --repo "$REPO" | git apply --check -
```

---

## 3. Check out the PR (worktree-first)

```bash
# Preferred: dedicated worktree, no disruption to current HEAD
TMP="$(mktemp -d -t pr-$PR-XXXX)"
git worktree add "$TMP" --detach
cd "$TMP"
gh pr checkout "$PR" --repo "$REPO"   # creates a tracking branch

# Inspect
git log --oneline -20
git diff --stat "origin/$(gh pr view "$PR" --json baseRefName --jq .baseRefName)"...HEAD

# Run the repo's own conventions
make test || npm test || go test ./... || pytest -x -q

# When done
cd -
git worktree remove "$TMP" --force
```

Alternatives when worktrees aren't an option:

```bash
# Raw-fetch, no checkout
git fetch origin "pull/$PR/head:pr-$PR"
git switch pr-$PR

# Comparison against base without switching
git fetch origin "pull/$PR/head"
git diff origin/main..FETCH_HEAD -- 'path/to/subtree/**'
```

---

## 4. CI checks + logs

```bash
# Quick status
gh pr checks "$PR" --repo "$REPO"

# All runs for this PR's head SHA
HEAD_SHA=$(gh pr view "$PR" --repo "$REPO" --json headRefOid --jq .headRefOid)
gh api "repos/$REPO/commits/$HEAD_SHA/check-runs" \
  --jq '.check_runs[] | {name, status, conclusion, url: .details_url}'

# Full run list for the workflow that's failing
gh run list --repo "$REPO" --branch "$(gh pr view "$PR" --json headRefName --jq .headRefName)" --limit 10

# Pull just the failed steps' logs (concise!)
FAILING=$(gh run list --repo "$REPO" --workflow ci.yml --status failure --limit 1 --json databaseId --jq .[0].databaseId)
gh run view "$FAILING" --repo "$REPO" --log-failed | head -200

# Re-run only the failed jobs (WRITE — ask for confirmation first)
# gh run rerun "$FAILING" --repo "$REPO" --failed
```

---

## 5. Review metadata (existing reviews & comments)

```bash
# Reviews on this PR (approve / changes-requested / comment)
gh api "repos/$REPO/pulls/$PR/reviews" \
  --jq '.[] | {user: .user.login, state, submitted_at, body: (.body | .[0:120])}'

# Line comments (review-attached)
gh api "repos/$REPO/pulls/$PR/comments" \
  --jq '.[] | {user: .user.login, path, line, body: (.body | .[0:120])}'

# Issue-style comments on the PR
gh api "repos/$REPO/issues/$PR/comments" \
  --jq '.[] | {user: .user.login, body: (.body | .[0:120])}'

# A single review thread + its replies
REVIEW_ID=...
gh api "repos/$REPO/pulls/$PR/reviews/$REVIEW_ID/comments" --jq '.'
```

---

## 6. Post a review  (WRITE — confirm first)

See [`comments.md`](comments.md) for the body/suggestion templates.
This section is just the plumbing.

```bash
# Draft payload
cat > /tmp/review.json <<'JSON'
{
  "event": "REQUEST_CHANGES",
  "commit_id": "<HEAD_SHA — optional, pins the review to a specific commit>",
  "body": "## Verdict\nHold — 1 blocker, 2 must-fix. CI red on e2e-smoke.\n\n## Summary\n...",
  "comments": [
    { "path": "internal/orders/handler.go", "line": 88, "side": "RIGHT", "body": "..." }
  ]
}
JSON

# Submit
gh api "repos/$REPO/pulls/$PR/reviews" \
  --method POST \
  --input /tmp/review.json \
  --jq '{state, submitted_at, url: .html_url}'
```

**Post-only body (no line comments):** `gh pr review "$PR" --repo "$REPO" --request-changes --body "…"` is fine for a one-liner. Use the API form above whenever you have any line-level feedback.

---

## 7. Reply, resolve, or dismiss

```bash
# Reply to a specific review comment (new thread reply)
COMMENT_ID=123456789
gh api "repos/$REPO/pulls/$PR/comments/$COMMENT_ID/replies" \
  --method POST \
  -f body="Good call — pushed a follow-up in 0afc231."

# Resolve a review thread (GraphQL — REST can't)
THREAD_ID=PRT_...
gh api graphql -f query='
mutation($thread: ID!) {
  resolveReviewThread(input: {threadId: $thread}) { thread { id isResolved } }
}' -f thread="$THREAD_ID"

# Dismiss your own stale review
gh api "repos/$REPO/pulls/$PR/reviews/$REVIEW_ID/dismissals" \
  --method PUT -f message="Stale — author force-pushed."

# Add a label
gh pr edit "$PR" --repo "$REPO" --add-label needs-tests

# Request changes from a specific reviewer
gh pr edit "$PR" --repo "$REPO" --add-reviewer other-dev
```

---

## 8. Merge (WRITE — explicit human "yes" required)

```bash
# Pre-flight
gh pr view "$PR" --repo "$REPO" --json mergeable,mergeStateStatus,reviewDecision --jq .
gh pr checks "$PR" --repo "$REPO"

# Squash-merge, delete branch, pin to the reviewed SHA
HEAD_SHA=$(gh pr view "$PR" --repo "$REPO" --json headRefOid --jq .headRefOid)
gh pr merge "$PR" --repo "$REPO" --squash --delete-branch --match-head-commit "$HEAD_SHA"

# Or merge-commit, or rebase — follow team policy (teams/<active>/pr-review.md)
# gh pr merge "$PR" --merge  --delete-branch --match-head-commit "$HEAD_SHA"
# gh pr merge "$PR" --rebase --delete-branch --match-head-commit "$HEAD_SHA"
```

---

## 9. Local git hygiene during review

Useful `git` idioms that come up often while reviewing:

```bash
# Show the commits the PR adds on top of base
git log --oneline "origin/main..HEAD"

# Show each commit's touched files (good for "tiny commits or not?")
git log --stat --oneline "origin/main..HEAD"

# Word-diff for prose-heavy files (docs, configs)
git diff --word-diff=color "origin/main...HEAD" -- '*.md'

# Find what a specific line looked like before
git blame -L 40,60 -- path/to/file.ext

# What tests exist for a given file
git grep -n -E 'func Test|describe\(|def test_' -- $(git log --name-only --pretty= "origin/main..HEAD" | sort -u)

# Check if the PR touches an owner-only path
git log --name-only --pretty= "origin/main..HEAD" \
  | sort -u \
  | grep -E '^(POLICIES\.md|RULES\.md|policies/)'
```

---

## 10. Environment & safety hooks

```bash
# Verify remotes before any push
git remote -v

# Confirm we're on the PR branch and nothing else
git status
git rev-parse --abbrev-ref HEAD

# NEVER run these without explicit human confirmation:
#   git push --force origin HEAD
#   gh pr merge ... --admin
#   gh pr close ...
#   gh run rerun ...
#   gh run cancel ...
```

Keep those in muscle memory: OpsIntelligence does not invoke them on
its own. If the user asks for a rerun or merge in the same turn, the
agent may run it — once, with the exact flags the user approved.
