---
name: devops
description: "OpsIntelligence DevOps skill graph — the entry point for PR review, SonarQube triage, CI/CD monitoring, incident response, and runbook execution. Lazy-load a specific node via read_skill_node(\"devops\", \"<node>\")."
user-invocable: false
metadata:
  {
    "opsintelligence": {
      "requires": { "tools": ["devops.*"] },
      "primaryEnvs": ["OPSINTEL_GITHUB_TOKEN", "OPSINTEL_GITLAB_TOKEN", "OPSINTEL_JENKINS_TOKEN", "OPSINTEL_SONAR_TOKEN"]
    }
  }
---

# DevOps — Map of Content

This is the entry node for the built-in DevOps graph. The graph is **read-only
by default** on every surface (PRs, pipelines, Sonar, Jenkins). Any write
action (approve, merge, retry, redeploy, silence a finding) requires explicit
human confirmation in the same turn.

## How to use this graph

1. Read the team policy files loaded from `teams/<active>/*.md`. The team's
   PR / Sonar / CI rules are authoritative — this graph is generic.
2. Pick the node that matches the user's ask and call
   `read_skill_node("devops", "<node>")`:
   - [[pr-review]] — review a pull/merge request end-to-end.
   - [[sonar]] — interpret SonarQube results and gate decisions.
   - [[cicd]] — monitor GitHub Actions, GitLab CI, and Jenkins pipelines.
   - [[incidents]] — triage a production incident from page to postmortem.
   - [[runbooks]] — execute or author a runbook safely.
3. Use the `devops.*` tools registered in the tool catalog to fetch evidence
   (PRs, pipelines, jobs, issues, quality gates). Quote sources; never invent
   IDs, SHAs, or run numbers.
4. Prefer delegating multi-step reasoning to a named **smart prompt chain**
   via the `chain_run` tool. Chains are bounded, self-critiquing pipelines
   and cost far fewer tokens than improvising the whole flow inline:
   - `chain_run {id: "pr-review", inputs: {pr_url: "..."}}` → Ship/Hold verdict.
   - `chain_run {id: "sonar-triage", inputs: {project_key: "..."}}` → gate recommendation.
   - `chain_run {id: "cicd-regression", inputs: {platform: "github", target: "owner/repo/ci.yml"}}` → regression triage.
   - `chain_run {id: "incident-scribe", inputs: {evidence: "..."}}` → brief + update + postmortem skeleton.
   - `chain_run {id: "meta/self-critique", kind: "prompt", inputs: {draft: "..."}}` → pre-send critique.
   Call `chain_list` at runtime if the ids above are stale.

## Output contract (applies to every node)

- Lead with a one-line verdict (e.g. `Ship` / `Hold` / `Roll forward`).
- Group findings under `Blockers`, `Must-fix`, `Nits`, or
  `Action / Monitor / Skip` — whatever the node specifies.
- Always link back to the source URL (PR, pipeline run, Sonar issue).
- Never paste raw tokens, secrets, cookies, or `.env` contents.

## Safety posture

- Read-only by default. Never trigger a deploy, cancel a production
  pipeline, merge a PR, or silence a Sonar rule without an in-turn "yes"
  from a human.
- Summarize logs, do not quote them verbatim — they may contain PII.
- Respect owner-only paths under the state dir (`POLICIES.md`, `RULES.md`,
  `policies/`). You may read them; you may not write to them.

## Companion skills

Built-in standalone skills the agent can invoke directly (alongside
this graph):

- [`gh-pr-review`](../gh-pr-review/SKILL.md) — post a review back to
  GitHub: `gh pr checkout`, disposable worktrees, local lint/test runs,
  the GitHub Reviews API, line-level comments, and one-click
  ```suggestion``` blocks. Use this after the `pr-review` chain
  produces a verdict.
- [`github`](../github/SKILL.md) — generic `gh` CLI cheat-sheet for
  everything that isn't PR-review-specific.
- [`gh-issues`](../gh-issues/SKILL.md) — auto-triage and auto-fix
  flows for GitHub Issues (advanced).

---

Related: [[pr-review]], [[sonar]], [[cicd]], [[incidents]], [[runbooks]].
