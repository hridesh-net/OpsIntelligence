# Troubleshooting

## Common issues

### 403 Forbidden on all API calls

**Cause:** Missing or expired API key; auth middleware rejecting requests.

**Fix:**

```bash
opsintelligence doctor
# Check the auth section — is OIDC misconfigured?
# If using local auth, verify the session cookie hasn't expired.
```

### Empty run trace

**Cause:** Agent hasn't emitted any events yet, or the trace filter is too restrictive.

**Fix:**

1. Check the "Kind" filter — try "All"
2. Verify the agent is actually running (`opsintelligence tasks list`)
3. Check server logs for panics

### Provider timeout

**Cause:** LLM API is slow or rate-limiting.

**Fix:**

```bash
opsintelligence providers health
# If a provider shows degraded, check your rate limits
# Increase timeout in config: agent.tool_timeout: 60
```

### Dashboard shows "Redis: Disabled" but I configured it

**Cause:** Redis connection failed at startup; the system fell back to in-memory mode.

**Fix:**

```bash
opsintelligence doctor
# Look for Redis connectivity errors
# Verify the addr and password in opsintelligence.yaml
```

### High memory usage

**Cause:** Large repos being indexed, or too many concurrent agent runs.

**Fix:**

1. Limit indexing parallelism: `repo_intel.max_concurrent_index: 2`
2. Reduce agent context window: `agent.context_window: 64000`
3. Enable response caching with Redis

### Tasks disappear after restart

**Cause:** Tasks are in-memory only unless Redis is enabled.

**Fix:** Enable Redis for durable task state, or rely on Run Trace for persistent history.

### GitHub webhooks not firing

**Cause:** Webhook secret mismatch, or the endpoint isn't reachable.

**Fix:**

1. Verify the webhook URL is correct and reachable from GitHub
2. Check the HMAC secret matches `devops.github.webhook_secret`
3. Look at server logs for `webhook: invalid signature` errors

### Slow PR reviews

**Cause:** Large diffs, or the model is processing too much context.

**Fix:**

1. Split large PRs into smaller chunks
2. Use `--model gpt-4o-mini` for faster (if less nuanced) reviews
3. Enable response caching for repeated code patterns
