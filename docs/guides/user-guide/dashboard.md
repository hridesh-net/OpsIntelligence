# Dashboard

The dashboard is a single-page app at `/dashboard/app`. Sign in with your owner or user credentials.

## Overview

The landing page shows live system status cards:

| Card | Indicator | What it means |
|------|-----------|---------------|
| **Gateway** | Green pulse | HTTP/WebSocket server is healthy |
| **Active Tasks** | Number + pulse | Sub-agent runs currently in-flight |
| **Redis** | Enabled / Disabled | Cross-instance caching and pub/sub state |
| **Version** | Build ID | Current OpsIntelligence release |

Cards refresh automatically every 10 seconds.

## Run Trace

The flight recorder for every agent decision.

**What you see:**

- **Timestamp** — when the event occurred
- **Stream** — `master` (main agent) or `subagent:<name>` (delegation)
- **Kind** — `task_start`, `model_iteration`, `tool_call`, `tool_done`, `chain_run_complete`, `log`
- **Message** — human-readable summary
- **JSON** — expand any row for the full payload

**Filters:**

- **NDJSON source** — All streams (merged), Master only, or Sub-agent only
- **Stream / role** — filter by agent name or role
- **Kind** — `task_start`, `tool_done`, etc.
- **Contains** — free-text search across the JSON payload

**Pro tip:** When debugging an agent, search for `ok: false` in the "Contains" filter to find failed tool calls immediately.

## Tasks

Live async runs from `TaskManager`.

| Column | Meaning |
|--------|---------|
| Task ID | Unique identifier |
| Agent | Which sub-agent or specialist ran |
| Status | `pending` → `running` → `completed` / `failed` / `cancelled` |
| Elapsed | Wall-clock time |
| Prompt | First 80 chars of the task |
| Last event | Most recent progress update |
| #Evts | Total events emitted |

Tasks are in-memory only — they clear on process restart. For durable history, use **Run trace**.

## Users & Roles

| Role | Permissions |
|------|-------------|
| **Owner** | Full access. Created at bootstrap. Can invite users. |
| **Admin** | Manage users, roles, API keys, settings |
| **Operator** | Read users, manage own API keys |
| **Developer** | Manage own API keys |
| **Auditor** | Read-only access to users and all API keys |
| **Viewer** | Minimal read access |

The server enforces permissions authoritatively. The dashboard hides buttons you can't use, but the backend gates every request.

## Settings

Every form writes directly to `opsintelligence.yaml` via `configsvc`. Changes are atomic (If-Match optimistic concurrency) and immediate — no restart required for most values.

| Section | Configure |
|---------|-----------|
| **Gateway** | Host, port, TLS, Tailscale bind mode |
| **Authentication** | Local login, OIDC, API keys, session TTL, CSRF |
| **Datastore** | SQLite vs. Postgres, connection pooling |
| **LLM providers** | Cloud and local model credentials |
| **MCP** | Model Context Protocol servers and clients |
| **Webhooks** | GitHub adapter, generic webhooks, HMAC secrets |
| **Channels** | Slack, Teams, outbound reliability |
| **DevOps** | GitHub, GitLab, Jenkins, SonarQube tokens |
| **Agent** | Iteration cap, planning, reflection, LocalIntel |
| **Repo Intel** | Indexing, scanning, call graph generation |

## Keyboard shortcuts

Press `?` anywhere in the dashboard for a shortcuts reference.

| Key | Action |
|-----|--------|
| `/` | Open command palette |
| `?` | Show keyboard shortcuts help |
| `g o` | Go to Overview |
| `g t` | Go to Tasks |
| `g u` | Go to Users |
| `g a` | Go to API keys |
| `g r` | Go to Repo Intel |
| `g s` | Go to Settings |
| `g l` | Go to Run trace |
| `Esc` | Close modal / palette |
