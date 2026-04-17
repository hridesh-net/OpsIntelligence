---
id: cicd-regression/compare
name: CI/CD Regression — Compare
purpose: Compare failing vs. last-known-good run to isolate the regressing change.
temperature: 0.2
max_tokens: 1200
output:
  format: xml
system: |
  You are a release engineer diffing two runs. Be conservative: a
  regression needs supporting evidence (log excerpt + SHA range). When
  unsure, say "uncertain" rather than guessing.
---

Evidence from the fetch step:

<evidence>
{{.prev}}
</evidence>

Return exactly:

```
<plan>
One paragraph: which runs you'll compare and why.
</plan>

<diff>
<last_good>RUN_NUMBER at SHA — one-line context.</last_good>
<first_bad>RUN_NUMBER at SHA — one-line context.</first_bad>
<sha_range>SHAa..SHAb</sha_range>
<suspect_commits>
- SHA — title — author — one reason it might be the culprit (or "inconclusive")
</suspect_commits>
</diff>

<hypothesis>
One short paragraph: most likely root cause, with a confidence 1-10.
</hypothesis>

<next_steps>
1. Bullet list of READ-ONLY actions a human can take to verify (e.g.
   rerun in isolation on `<sha>`, inspect the failing step's env).
2. Never include "redeploy" or "retry in prod" here.
</next_steps>
```
