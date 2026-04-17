# CI/CD policy — example team

## Required pipelines
- **pull_request**: lint, unit, integration, security-scan.
- **main**: the above, plus `build-artifacts`, `deploy-staging`, and `e2e-smoke`.
- **tag (vX.Y.Z)**: `deploy-prod` with manual approval gate.

## Flaky-test policy
- A test that fails ≥ 2 times in 7 days is quarantined: add `@flaky` label and open a ticket owned by the test's author.
- The agent may link recent failures but must **not** quarantine tests or retry jobs automatically — that is a human decision.

## Rollback playbook
1. Page on-call via `#oncall-ops`.
2. Roll forward by default — only roll back when the failure impacts revenue or PII.
3. To roll back: redeploy the previous tag from `deploy-prod` (manual button in Jenkins / GitLab / Actions).
4. Once stable, open an incident ticket and fill `incidents.md` template.

## Agent behavior
- When asked "is the main branch green?", the agent must read `devops.github.workflow_runs` or `devops.gitlab.pipelines` and report the latest five runs, not just the last one.
- The agent must **never** cancel or retrigger production pipelines without explicit human approval in the same turn.
