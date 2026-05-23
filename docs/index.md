# OpsIntelligence documentation

OpsIntelligence is an autonomous agent for **in-house DevOps**: PR/MR review, CI signals, Sonar triage, incidents, and runbooks — with your team policies wired in. Integrations stay **read-first** until an operator confirms writes in-turn.

Use this site as the structured guide; the repository [README](https://github.com/hridesh-net/OpsIntelligence/blob/main/README.md) stays a concise landing page with badges and quick facts.

## Topic map

| Section | What you will find |
| -------- | ------------------ |
| [Getting started](getting-started.md) | 5-minute bootstrap checklist |
| [Install](install.md) | Release installer, pinned versions, build-from-source, environment toggles, uninstall |
| [Configuration](configuration.md) | State directory, onboarding, YAML pointers, example config reference |
| [User guide](guides/user-guide/index.md) | Dashboard walkthrough, CLI cheat sheet, workflow patterns, troubleshooting |
| [Architecture](architecture/overview.md) | Request flow, major packages, resource usage — internals for contributors and operators |
| [Agents & tools](agents-tools.md) | Runner loop, skills, smart prompts / chains, specialist routing |
| [Multi-agent systems in Go](guides/multi-agent-go/index.md) | Concepts, patterns, and production practices for building autonomous multi-agent apps |
| [Repo intelligence](repo-intelligence.md) | Optional indexing, call graph, hybrid search, CLI and dashboard |
| [Observability](observability.md) | Run traces, OpenTelemetry, logs |
| [Security](security.md) | Tokens, guardrails, audit logging |
| [Contributing](contributing.md) | Build, test, docs workflow |
| [Changelog](changelog.md) | Pointer to release history |

## Visual architecture

Runtime diagrams ship under **`docs/`** (MkDocs copies them to the site):

- Editable source and PNG: [`docs/architecture/diagrams/opsintelligence-architecture.drawio`](https://github.com/hridesh-net/OpsIntelligence/blob/main/docs/architecture/diagrams/opsintelligence-architecture.drawio) · [`opsintelligence-architecture.png`](https://github.com/hridesh-net/OpsIntelligence/blob/main/docs/architecture/diagrams/opsintelligence-architecture.png)
- Multi-tab detail: [`architecture-overview.drawio`](https://github.com/hridesh-net/OpsIntelligence/blob/main/architecture-overview.drawio) at repository root

Corresponding narrative: [Architecture overview](architecture/overview.md).

## Further reading in-repo

Deep dives that remain outside this MkDocs tree include [`doc/smart-prompts.md`](https://github.com/hridesh-net/OpsIntelligence/blob/main/doc/smart-prompts.md) (prompt chaining internals), narrative/extra detail under [`doc/deep-dives/`](https://github.com/hridesh-net/OpsIntelligence/tree/main/doc/deep-dives), and runbooks under [`doc/runbooks/`](https://github.com/hridesh-net/OpsIntelligence/tree/main/doc/runbooks).
