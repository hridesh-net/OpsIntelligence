---
name: sonar
summary: "Read SonarQube/SonarCloud quality gates, issues, and hotspots; decide block vs flag vs ignore."
---

# Sonar Triage

Use this node when the user asks "is Sonar green", "what did Sonar flag",
"triage quality gate", or during a PR review that needs a quality
decision. The tool surface is `devops.sonar.*`.

> **Fast path**: `chain_run {id: "sonar-triage", inputs: {project_key: "<key>"}}`
> executes the fetch → classify → recommend pipeline documented below and
> returns a Ship/Hold verdict with per-issue actions.

## Inputs

1. **Project key.** Either provided by the user, or derived from the repo
   (configured as `devops.sonar.project_key_prefix`). If ambiguous, list
   candidate projects via `devops.sonar.search_projects` and ask.
2. **Scope.** `new_code` (PR / feature branch) vs `overall` (tech debt).
   Default: `new_code` for PRs, `overall` only on explicit ask.
3. **Team policy.** Read `teams/<active>/sonar.md` if present — those
   thresholds win over the defaults below.

## Evidence to fetch

- `devops.sonar.quality_gate(projectKey, branch?, pullRequest?)` — the
  single most important signal.
- `devops.sonar.search_issues(projectKey, severities=[BLOCKER,CRITICAL,MAJOR], types=[BUG,VULNERABILITY,CODE_SMELL], sinceLeakPeriod=true)`.
- `devops.sonar.hotspots_search(projectKey, status=TO_REVIEW)`.

## Default rubric

| Severity | Action |
|---|---|
| BLOCKER | Treat as merge blocker. If discovered on `main`, page on-call. |
| CRITICAL | Must be fixed in the same PR, or the PR must link an opened ticket. |
| MAJOR | Flag in PR review; do not block. Owner fixes in the same sprint. |
| MINOR / INFO | Ignore unless part of a broader cleanup PR. |

Security hotspots: never auto-mark `SAFE`. The agent may summarize them
and propose a review assignee; only a human can change hotspot status.

## Output format

```
## Quality gate
<OK | WARN | ERROR> — <one sentence reason>

## New code findings (since leak period)
| Severity | Rule | File:Line | Link |
|----------|------|-----------|------|
| CRITICAL | … | … | … |

## Hotspots to review
- <file>:<line> — <summary> — <link>

## Recommendation
<Block | Flag | Accept> for PR #<N>, with reason.
```

## False-positive handling

Never silence a rule globally. If a finding is a legitimate false
positive, open a ticket in the team's tracker, reference it on the
Sonar issue, and record the decision in the PR. Add the rule+component
to the team's WONTFIX list (in `teams/<active>/sonar.md`) — do not edit
Sonar's built-in rule profiles without security sign-off.

---

Related: [[pr-review]], [[cicd]], back to [[SKILL]].
