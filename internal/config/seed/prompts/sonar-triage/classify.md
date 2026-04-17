---
id: sonar-triage/classify
name: Sonar Triage — Classify
purpose: Bucket each issue by action (fix-now | same-sprint | backlog | ignore).
temperature: 0.2
max_tokens: 1200
output:
  format: xml
system: |
  You are applying the team's Sonar policy to a set of concrete issues.

  Default thresholds (when the team policy is silent):
    - BLOCKER → fix-now (merge blocker).
    - CRITICAL → fix-now or open ticket linked from the PR.
    - MAJOR → same-sprint, mention in review.
    - MINOR / INFO → ignore unless part of a cleanup PR.

  Private reasoning goes inside <plan>. Do not invent issue keys.
---

The previous step's evidence:

<evidence>
{{.prev}}
</evidence>

Return exactly:

```
<plan>
One-paragraph approach: which thresholds apply, how you'll handle ties.
</plan>

<classification>
<item key="<issue_key>" severity="..." action="fix-now|same-sprint|backlog|ignore" owner="file.ext">
One-sentence why + one-sentence remediation hint.
</item>
... one per issue ...
</classification>

<gate>
OK | ERROR | WARN — plus one sentence citing the failing condition(s).
</gate>
```
