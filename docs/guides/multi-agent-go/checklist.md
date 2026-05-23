# Design Checklist

Before shipping a multi-agent system, verify each item.

## Observability

- [ ] Every agent decision is logged with timestamp, stream, and payload
- [ ] Tool calls include input arguments and output results
- [ ] Errors include stack traces or context
- [ ] Dashboard shows live status and historical traces
- [ ] Alerts fire when error rate exceeds threshold

## Limits

- [ ] Hard iteration cap (e.g., 10-20 rounds)
- [ ] Token budget per conversation
- [ ] Tool timeout (e.g., 30s)
- [ ] Rate limits per user and per provider
- [ ] Circuit breakers for failing external APIs

## Tool safety

- [ ] Tool schemas are validated before execution
- [ ] Untrusted tools run sandboxed (containers, seccomp)
- [ ] Destructive operations require human approval
- [ ] Tool output is sanitized before re-entering the prompt
- [ ] No tool can exfiltrate secrets or credentials

## Concurrency

- [ ] Parallel tool execution with bounded concurrency
- [ ] Context cancellation propagates to all goroutines
- [ ] No data races on shared state
- [ ] Graceful shutdown waits for in-flight tasks

## Memory

- [ ] Working memory is bounded (drop oldest messages)
- [ ] Episodic memory persists across restarts
- [ ] Semantic memory is indexed and searchable
- [ ] Sensitive data is not logged or cached

## Recovery

- [ ] Failed tasks can be retried with exponential backoff
- [ ] Partial results are saved and resumable
- [ ] Agent state can be checkpointed and restored
- [ ] System degrades gracefully when LLM is unavailable

## AuthZ

- [ ] Every API endpoint checks user permissions
- [ ] API keys are scoped and rotatable
- [ ] Session tokens expire and are revocable
- [ ] Audit log is tamper-evident

## Cost

- [ ] Smaller models are used for simple tasks
- [ ] Response caching reduces duplicate API calls
- [ ] Token usage is tracked per user and per task
- [ ] Budget alerts fire before overspend
