# Getting Started

5-minute bootstrap checklist for new operators.

## 1. Bootstrap the owner account

Start the server:

```bash
opsintelligence serve
```

Open `http://localhost:8080/dashboard/app` and create the first owner account. This account has full permissions and can invite other users.

Alternatively, use the CLI:

```bash
opsintelligence bootstrap --email admin@example.com --password "$(openssl rand -base64 24)"
```

## 2. Configure your first LLM provider

Edit `opsintelligence.yaml`:

```yaml
providers:
  openai:
    enabled: true
    api_key: "${OPENAI_API_KEY}"
    default_model: "gpt-4o"
```

Or use Anthropic, Google Vertex, Ollama, or OpenRouter. See [Providers](guides/user-guide/providers.md) for all options.

Verify connectivity:

```bash
opsintelligence providers health
```

## 3. Enable DevOps integrations

### GitHub

```yaml
devops:
  github:
    enabled: true
    token: "${GITHUB_PAT}"
    webhook_secret: "$(openssl rand -hex 32)"
```

Add the webhook URL to your repo settings: `https://your-host/webhooks/github`.

### Slack (optional)

```yaml
channels:
  slack:
    enabled: true
    bot_token: "${SLACK_BOT_TOKEN}"
    app_token: "${SLACK_APP_TOKEN}"
```

## 4. Add a repository for indexing

```bash
opsintelligence repos sync owner/repo
```

This clones, scans, and indexes the repo for LocalIntel (code search, call graphs, embeddings).

## 5. Verify health

```bash
opsintelligence doctor
```

You should see all checks green: config, datastore, providers, DevOps integrations.

## Next steps

- Read the [User guide](guides/user-guide/index.md) for daily operations
- Explore [Architecture](architecture/overview.md) to understand how it works
- Learn about [Multi-agent systems in Go](guides/multi-agent-go/index.md) to extend the platform
