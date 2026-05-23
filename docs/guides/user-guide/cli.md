# CLI Reference

Every `opsintelligence` command with examples.

---

## `serve`

Start the HTTP/WebSocket gateway and all background services.

```bash
opsintelligence serve
```

Options:

- `--config FILE` — use a different config file (default: `./opsintelligence.yaml`)
- `--port PORT` — override the gateway port from config

The server initializes in this order:

1. Load configuration and secrets
2. Connect datastores (SQLite or Postgres)
3. Start Redis client if enabled
4. Start channels (Slack, Teams, generic webhooks)
5. Start background workers (TaskManager, Indexing, GitHub sync)
6. Mount HTTP handlers and WebSocket hub
7. Serve the dashboard SPA at `/dashboard/app`

---

## `run`

Execute a single task synchronously.

```bash
opsintelligence run "Review PR #123 in opsintelligence"
opsintelligence run --agent "sonar-review" --repo "my-org/api" \
  "Check test coverage after the latest push"
```

The command blocks until the agent chain finishes, printing every event to stderr. Exit code is 0 on success, 1 on failure.

---

## `run-async`

Submit a task to the background queue and return immediately.

```bash
opsintelligence run-async "Deploy staging cluster"
# → Task ID: task_abc123

opsintelligence tasks tail task_abc123
```

Use `tasks tail` to follow live progress. Async tasks survive the CLI process exiting.

---

## `repos`

Manage repository indexing and LocalIntel.

```bash
# List all indexed repos
opsintelligence repos list

# Sync a specific repo (pull latest, reindex)
opsintelligence repos sync owner/repo

# Show repo details and last scan
opsintelligence repos show owner/repo

# Trigger full re-index
opsintelligence repos reindex owner/repo
```

---

## `doctor`

Health check — verifies every integration the system knows about.

```bash
opsintelligence doctor
```

Checks:

- Config file is valid YAML
- Datastore connectivity
- Redis connectivity (if enabled)
- LLM provider health (one ping per configured provider)
- GitHub / GitLab / Jenkins / SonarQube connectivity
- Slack / Teams connectivity
- Webhook HMAC key rotation age

---

## `providers`

List, test, and rotate LLM provider credentials.

```bash
opsintelligence providers list
opsintelligence providers health
opsintelligence providers rotate openai
```

---

## Environment variables

| Variable | Purpose |
|----------|---------|
| `OPSINTELLIGENCE_CONFIG` | Path to config file |
| `OPSINTELLIGENCE_LOG_LEVEL` | `debug`, `info`, `warn`, `error` |
| `OPSINTELLIGENCE_NO_COLOR` | Disable colored output |
