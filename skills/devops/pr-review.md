---
name: pr-review
summary: "Review a pull/merge request: diff, CI status, Sonar gate, and team policy."
---

# PR Review

Use this node when the user asks "review PR X", "should we merge Y", or
"what's blocking #123". The workflow is the same whether the source is
GitHub or GitLab.

> **Fast path (thinking)**: call `chain_run` with
> `{"id": "pr-review", "inputs": {"pr_url": "<url>"}}`. That chain
> already implements the gather → analyze → critique → render flow
> documented below, with a built-in self-critique pass.
>
> **Fast path (posting the review back to GitHub)**: once the chain has
> produced a verdict, follow the [`gh-pr-review`](../gh-pr-review/SKILL.md)
> skill — it covers `gh` / `git` commands for checkout + local tests,
> the GitHub Reviews API, line-level comments, and one-click
> `suggestion` blocks.

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
| Metadata (title, author, base/head, draft) | `devops.github.get_pr` | `devops.gitlab.list_mrs` (scoped) |
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

## Output format

```
## Verdict
Ship / Hold — one line with the single most important reason.

## Evidence
- CI: <status> (<run URL>)
- Sonar: <gate> (<project link>)
- Size: +<adds> / -<dels> across <N> files

## Blockers
- <path>:<line> — <why it's a blocker> — <concrete fix>.

## Must-fix
- …

## Nits
- …

## Links
- PR: <url>
- CI run: <url>
- Sonar: <url>
```

## Never do

- Merge, approve, dismiss reviews, or apply labels without explicit
  in-turn human approval.
- Invent SHAs, run numbers, or Sonar issue keys — quote them verbatim
  from tool output.
- Paste secrets or tokens found in a diff; truncate to 4 chars and
  recommend rotation.

---

Related: [[sonar]], [[cicd]], back to [[SKILL]].
