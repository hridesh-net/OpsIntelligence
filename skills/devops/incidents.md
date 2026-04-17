---
name: incidents
summary: "Triage a production incident from page to stable state, with a trail for postmortem."
---

# Incident Triage

Use this node when the user says "there's an incident", "prod is down", a
page just fired, or when [[cicd]] detects a regression on `main`. The
agent's job is to **assist**, not to run the incident — a human
incident commander (IC) is always in charge.

> **Fast path**: once you have any meaningful evidence (alert payload,
> log excerpt, dashboard screenshot text), call
> `chain_run {id: "incident-scribe", inputs: {evidence: "<paste>"}}`.
> It emits a SEV-graded brief, a draft Slack update, and a blameless
> postmortem skeleton in one pass.

## First 5 minutes

1. **Acknowledge and anchor.** Confirm the surface (Slack channel, page
   ID, pipeline run) and who the current IC is. If no IC is declared,
   ask who it is — do not assume.
2. **Scope the blast radius.** What service? What % of traffic / users?
   Is PII or revenue affected? If unclear, mark it "unknown" — do not
   speculate.
3. **Freeze writes by default.** Do not trigger deploys, retries, or
   rollbacks without the IC saying so in the same turn.

## Evidence to gather (read-only)

- **CI/CD**: latest runs on `main` for the affected service via
  [[cicd]] (`devops.github.workflow_runs`, `devops.gitlab.pipelines`,
  `devops.jenkins.job`).
- **Recent PRs merged in the last 24h** (`devops.github.list_prs state=closed`,
  `devops.gitlab.list_mrs state=merged`). The most recent merge is the
  most likely suspect.
- **Sonar** quality gate deltas on the same branch via [[sonar]].
- **Any configured MCP observability servers** (Datadog, Sentry, Grafana,
  PagerDuty). If they are not configured, say so explicitly.

## Output format (repeat every few minutes until stable)

```
## Incident update #<N> — <HH:MM local>
- Severity: SEV<1|2|3>
- Status: Investigating | Identified | Mitigated | Monitoring | Resolved
- Blast radius: <what is impacted>
- Suspected cause: <1 sentence, flagged as a hypothesis>
- Next step (who): <person> to <action> by <time>

## Evidence collected since last update
- …

## Risks if we act
- …
```

## Rollback decision checklist

The agent does **not** roll back. It drafts the checklist for the IC:

- [ ] Is revenue or PII affected? (If yes, lean rollback.)
- [ ] Is there a clean tag to roll back to? (Name it.)
- [ ] Is the fix-forward in active development? (Who, ETA.)
- [ ] Is the rollback itself risky (schema change, data migration)?
- [ ] Who presses the button and who confirms?

Only after the IC says "roll back" in the same turn does the agent
produce the exact command or pipeline link (from [[cicd]]).

## After the incident

Produce a postmortem scaffold in `teams/<active>/postmortems/<date>-<slug>.md`
(if the directory exists; otherwise print it for a human to save):

```
# Incident <date> — <slug>
- IC: …
- Duration: <start> → <end>
- User impact: …
- Root cause: …
- Timeline (UTC): …
- What went well: …
- What we'll change: …
- Action items (owner / due date / ticket): …
```

---

Related: [[cicd]], [[runbooks]], back to [[SKILL]].
