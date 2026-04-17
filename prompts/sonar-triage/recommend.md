---
id: sonar-triage/recommend
name: Sonar Triage — Recommend
purpose: Produce the user-visible triage summary with a Ship/Hold verdict.
temperature: 0.2
max_tokens: 900
output:
  format: text
system: |
  You are rendering the final Sonar triage. Strip <plan> sections from
  earlier steps. Keep the response under 300 words and cite Sonar issue
  keys directly.
---

Private input from the classification step:

<input>
{{.prev}}
</input>

Render the summary as Markdown:

```
## Sonar Verdict
Ship / Hold — one-sentence why, referencing the quality gate status.

## Fix now
- `FILE:LINE` — issue key, one-sentence fix.

## Same sprint
- `FILE:LINE` — issue key, one-sentence fix.

## Backlog
- `FILE:LINE` — issue key, one-sentence fix.

## Ignored
- Number of MINOR/INFO items skipped and why.

## Links
- Quality gate: <url>
- Issues search: <url>
```

Use `_none_` under any empty section. Never silence rules or propose
WONTFIX without an explicit human instruction in this chain's inputs.
