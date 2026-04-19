---
name: gh-pr-review
description: "Review a GitHub pull request end-to-end: check out the branch, run the code locally, post line-level comments, code suggestions, and a final Approve / Request-changes / Comment review. Use when: (1) the user says 'review PR X', 'check this PR', 'can we merge #123'; (2) the chain_run `pr-review` has produced a verdict and you need to post it; (3) an on-call engineer asks for a second opinion on a diff. NOT for: GitLab MRs (use gitlab skill), non-GitHub forges, or performing the merge itself — merges always require explicit human confirmation in the same turn."
user-invocable: true
metadata:
  {
    "opsintelligence":
      {
        "emoji": "🔎",
        "requires": { "bins": ["git", "gh"] },
        "primaryEnvs": ["GH_TOKEN", "OPSINTEL_GITHUB_TOKEN"],
        "install":
          [
            { "id": "brew-gh", "kind": "brew", "formula": "gh", "bins": ["gh"], "label": "Install GitHub CLI (brew)" },
            { "id": "apt-gh", "kind": "apt", "package": "gh", "bins": ["gh"], "label": "Install GitHub CLI (apt)" },
            { "id": "brew-git", "kind": "brew", "formula": "git", "bins": ["git"], "label": "Install git (brew)" }
          ]
      }
  }
---

# gh-pr-review — Review a GitHub Pull Request

This skill is the authoritative **how-to** for reviewing a GitHub pull
request end-to-end: pulling it down, reading the diff, running the code,
and posting a review with line-level comments, suggested edits, and a
final Approve / Request-changes / Comment verdict.

It pairs with two other OpsIntelligence pieces:

- The **`devops.github.*`** agent tools (PR metadata, diffs, checks) —
  prefer these when you just need to read data.
- The **`pr-review` smart-prompt chain** — the main agent should fetch
  PR metadata and diff via `devops.github.pull_request` and
  `devops.github.pr_diff`, then call `chain_run` with those strings in
  `inputs` (see `skills/devops/pr-review.md`). Then come back here to **post** them.

> **Safety posture**: this skill is **read-only by default** on GitHub.
> Pushing fixes, approving, merging, re-running CI, and closing PRs are
> all WRITE actions and must be triggered by an explicit human "yes" in
> the same turn. The skill never auto-approves or auto-merges.

---

## When to use

Use this skill when you need to:

1. Fetch a PR locally and inspect its diff without cloning the whole
   history (`gh pr checkout`).
2. Run the PR's code — tests, linters, the application itself — to
   verify behaviour before reviewing.
3. Post a **line-level comment** on a specific `file:line` range.
4. Post a **code suggestion** the author can accept with one click
   (GitHub's ``` ```suggestion``` ``` code block).
5. Submit a final review verdict: `APPROVE`, `REQUEST_CHANGES`, or
   `COMMENT`.
6. Reply to an existing review thread or resolve it.

## When NOT to use

- GitLab merge requests → use the GitLab skill / `devops.gitlab.*` tools.
- Analysing a diff you already have as text → use the
  [[pr-review]] skill node and the `pr-review` chain.
- Bulk operations across dozens of PRs → write a shell script with
  `gh api` and pagination; this skill is per-PR.
- Actually merging → this skill stops at REVIEW submission; merges
  require a separate, human-confirmed step.

---

## Prerequisites

```bash
# Once per workstation
gh auth login            # interactive, or:
echo "$OPSINTEL_GITHUB_TOKEN" | gh auth login --with-token

# Verify
gh auth status           # shows scopes; need at least repo, read:org
git --version            # any modern git is fine
```

If `gh` is not installed, run the skill's install hook (`brew-gh` or
`apt-gh` in the metadata above) or follow
<https://cli.github.com/manual/installation>.

---

## The review workflow (opinionated)

Follow these six phases in order. Steps marked **[READ]** are safe
read-only operations; steps marked **[WRITE]** touch GitHub or local
branches and must be announced to the user before running.

### Phase 1 — Identify the PR  [READ]

The input is always a URL, a `owner/repo#N` coordinate, or a bare `#N`
inside a clone. Normalise it first:

```bash
# URL → owner/repo + number
PR_URL="https://github.com/acme/api/pull/123"
REPO=$(echo "$PR_URL" | sed -E 's#https://github.com/([^/]+/[^/]+)/pull/.*#\1#')
PR=$(echo   "$PR_URL" | sed -E 's#.*/pull/([0-9]+).*#\1#')

# Or resolve from a local checkout
gh pr view --json number,baseRefName,headRefName,url --jq .
```

Reject the review early if:

- The repo is archived: `gh repo view "$REPO" --json isArchived --jq .isArchived`.
- The PR is already merged or closed:
  `gh pr view "$PR" --repo "$REPO" --json state --jq .state`.
- The PR is a draft and the user didn't explicitly ask for a draft review.

### Phase 2 — Gather evidence  [READ]

Pull structured metadata and CI/Sonar status before reading the diff —
it shapes what severity you'll assign to findings.

