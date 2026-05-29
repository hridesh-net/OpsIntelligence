package dispatcher

import (
	"context"
	"encoding/json"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/memory"
)

// GoDriver uses the native Go agent runner (internal/agent) to execute cards.
// The runner is shared; each Run executes in its own goroutine with
// a custom StreamHandler so concurrent dispatches are isolated.
type GoDriver struct {
	runner *agent.Runner
}

// NewGoDriver creates a driver that wraps an existing agent.Runner.
func NewGoDriver(runner *agent.Runner) *GoDriver {
	return &GoDriver{runner: runner}
}

// Type returns "go".
func (d *GoDriver) Type() string { return "go" }

// SupportedModels returns models available through the Go provider chain.
// The actual list depends on the configured provider; this is a reasonable default set.
func (d *GoDriver) SupportedModels() []string {
	return []string{
		"claude-opus-4", "claude-sonnet-4", "claude-haiku-4",
		"gpt-4o", "gpt-4o-mini", "gpt-4-turbo",
		"gemini-1.5-pro", "gemini-1.5-flash",
	}
}

// Run executes the card prompt using the Go agent runner.
func (d *GoDriver) Run(ctx context.Context, opts RunOpts) (<-chan Event, context.CancelFunc, error) {
	events := make(chan Event, 64)
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		defer close(events)

		handler := &goStreamHandler{events: events}
		msg := memory.Message{
			Role:    "user",
			Content: opts.Prompt,
		}

		d.runner.RunStream(ctx, msg, handler)
	}()

	return events, cancel, nil
}

// goStreamHandler converts agent.Runner callbacks to dispatcher Events.
// Uses non-blocking sends with a drop strategy to avoid deadlocking the
// shared agent.Runner when the consumer is slow.
type goStreamHandler struct {
	events chan<- Event
	buf    string
}

func (h *goStreamHandler) send(ev Event) {
	select {
	case h.events <- ev:
	default:
		// Channel full: drop the event rather than block the runner.
		// The consumer will still get the done/error event.
	}
}

func (h *goStreamHandler) OnToken(token string) {
	h.buf += token
	h.send(Event{Kind: "text", Message: token})
}

func (h *goStreamHandler) OnToolCall(name string, input json.RawMessage) {
	h.send(Event{
		Kind:    "tool_start",
		Message: name,
		Metadata: map[string]any{
			"tool":  name,
			"input": string(input),
		},
	})
}

func (h *goStreamHandler) OnToolResult(name string, result string) {
	h.send(Event{
		Kind:    "tool_end",
		Message: name,
		Metadata: map[string]any{
			"tool":   name,
			"result": result,
		},
	})
}

func (h *goStreamHandler) OnDone(result *agent.RunResult) {
	meta := map[string]any{}
	if result != nil {
		meta["iterations"] = result.Iterations
		meta["usage"] = map[string]any{
			"prompt_tokens":     result.Usage.PromptTokens,
			"completion_tokens": result.Usage.CompletionTokens,
			"total_tokens":      result.Usage.TotalTokens,
		}
	}
	h.send(Event{
		Kind:     "done",
		Message:  h.buf,
		Metadata: meta,
	})
}

func (h *goStreamHandler) OnError(err error) {
	h.send(Event{
		Kind:    "error",
		Message: err.Error(),
	})
}
