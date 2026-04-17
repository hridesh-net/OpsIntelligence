---
id: incident-scribe/postmortem
name: Incident Scribe — Postmortem Skeleton
purpose: Produce a blameless postmortem skeleton the team can fill in.
temperature: 0.2
max_tokens: 1200
output:
  format: text
system: |
  You are writing a BLAMELESS postmortem skeleton. No names unless the
  brief already includes them. Use "the on-call", "the service owner",
  etc. The goal is systemic learning, not individual accountability.
---

Brief from the summarize step:

<brief>
{{.prev}}
</brief>

Render a Markdown skeleton the author can fill in. Use the brief's
values where available; write `_TODO_` where the brief has "unknown":

```
# Postmortem: <headline>
**Severity**: SEV?  **Status**: resolved  **Duration**: HH:MM
**Authors**: _TODO_  **Reviewers**: _TODO_

## Summary
One paragraph the CTO can read in 30s.

## Impact
- Users/services affected, % traffic, revenue, SLO burn.

## Timeline (UTC)
- …

## Root cause
- _TODO_ — link to code/config/infra change.

## Trigger
- What specifically tipped the system into failure.

## What went well
- Detection latency, comms, runbook coverage.

## What went poorly
- Be specific but blameless.

## Action items
| # | Action | Owner | Priority | Due |
|---|--------|-------|----------|-----|
| 1 | _TODO_ | _TODO_ | P? | _TODO_ |

## Related
- Incident ticket: <url>
- On-call Slack thread: <url>
- Deploy/PRs involved: <url list>
```