```bash
# PR header: title, author, base/head, size
gh pr view "$PR" --repo "$REPO" \
  --json number,title,author,baseRefName,headRefName,additions,deletions,changedFiles,mergeable,isDraft,labels \
  --jq '.'

# CI status: the single source of truth for "is the build green?"
gh pr checks "$PR" --repo "$REPO"

# Linked issues
gh pr view "$PR" --repo "$REPO" --json closingIssuesReferences \
  --jq '.closingIssuesReferences[]?.number'
```

If you have the **pr-review smart chain** available, prefer:

```
chain_run {"id": "pr-review", "inputs": {"pr_url": "<url>"}}
```

It returns a complete Ship/Hold verdict with severity-graded findings.
Keep that output; Phase 5 posts it back to GitHub.

### Phase 3 — Read the diff  [READ]

Start with a top-down shape; never dive into individual files before
understanding which files changed and how big each delta is.

```bash
# Files touched + sizes (sorted by churn)
gh pr diff "$PR" --repo "$REPO" --name-only | sort

# Full unified diff (paginate with less if large)
gh pr diff "$PR" --repo "$REPO" | less -R

# Per-file diff via API (handy when you need stable line numbers)
gh api "repos/$REPO/pulls/$PR/files" \
  --jq '.[] | {path: .filename, adds: .additions, dels: .deletions, status: .status}'
```

Flag a **large-change** warning when additions > 400 lines; ask the
author for a split rationale before a detailed review.

### Phase 4 — Run the code locally  [WRITE — local only]

Check out the PR branch into a disposable worktree so you do not
disturb the user's current work:

```bash
# Preferred: a dedicated worktree per review
TMP_DIR="$(mktemp -d -t pr-review-$PR-XXXX)"
git worktree add "$TMP_DIR" --detach
cd "$TMP_DIR"
gh pr checkout "$PR" --repo "$REPO"

# Quick alternatives (only if worktrees are not an option)
# gh pr checkout "$PR"                    # checks out into current repo
# gh pr diff "$PR" | git apply --check    # dry-run the patch, no checkout
```

Now run whatever the repo's convention calls for. Start with the
cheapest verification first:

```bash
# 1. Formatter / linter  (seconds)
#    e.g. Go:
go vet ./...
#    e.g. Node:
npm run lint --silent
#    e.g. Python:
ruff check . && mypy .

# 2. Unit tests           (seconds-to-minutes)
go test ./... -count=1
npm test --silent
pytest -x -q

# 3. Build                 (may be long — only if 1+2 pass)
make build
```

When you finish, clean up the worktree:

```bash
cd -
git worktree remove "$TMP_DIR" --force
```

Everything you learn in this phase — failing tests, lint warnings,
build errors — becomes evidence you cite in Phase 5.

### Phase 5 — Write the review  [WRITE]

GitHub distinguishes three kinds of review feedback. Pick the right one
per finding:

| Kind             | What it is                                    | How you post it                     |
| ---------------- | --------------------------------------------- | ----------------------------------- |
| Review **body**  | High-level summary, verdict                   | `gh pr review --body`              |
| **Line comment** | Concern on a specific line range              | `gh api pulls/{n}/reviews` w/ body |
| **Suggestion**   | A one-click apply-able patch on a line range | ```` ```suggestion ``` ```` block  |

**Draft the whole review in one JSON body**, then submit it as a single
review — never drip-post individual comments. This matches the UI's
"Submit review" flow and avoids the email-per-comment spam.

A single "review with comments and suggestions" looks like this:

```bash
# review.json
cat > review.json <<'JSON'
{
  "event": "REQUEST_CHANGES",
  "body": "## Verdict\nHold — 1 blocker + 2 must-fix. CI failing on e2e-smoke (see run <url>).\n\n## Summary\nAdds a new /orders endpoint. Good tests, but missing auth on DELETE and a migration that drops a column without a backfill.",
  "comments": [
    {
      "path": "internal/orders/handler.go",
      "line": 88,
      "side": "RIGHT",
      "body": "Blocker — DELETE /orders/{id} has no auth middleware. Every other verb does. Suggested fix:\n\n```suggestion\n\tmux.Handle(\"DELETE /orders/{id}\", auth.Required(deleteOrder))\n```"
    },
    {
      "path": "db/migrations/0042_drop_legacy_col.sql",
      "line": 3,
      "side": "RIGHT",
      "body": "Must-fix — dropping `legacy_status` without a backfill will break the read replica. Either add a default or run the backfill migration first (see team playbook `secrets-and-safety.md`)."
    }
  ]
}
JSON

# Preview the payload
jq . review.json

# Submit as a single review
gh api "repos/$REPO/pulls/$PR/reviews" \
  --method POST \
  --input review.json
```

Key rules for the `comments[]` array:

- `path` — repo-relative path from the PR's diff (not an absolute path).
- `line` — the line number **in the destination file** on the `RIGHT`
  side for additions, or the **original file** on the `LEFT` side for
  deletions.
- `side` — `RIGHT` for new / modified lines (the common case),
  `LEFT` for lines the PR removed.
- Multi-line comment: add `"start_line": N, "start_side": "RIGHT"`
  alongside `line` and `side` to cover a range.
- `body` accepts Markdown. Use it.

**Suggestions** are just fenced code blocks tagged `suggestion` inside
a comment body:

```markdown
Consider returning a typed error here:

