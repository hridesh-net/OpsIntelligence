# Getting Started

Welcome to OpsIntelligence — an autonomous DevOps agent that runs on your infrastructure and works with your team policies.

## What is OpsIntelligence?

OpsIntelligence is a **multi-agent autonomous system** written in Go that handles DevOps workflows: PR review, CI signal triage, security scanning, incident response, and runbook execution. It integrates with GitHub, GitLab, Jenkins, SonarQube, Slack, and more — staying **read-first** until a human confirms destructive actions.

## Who is this for?

| Persona | How you use it |
|---------|---------------|
| **Platform Engineer** | Configure providers, set up webhooks, tune performance |
| **SRE / DevOps** | Let it triage alerts, draft incident responses, query logs |
| **Developer** | Get PR reviews, ask questions about your codebase |
| **Team Lead** | Monitor agent activity via run traces and dashboards |

## Architecture at a glance

```
┌─────────────┐     ┌──────────────┐     ┌─────────────┐
│   Gateway   │────▶│ Agent Runner │────▶│   Tools     │
│ (HTTP + WS) │     │ (LLM loop)   │     │ (GitHub,    │
└─────────────┘     └──────────────┘     │  CI, scans) │
       │                   │              └─────────────┘
       ▼                   ▼
┌─────────────┐     ┌──────────────┐
│  Dashboard  │     │   Memory     │
│  (admin UI) │     │ (3-tier)     │
└─────────────┘     └──────────────┘
```

## Next steps

1. **[Install](installation.md)** — binary release or build from source
2. **[Quickstart](quickstart.md)** — first agent run in 5 minutes
3. **[Configuration](../configuration.md)** — providers, webhooks, auth
4. **[User Guide](../guides/user-guide/)** — dashboard, CLI, workflows
