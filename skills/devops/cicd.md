---
name: cicd
summary: "Monitor and reason about CI/CD pipelines across GitHub Actions, GitLab CI, and Jenkins."
---

# CI/CD Monitoring

Use this node when the user asks "is the build green", "why did the pipeline
fail", "show me the last N runs", or "is staging healthy". The tool surface
is `devops.github.*`, `devops.gitlab.*`, and `devops.jenkins.*`.

> **Fast path for regressions**: `chain_run {id: "cicd-regression", inputs:
> {platform: "github|gitlab|jenkins", target: "<workflow or job id>"}}`.
> The chain fetches recent runs, compares failing vs. last-good, and
> produces a human-actionable triage report. This agent NEVER retries,
> cancels, or redeploys pipelines — the chain only recommends actions.

## Inputs

1. **Platform.** Infer from the repo/host (`github.com`, `gitlab.com` or
   self-hosted GitLab, or a Jenkins base URL). If multiple are configured,
   ask which one unless the context makes it obvious.
2. **Scope.** A specific run/build, a branch (default `main`), or "all
   required pipelines". Default to the **last 5 runs** on the default
   branch so the user sees trends, not a single data point.
3. **Team policy.** Read `teams/<active>/cicd.md` if present — it
   defines required pipelines, flaky-test policy, and rollback steps.

## Evidence to fetch

- **GitHub Actions**
  - `devops.github.workflow_runs(owner, repo, branch?, status?, limit=5)`
  - `devops.github.commit_status(owner, repo, ref)` — aggregates
    check-runs into pass/fail/pending.
- **GitLab CI**
  - `devops.gitlab.pipelines(projectPath, ref?, status?, limit=5)`
  - `devops.gitlab.pipeline_jobs(projectPath, pipelineID)` when a run
    failed and you need to point at the failing job.
- **Jenkins**
  - `devops.jenkins.job(path)` — returns last build and health report.
  - `devops.jenkins.build(path, buildNumber)` — duration, result, URL.

Always include the run URL in your reply.

## Output format

```
## Status
<Green | Red | Mixed> on <branch> over the last <N> runs.

## Recent runs
| # | Status | Duration | Trigger | Link |
|---|--------|----------|---------|------|
| 412 | success | 4m12s | push by alice | … |
| 411 | failure | 6m03s | push by bob   | … |
| …

## Failing job (if any)
- Job: <name>
- First failing step: <step name>
- Probable cause: <1-2 sentence summary, no raw log dump>
- Link: <job URL>

## Suggested next step
<Retry | Investigate | Roll back | Notify on-call> — with reason.
```

## Safety — never automate these

- Cancelling or retriggering a **production** pipeline.
- Approving a manual gate (staging → prod promotion).
- Rolling back a deployment.
- Quarantining a flaky test (that's a human's call per team policy).

All of the above require an explicit in-turn confirmation from a human.
The agent may **prepare** the command (e.g. link to the Jenkins button
or the `gh workflow run` invocation) but does not execute it.

## Flaky vs. broken

Before calling a failure "flaky":
1. Check the last 10 runs for the same job on the same branch.
2. If ≥ 8 passed, the single failure might be flake — still surface it.
3. If ≥ 3 of the last 10 failed on the same step, it is **not** flake —
   call it a regression and escalate per [[incidents]].

---

Related: [[incidents]], [[runbooks]], back to [[SKILL]].
