# LLM Providers

Configure one or more providers. OpsIntelligence routes requests to the best available model per task.

## Cloud providers

### OpenAI

```yaml
providers:
  openai:
    enabled: true
    api_key: "${OPENAI_API_KEY}"
    default_model: "gpt-4o"
    models:
      - id: "gpt-4o"
        max_tokens: 4096
        temperature: 0.2
      - id: "gpt-4o-mini"
        max_tokens: 4096
        temperature: 0.3
```

**Best for:** General reasoning, structured tool calls, coding.

### Anthropic

```yaml
providers:
  anthropic:
    enabled: true
    api_key: "${ANTHROPIC_API_KEY}"
    default_model: "claude-sonnet-4-20250514"
    models:
      - id: "claude-sonnet-4-20250514"
        max_tokens: 8192
        temperature: 0.2
```

**Best for:** Long context (200k tokens), nuanced reasoning, safety-critical tasks.

### Google Vertex AI (Gemini)

```yaml
providers:
  vertex:
    enabled: true
    project_id: "my-gcp-project"
    location: "us-central1"
    credentials: "/etc/opsintelligence/gcp-sa.json"
    default_model: "gemini-2.5-flash"
    models:
      - id: "gemini-2.5-flash"
        max_tokens: 8192
        temperature: 0.2
```

**Best for:** GCP-native deployments, cost-sensitive workloads, multimodal input.

## Local / self-hosted

### Ollama

```yaml
providers:
  ollama:
    enabled: true
    base_url: "http://localhost:11434"
    default_model: "llama3.1:8b"
```

**Best for:** Air-gapped environments, zero API costs, fast iteration.

### OpenRouter

```yaml
providers:
  openrouter:
    enabled: true
    api_key: "${OPENROUTER_API_KEY}"
    default_model: "anthropic/claude-sonnet-4"
```

**Best for:** Unified access to 100+ models with a single API key.

## Model selection

The agent framework picks the model based on task characteristics:

| Task type | Preferred model | Why |
|-----------|-----------------|-----|
| Code review | Claude Sonnet | Long context, nuanced critique |
| Incident response | GPT-4o | Fast structured tool calls |
| Summarization | Gemini Flash | Low cost, high throughput |
| Sensitive ops | Claude Sonnet | Strong safety alignment |
| Air-gapped | Ollama Llama 3 | Local, no data leaves network |

You can override per-task via `--model` or the dashboard.
