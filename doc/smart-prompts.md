# Smart Prompts & Prompt Chaining

OpsIntelligence ships a small library of curated, disk-editable
**smart prompts** and a runtime for executing them as ordered **chains**.
Chains are how the agent gets good at hard DevOps tasks — PR review,
Sonar triage, CI/CD regression triage, incident scribing — without
blowing the whole task into one giant inline prompt.

## Why chains?

Short version: the best public system prompts from modern coding agents
(Cursor, Claude Code, Devin, Junie, Windsurf, Augment, Warp, etc.) all
converge on the same techniques. OpsIntelligence borrows those patterns
and makes them first-class:

| Technique                          | Where it shows up here                       |
| ---------------------------------- | -------------------------------------------- |
| Structured reasoning phases        | XML tags (`<plan>`, `<findings>`, `<critique>`) in each step. |
| Explicit self-critique pass        | `pr-review/critique`, `meta/self-critique`.  |
| Evidence-first rendering           | Every render step cites a URL / SHA / issue. |
| Budget discipline                  | Chains are bounded (≤8 steps, no loops).     |
| Specialist prompts per task        | Four named DevOps chains + 3 meta prompts.   |
| Disk-editable prompt library       | `~/.opsintelligence/prompts/*.md` overrides. |

Crucially, the prompts themselves are written from scratch — we only
borrow the *shape* of what proven agents do. No GPL source material was
copied into this repository.

## Anatomy of a smart prompt

Each SmartPrompt is a Markdown file with YAML frontmatter:

```markdown
---
id: pr-review/gather
name: PR Review — Evidence Gather
purpose: Collect the raw facts needed to review a pull/merge request.
temperature: 0.1
max_tokens: 1200
output:
  format: json
system: |
  You are an evidence collector. Return ONLY JSON...
---

Collect the evidence needed to review {{.pr_url}}.
...
```

- Everything under `---` lines is YAML frontmatter (schema lives in
  `internal/prompts/types.go` → `SmartPrompt`).
- The body is a Go `text/template`. `{{.key}}` substitutions use the
  caller's inputs plus an auto-injected `{{.prev}}` (the previous step's
  output when run as part of a chain).

## Chain definition

Chains are YAML files under `prompts/chains/`:

```yaml
id: pr-review
name: Pull Request Review
purpose: End-to-end PR/MR review (gather → analyze → critique → render).
inputs:
  - pr_url
tags: [devops, github, gitlab]
steps:
  - pr-review/gather
  - pr-review/analyze
  - pr-review/critique
  - pr-review/render
```

At runtime each step's output flows into the next as `{{.prev}}`. The
runner never loops; if you want iterative refinement, model it as
distinct steps.

## Built-in library

| Chain / Prompt               | What it does                                                    |
| ---------------------------- | --------------------------------------------------------------- |
| `chain:pr-review`            | PR/MR review: evidence → analyse → critique → render Ship/Hold. |
| `chain:sonar-triage`         | Sonar quality gate: fetch → classify by severity → recommend.   |
| `chain:cicd-regression`      | CI/CD regression: fetch runs → diff failing vs. last-good → report. |
| `chain:incident-scribe`      | Incident: structured brief + Slack update + postmortem skeleton.|
| `prompt:meta/self-critique`  | Reflect on a draft; flag missing evidence and speculative claims. |
| `prompt:meta/evidence-extractor` | Pull URLs/SHAs/IDs out of messy human input (redacts secrets). |
| `prompt:meta/plan-then-act`  | Draft a bounded plan before the agent touches any tool.         |

Run `opsintelligence prompts ls` to see what's actually loaded (the CLI
is authoritative — it reflects any operator overrides).

## Where prompts live

There are two sources; disk wins:

1. **Embedded** (inside the compiled binary) — the set shipped with this
   release under `internal/config/seed/prompts/`.
2. **Disk** — `~/.opsintelligence/prompts/` (or wherever `state_dir`
   points). The first `opsintelligence init` / first boot seeds the
   embedded defaults here; your edits are never overwritten on
   re-initialisation.

To override the shipped `pr-review/render` step for one team:

```bash
$EDITOR ~/.opsintelligence/prompts/pr-review/render.md
```

Run `opsintelligence prompts show pr-review/render` to confirm the
`source_path` now points at your edited file instead of `embedded:...`.

## CLI

```
opsintelligence prompts ls                          # list everything
opsintelligence prompts ls --tag devops             # filter by tag
opsintelligence prompts show pr-review              # chain spec + resolved steps
opsintelligence prompts show pr-review/gather       # single prompt spec + body
opsintelligence prompts run pr-review \
  --input pr_url=https://github.com/acme/api/pull/42
opsintelligence prompts run pr-review \
  --inputs-file ./pr.json --no-trace --output review.md
opsintelligence prompts run meta/self-critique \
  --kind prompt --input draft="$(cat draft.md)"
```

`opsintelligence prompts run` uses the default model from
`routing.default` in your config. Each step's latency and token usage is
reported in the trace.

## Agent-facing surface

Inside the agent loop the library is exposed via two tools:

- `chain_run` — execute a named chain or single prompt.
- `chain_list` — list chains/prompts at runtime (useful if the system
  prompt's Smart Prompts Index is stale because a new prompt was dropped
  on disk since boot).

The system prompt is automatically appended with a **Smart Prompts
Index**, so the model knows what ids exist without having to call
`chain_list` first:

```
Smart Prompts:
  - chain:cicd-regression    Isolate a regressing change in a CI/CD pipeline …
  - chain:incident-scribe    Incident brief + status update + postmortem skeleton.
  - chain:pr-review          End-to-end PR/MR review (gather → analyze → critique → render).
  - chain:sonar-triage       Triage SonarQube findings against team thresholds …
  - prompt:meta/evidence-extractor  Pull structured evidence …
  - prompt:meta/plan-then-act       Draft an explicit plan before touching any tool …
  - prompt:meta/self-critique       Reflect on a draft answer …
```

The agent also picks chains up from the DevOps skill graph — each
skill node (`skills/devops/pr-review.md`, `.../sonar.md`, etc.) now
starts with a **Fast path** hint that invokes the matching chain.

## Authoring guidelines

When you write a new SmartPrompt for your team:

1. **Name it tightly.** `<family>/<stage>` keeps chains readable.
2. **Keep system blocks short.** A paragraph of rules, not an essay.
3. **Use XML scaffolds for private reasoning.** Wrap with
   `<plan>`, `<critique>`, `<scratchpad>`. Downstream steps are
   instructed to strip those before rendering to the user.
4. **Declare an output format.** Even when advisory, it surfaces
   violations the next step can correct.
5. **Never paste secrets, tokens, cookies, or raw logs.** Redact.
   Summarize. This applies recursively — `chain_run` doesn't make
   the safety posture go away.
6. **No write actions.** Chains may SUGGEST retries, merges, or
   deploys; they must not invoke tools that perform them. That gate
   belongs to the main agent loop, in a turn where a human said yes.

## Tests

- Library integrity: `go test ./internal/prompts/...`
- Embedded seed + operator-override protection: `go test ./internal/config/...`
- End-to-end smoke: `opsintelligence prompts ls` and `opsintelligence prompts show pr-review`.
