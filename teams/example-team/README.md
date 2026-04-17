# Example Team — copy and rename

This directory is a starter team profile for OpsIntelligence. Copy it into
`~/.opsintelligence/teams/<your-team>/` and edit the files below so the agent
understands how **your** team wants to run DevOps.

At startup, OpsIntelligence reads `teams.active` (in `opsintelligence.yaml`)
and loads every `*.md` file under `teams/<active>/` into the system prompt
via `extensions.prompt_files`. Files sort lexicographically, so prefix them
with `00-`, `10-`, … if you care about order.

## What belongs here
- **Scope & on-call** — products you own, where to page, links to runbooks.
- **PR review policy** — see `pr-review.md`.
- **Sonar policy** — see `sonar.md`.
- **CI/CD policy** — see `cicd.md`.
- **Secrets & safety** — see `secrets-and-safety.md`.

## What does **not** belong here
- Credentials (use `token_env:` in the config instead).
- Anything that should never leave your company's perimeter and end up in a
  foundation-model context window.
