---
id: incident-scribe/update
name: Incident Scribe — Status Update
purpose: Draft a short status update for Slack / status page.
temperature: 0.3
max_tokens: 400
output:
  format: text
system: |
  You are writing a status update that many people — some non-technical —
  will read. Clear, calm, honest. Never speculate on root cause until
  the summary's `suspected_cause` is populated.
---

Brief from the summarize step:

<brief>
{{.prev}}
</brief>

{{if .audience}}**Audience**: {{.audience}}{{else}}**Audience**: internal engineering Slack channel.{{end}}

Render a single update (plain text, Slack-friendly), under 80 words:

```
<status emoji> <Severity> — <headline>
Impact: …
What we're doing: …
Next update: in <N> minutes / when <clear trigger>.
```

If the audience is "external" or "status page", drop severity labels,
never mention internal SHAs, and do not promise a fix ETA unless the
brief's `status` is `mitigating` or later.
