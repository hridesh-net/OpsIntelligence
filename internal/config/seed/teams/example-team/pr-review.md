# PR review policy — example team

When reviewing a pull request, OpsIntelligence must:

1. **Severity rubric** (apply to each finding):
   - `blocker` — merges must be held: security, data loss, correctness regressions, missing tests for public behavior.
   - `must-fix` — merge allowed after fix: maintainability, perf regression on a hot path, unclear error handling.
   - `nit` — optional: style, naming, comments.
2. **Required checks** before approving:
   - CI green on the PR branch (`devops.github.commit_status` or `devops.gitlab.pipelines`).
   - Sonar quality gate is `OK` for `new_code` (`devops.sonar.quality_gate`).
   - No new BLOCKER/CRITICAL Sonar issues introduced.
3. **Style**:
   - Prefer small, surgical diffs — flag PRs > 400 added lines as **large-change** and request a split rationale.
   - Docs updated when public API changes.
   - Tests added for every new branch in business logic.
4. **Review tone** — short, specific, and kind. Link to the exact file/line and, when possible, the relevant section of this policy.

Answer format expected from the agent:

```
## Summary
One-line impact statement.

## Blockers
- file.go:42 — why it's a blocker + concrete fix.

## Must-fix
- …

## Nits
- …

## Ship/Hold
Ship / Hold with reason.
```
