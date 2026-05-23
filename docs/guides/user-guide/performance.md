# Performance Tuning

## Memory and context

| Knob | Default | Effect |
|------|---------|--------|
| `agent.max_iterations` | 10 | Hard cap on agent loop rounds. Lower = faster, higher = more thorough. |
| `agent.context_window` | 128k | Max tokens per conversation. Reduce for speed, increase for complex tasks. |
| `agent.tool_timeout` | 30s | How long to wait for a tool call. Increase for slow CI pipelines. |

## Concurrency

The system runs up to 4 tool calls in parallel per agent. If you need more:

```yaml
agent:
  max_parallel_tools: 8
```

Be mindful of rate limits — OpenAI and Anthropic enforce TPM/RPM caps.

## Cost reduction

1. **Use smaller models for simple tasks** — GPT-4o-mini or Gemini Flash for summarization
2. **Enable response caching** — Redis caches identical prompts for 1 hour by default
3. **Shorten context** — trim old messages when the window fills
4. **Batch async tasks** — `run-async` queues work instead of blocking expensive synchronous calls

## Redis

If you run multiple instances:

```yaml
redis:
  enabled: true
  addr: "localhost:6379"
  db: 0
```

Redis provides:

- Cross-instance task state (see Tasks from any replica)
- Distributed cron locks (prevents duplicate scheduled jobs)
- Pub/sub for real-time dashboard updates
- Response caching between instances
