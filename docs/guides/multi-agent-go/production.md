# Production Practices

Lessons from running OpsIntelligence in production.

## Parallel tool execution

When a model requests multiple tools at once, run them concurrently:

```go
var wg sync.WaitGroup
results := make([]ToolResult, len(toolCalls))
sem := make(chan struct{}, 4) // max 4 concurrent

for i, tc := range toolCalls {
	wg.Add(1)
	sem <- struct{}{}
	go func(idx int, call ToolCall) {
		defer wg.Done()
		defer func() { <-sem }()
		results[idx] = execute(call)
	}(i, tc)
}
wg.Wait()
```

Why 4? Most cloud LLM providers rate-limit at ~4-8 concurrent requests. Tune for your infrastructure.

## Redis bridge

For multi-instance deployments, use Redis for:

- **Task state** — any instance can see active tasks
- **Pub/sub** — real-time dashboard updates across replicas
- **Distributed locks** — cron jobs run exactly once
- **Response cache** — identical prompts hit cache instead of the API

## Palace routing

OpsIntelligence uses a "Palace" pattern: a central router (the Palace) dispatches to specialist agents (the Rooms). Each room is a self-contained sub-agent with its own tools and memory.

```go
type Palace struct {
	Rooms map[string]*Agent
}

func (p *Palace) Route(ctx context.Context, prompt string) (string, error) {
	room := p.classify(prompt) // "incident", "pr-review", etc.
	return p.Rooms[room].Run(ctx, prompt)
}
```

## Safety

- **Tool sandboxing** — run untrusted tools in separate processes or containers
- **Output validation** — never pass tool output directly to shell commands
- **Human approval** — require confirmation for destructive operations
- **Rate limiting** — cap requests per user per minute
- **Audit logging** — every tool call and model decision is logged immutably

## Observability

Emit structured events at every step:

```go
type AgentEvent struct {
	Timestamp time.Time `json:"ts"`
	Stream    string    `json:"stream"` // "master", "subagent:X"
	Kind      string    `json:"kind"`   // "tool_call", "tool_done", "log"
	Message   string    `json:"msg"`
	Payload   any       `json:"data,omitempty"`
}
```

Store these in a ring buffer (in-memory) and flush to persistent storage (Run Trace).
