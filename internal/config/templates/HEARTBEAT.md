---
title: "HEARTBEAT.md (OpsIntelligence)"
summary: "DevOps morning/periodic sweep — what the agent checks on every tick."
read_when:
  - Configuring proactive agent behavior
  - A cron tick fires the agent with prompt 'heartbeat'
---

# Heartbeat checklist

Keep this file short. The agent reads it on each scheduled heartbeat
(see `cron:` in `opsintelligence.yaml`). The goal is a 1-minute
situational report, not a deep investigation.

## Default sweep (all surfaces that are configured)

- [ ] **CI/CD** — any **red** runs on `main` in the last 24h?
  Use `devops.github.workflow_runs`, `devops.gitlab.pipelines`, and
  `devops.jenkins.job`. Cross-check with `teams/<active>/cicd.md`
  required-pipelines list.
- [ ] **Quality gate** — any project flipping from OK → ERROR?
  Use `devops.sonar.quality_gate` per configured project key.
- [ ] **PR hygiene** — PRs open > 3 days with no review, or > 7 days
  overall, on repos under `devops.github.default_org` /
  `devops.gitlab.default_group`.
- [ ] **Pages / incidents** — if an MCP observability server is wired
  (PagerDuty, Opsgenie, Sentry, Datadog), list open SEV1/SEV2 from the
  last 12h.

## Output contract

- Lead with `HEARTBEAT_OK` on its own line if nothing needs attention.
- Otherwise:
  ```
  HEARTBEAT
  - CI: <summary with counts + links>
  - Sonar: <summary>
  - PRs: <count aging out + one example link>
  - Incidents: <open count + links>
  ```
- Never repeat the same item two ticks in a row without adding new
  context (e.g. a fresh failure count, a new comment). Users tune out
  repeating noise.

## Safety

- Read-only. The heartbeat never retries pipelines, merges PRs, or
  acknowledges pages on its own.
- Respect team quiet hours if defined in `teams/<active>/cicd.md` —
  still collect evidence, but hold the post until the window ends.
