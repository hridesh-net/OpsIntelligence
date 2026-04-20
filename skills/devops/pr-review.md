---
name: pr-review
summary: "Review a pull/merge request: diff, CI status, Sonar gate, and team policy."
---

# PR Review

Use this node when the user asks "review PR X", "should we merge Y", or
"what's blocking #123". The workflow is the same whether the source is
GitHub or GitLab.

> **Chain steps**: the `pr-review` chain runs **without tools** (LLM-only
> steps). The **outer agent** must call GitHub DevOps tools first, then
> pass evidence into the chain:
>
> ```
> chain_run {
>   “id”: “pr-review”,
>   “inputs”: {
>     “pr_url”:          “<url>”,
>     “github_pr_json”:  “<output of devops.github.pull_request>”,
>     “github_diff”:     “<truncated devops.github.pr_diff>”,
>     “github_ci_hint”:  “optional CI summary”
>   }
> }
> ```
>
> The chain runs **gather → analyze → critique → render → post** (five steps).
> The final `post` step returns a **structured JSON payload** with three keys:
>
> | Key        | Contents |
> |------------|---------|
> | `event`    | `”APPROVE”`, `”REQUEST_CHANGES”`, or `”COMMENT”` |
> | `body`     | Top-level Markdown summary (Verdict, Walkthrough, Evidence, Confidence) |
> | `comments` | Array of `{path, line, side, body}` — one entry per inline finding |
>
> **Posting the review back to GitHub — REQUIRED steps**:
>
> 1. Parse the JSON payload from the `post` step.
> 2. Call **`devops.github.submit_review`** with:
>    - `owner`, `repo`, `number` — from the PR URL / metadata
>    - `event` — from the payload
>    - `body` — from the payload
>    - `comments` — the full `comments[]` array from the payload
>
>    This posts the verdict body **plus** all inline line-level comments in
>    one atomic request — exactly like CodeRabbit. No `gh` binary or local
>    checkout is needed.
>
> 3. For a top-level-only reply (no inline comments), you may use
>    `devops.github.pr_comment` instead — but prefer `submit_review` whenever
>    the chain found at least one grounded `path:line` finding.
>
> **Never** post the rendered Markdown as a flat `pr_comment` when the `post`
> payload contains a non-empty `comments[]` — that loses all inline context.
>
> **CodeRabbit-style automation**: install `gh`, set `GH_TOKEN` / `OPSINTEL_GITHUB_TOKEN`,
> run `opsintelligence skills install gh-pr-review`, then wire the GitHub webhook
> `prompts.pull_request` so the outer agent runs this chain and posts one review.
> See [doc/github-webhooks.md](../../doc/github-webhooks.md)
> (“Gateway exposure”, “GitHub token and gh”, “CodeRabbit-style layout”).

## Inputs you must collect first

1. **Target URL or coordinates.** If the user gave a URL, parse
   `owner/repo#N` (GitHub) or `group/project!N` (GitLab). If they gave
   ambiguous text, ask — do not guess.
2. **Team policy.** Read the policy fragments that were merged into the
   system prompt from `teams/<active>/pr-review.md`. Prefer team rules
   over the generic defaults below.

## Evidence to fetch (via `devops.*` tools)

| What | GitHub tool | GitLab tool |
|---|---|---|
| Metadata (title, author, base/head, draft) | `devops.github.pull_request` | `devops.gitlab.list_mrs` (scoped) |
| Diff / changed files | `devops.github.pr_diff` | `devops.gitlab.list_mrs` + manual URL |
| CI status | `devops.github.commit_status` + `devops.github.workflow_runs` | `devops.gitlab.pipelines` |
| Quality gate | `devops.sonar.quality_gate` (projectKey derived from repo) | same |
| New issues | `devops.sonar.search_issues` (`sinceLeakPeriod=true`) | same |

If any tool is disabled (no token configured), say so explicitly instead of
fabricating results.

## Default review rubric (fallback when the team has no policy file)

- **Blocker** — security, data loss, correctness regression, missing tests
  for public behavior, unbounded external calls, secrets in diff.
- **Must-fix** — maintainability, perf regression on a hot path, unclear
  error handling, missing observability for a new code path.
- **Nit** — style, naming, comments, import ordering.

## Checks before recommending "Ship"

1. CI green on the PR branch (all required workflows/pipelines passed).
2. Sonar quality gate for **new code** is `OK`.
3. No new BLOCKER/CRITICAL Sonar issues introduced by the diff.
4. PR size is within the team's limit (default: flag > 400 added lines as
   `large-change` and ask for a split rationale).
5. Tests exist for every new branch in business logic.

## Output format (what the user sees after posting)

Once `devops.github.submit_review` succeeds, summarise to the user:

```
Posted review on <owner/repo>#<N>:
- Verdict: Ship | Hold | Hold-with-fixes
- Inline comments: <N> (across <files>)
- Review URL: <html_url from submit_review response>
```

The detailed findings live as GitHub inline comments directly on the diff —
do not repeat them in full in the chat reply.

## Never do

- Merge, approve, dismiss reviews, or apply labels without explicit
  in-turn human approval.
- Invent SHAs, run numbers, or Sonar issue keys — quote them verbatim
  from tool output.
- Paste secrets or tokens found in a diff; truncate to 4 chars and
  recommend rotation.
- Post the full Markdown review as a flat `devops.github.pr_comment`
  when the `post` chain step returned a non-empty `comments[]`. Use
  `devops.github.submit_review` with `comments` instead — it posts the
  same verdict body plus all inline findings in one request.

---

Related: [[sonar]], [[cicd]], back to [[SKILL]].
