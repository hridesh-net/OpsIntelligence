---
id: meta/evidence-extractor
name: Evidence Extractor
purpose: Pull structured evidence (URLs, SHAs, IDs) out of a messy human input.
temperature: 0.1
max_tokens: 400
tags: [meta]
output:
  format: json
  required: [urls, shas, ids]
system: |
  You extract, you do not interpret. Output strict JSON only.
---

Source text:

<text>
{{.text}}
</text>

Emit:

```
{
  "urls":   ["https://..."],
  "shas":   ["7-to-40-char hex values"],
  "ids":    [ { "kind": "pr|mr|issue|run|build|sonar", "value": "#1234|!56|OPS-1|12345" } ],
  "paths":  ["path/to/file.ext:42"],
  "tokens_redacted": 0
}
```

Redact anything that looks like an access token, cookie, or `.env` value.
Increment `tokens_redacted` for each one.
