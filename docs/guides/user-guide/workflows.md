# Workflows

Common operational patterns you can run with OpsIntelligence.

## PR review pipeline

**What it does:**

1. Fetches PR diff, description, and linked issues
2. Analyzes code for bugs, security, style, and test coverage
3. Posts a structured review comment with inline suggestions
4. Updates SonarQube quality gate status

**How to run:**

```bash
opsintelligence run --agent "pr-review" \
  "Review PR #123 in my-org/api"
```

Or configure a GitHub webhook to trigger automatically on PR open/update.

## Incident response

**What it does:**

1. Receives alert (PagerDuty, Slack, webhook)
2. Classifies severity and impact radius
3. Queries logs, metrics, and recent deploys
4. Suggests root cause and remediation steps
5. Opens a GitHub issue with full timeline

**How to run:**

```bash
opsintelligence run-async --agent "incident-response" \
  "Investigate high CPU alert on prod-web-3"
```

## Autonomous maintenance

**What it does:**

1. Scans repos for stale branches, outdated dependencies, security advisories
2. Generates fix PRs with clear descriptions
3. Runs tests and validates changes
4. Submits PRs for human approval

**How to run:**

```bash
# Weekly cron job
opsintelligence run-async --agent "maintenance" \
  "Check all repos for stale branches and outdated deps"
```

## Codebase Q&A

**What it does:**

1. Indexes the repo (AST, call graph, embeddings)
2. Accepts natural language queries
3. Returns precise answers with file references and code snippets

**How to run:**

```bash
opsintelligence run --agent "codebase-qa" \
  "Where is the auth middleware defined and what does it check?"
```
