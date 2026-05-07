# Configuration

## State directory

Configuration and runtime data live under **`state_dir`** (default `~/.opsintelligence`, overridable with **`OPSINTELLIGENCE_STATE_DIR`**). Paths in YAML are usually resolved relative to this directory unless absolute.

Canonical layout is defined in [`internal/dirs/layout.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/dirs/layout.go) (`dirs.Layout`). Defaults and merges are applied in [`internal/config/config.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/config/config.go). When YAML comments disagree with code, prefer the implementation.

## Onboarding and templates

```bash
opsintelligence onboard    # interactive setup → ~/.opsintelligence/opsintelligence.yaml
opsintelligence init       # seed teams/example-team-style templates into state
```

Onboarding collects LLM provider keys, optional Slack tokens, optional GitHub / GitLab / Jenkins / Sonar tokens, and the active team name. Advanced options (memory, MCP, cron, webhooks, **repo_intel**) are edited in YAML or the dashboard.

## Team policies

A **team** is a folder of Markdown files merged into the agent system prompt at startup, typically:

```
~/.opsintelligence/teams/<team>/
├── README.md
├── pr-review.md
├── sonar.md
├── cicd.md
├── secrets-and-safety.md
└── runbooks/
```

Start from [`teams/example-team/`](https://github.com/hridesh-net/OpsIntelligence/tree/main/teams/example-team) in the repository; copy, rename, and edit.

## Secrets

Integration tokens are **never** stored inline in committed YAML. Reference environment variables from config (`token_env:`). Run **`opsintelligence doctor`** before **`start`** to verify referenced variables exist.

## Reference config

The fully commented reference file is [`.opsintelligence.yaml.example`](https://github.com/hridesh-net/OpsIntelligence/blob/main/.opsintelligence.yaml.example) in the repository root.

## Dashboard settings

With the gateway running, Settings covers gateway bind/TLS, auth/OIDC, datastore, LLM providers, MCP, channels, webhooks, agent guardrails, and Repo Intelligence (index limits, call-graph policy, embeddings). Edits use **`If-Match`** optimistic concurrency.

Default dashboard URL when bound locally:

```
http://127.0.0.1:18790/dashboard/
```

Related API notes for operators: [`doc/users-apikeys-api.md`](https://github.com/hridesh-net/OpsIntelligence/blob/main/doc/users-apikeys-api.md).

## CLI quick reference

```bash
opsintelligence doctor --config .opsintelligence.yaml.example --skip-network   # CI-style check
```

See also [Install](install.md) for environment variables that affect installation and bootstrap behavior.
