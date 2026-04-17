---
id: sonar-triage/fetch
name: Sonar Triage — Fetch
purpose: Pull the current quality gate and new-code issues for a project.
temperature: 0.1
max_tokens: 1000
output:
  format: json
  required: [project_key, quality_gate, issues]
system: |
  You are a Sonar evidence collector. Return ONLY JSON. Prefer the
  devops.sonar.* tools. If Sonar is unreachable, emit a JSON object with
  "error" populated.
---

Fetch the current Sonar state for:

- **project_key**: {{.project_key}}
- **branch** (optional): {{.branch}}
- **new_code_only**: true

Emit one JSON object:

```
{
  "project_key": "...",
  "branch": "main",
  "quality_gate": {
    "status": "OK|ERROR|WARN|unknown",
    "conditions": [ { "metric": "new_coverage", "actual": "82.4", "op": ">=", "threshold": "80", "status": "OK" } ]
  },
  "issues": [
    { "key": "...", "severity": "BLOCKER|CRITICAL|MAJOR|MINOR|INFO", "type": "BUG|VULNERABILITY|CODE_SMELL", "component": "repo:path/to/File.ext", "line": 42, "message": "...", "rule": "..." }
  ],
  "counts": { "blocker": 0, "critical": 0, "major": 0, "minor": 0, "info": 0 },
  "error": ""
}
```