```suggestion
	return nil, fmt.Errorf("orders: stale tx: %w", err)
```
```

The diff inside the ```` ```suggestion ``` ```` block must line up
**exactly** with the line(s) the comment targets — GitHub computes the
replacement as "take line `line` (or `start_line..line`) and swap it
for the block's contents". If the author clicks "Apply suggestion",
that's the commit message your one-line hint becomes.

For the three review verdicts, set `event` to:

- `APPROVE` — you're happy; merge whenever.
- `REQUEST_CHANGES` — hold; author must address before merge.
- `COMMENT` — neutral; used for questions or non-blocking notes.

Or omit `event` to save the review as a draft that the user can submit
from the UI.

See [`comments.md`](comments.md) for many more templates
(nit vs. must-fix wording, suggestion blocks for renames, multi-file
suggestions, replying to existing threads, resolving conversations).

### Phase 6 — Follow up  [READ / conditional WRITE]

After the review is posted:

```bash
# Confirm the review landed
gh pr view "$PR" --repo "$REPO" --json reviews --jq '.reviews[-1]'

# If the author pushes fixes, re-check:
gh pr checks "$PR" --repo "$REPO"
gh pr diff "$PR" --repo "$REPO" --name-only
```

To **approve and merge** once all blockers are addressed, ask the user
for explicit confirmation, then (and only then) run:

```bash
# READ: double-check the ref is still what the user expects
HEAD_SHA=$(gh pr view "$PR" --repo "$REPO" --json headRefOid --jq .headRefOid)
echo "Merging HEAD=$HEAD_SHA"

# WRITE: squash-merge (match your team's policy — rebase/merge also valid)
gh pr merge "$PR" --repo "$REPO" --squash --delete-branch \
  --match-head-commit "$HEAD_SHA"
```

`--match-head-commit` protects against a race where the author pushes
while you're merging.

---

## Output format when you surface the review to the user

Whenever you show a user the review you are about to post, use the same
shape the `pr-review` chain emits:

```
## Verdict
Ship / Hold / Hold-with-fixes — one-sentence why.

## Blockers
- `path/file.ext:42` — what's wrong + concrete fix.

## Must-fix
- `path/file.ext:77` — what's wrong + concrete fix.

## Nits
- `path/file.ext:120` — optional polish.

## Evidence
- PR: <url>
- CI: <run url> — pass | fail | pending
- Sonar (new code): OK | ERROR — <link>

## Confidence
N/10 — one-sentence reason.
```

Then ask: "Post this as an **Approve / Request-changes / Comment**
review on `owner/repo#N`?" — and wait for confirmation before running
the write-action in Phase 5.

---

## Deeper references

- [`commands.md`](commands.md) — the full `git` + `gh` + `gh api` cheat
  sheet used in this skill (diff inspection, worktrees, log
  spelunking, rerun CI, fetch review comments).
- [`comments.md`](comments.md) — every review-comment and suggestion
  template (single-line suggestion, multi-line suggestion, rename
  suggestion, reply-to-thread, resolve-thread, nit vs. must-fix
  wording).
- [`scripts/`](scripts) — runnable helpers:
  - `scripts/pr-evidence.sh` — one-shot PR evidence dump.
  - `scripts/post-review.sh` — safe wrapper around the reviews API.
  - `scripts/apply-and-test.sh` — check out the PR into a worktree,
    run the team's lint+test+build, clean up.

---

## Troubleshooting

| Symptom                                     | Cause / fix                                                                                           |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `HTTP 403: Resource not accessible`         | Token lacks `repo` scope. Re-login with `gh auth login --scopes "repo,read:org"`.                    |
| `Validation Failed: path is outside the diff` | The `path` or `line` doesn't match the PR's diff. Re-fetch `pulls/{n}/files` and use its `filename`. |
| Suggestion block does not apply cleanly     | The block's content must exactly replace `line` (or `start_line..line`). Trim trailing whitespace.    |
| `gh pr checkout` fails with `refs/pull not found` | Fork PR. Run with `--detach` or first: `git fetch origin pull/$PR/head:pr-$PR`.                |
| Rate-limit `HTTP 429`                       | Use `gh api --cache 1h` for repeat reads, or back off with `sleep 60`.                               |
| Review shows zero line comments             | You posted the body via `gh pr review --body` but comments must go through `pulls/{n}/reviews` in a single request. |

---

## Related

- [[pr-review]] — the generic DevOps skill-graph node (platform-agnostic).
- Smart prompt chain [`chain:pr-review`](../../doc/smart-prompts.md) —
  produces the verdict you'll post here.
- [[sonar]] — decide whether the Sonar gate is a blocker for this PR.
- [[cicd]] — when CI failed, triage before reviewing code.
