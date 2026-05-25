// Package dispatcher provides the Go provider driver for kanban card runs.
package dispatcher

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// GoDriver runs agents using the internal Go LLM provider chain.
type GoDriver struct {
	provider provider.Provider
	log      *zap.Logger
}

// NewGoDriver creates a driver backed by the given LLM provider.
func NewGoDriver(p provider.Provider, log *zap.Logger) *GoDriver {
	if log == nil {
		log = zap.NewNop()
	}
	return &GoDriver{provider: p, log: log}
}

// Name returns "go".
func (d *GoDriver) Name() string { return "go" }

// Run executes a Go-provider agent turn for the card.
func (d *GoDriver) Run(ctx context.Context, req RunRequest, events chan<- Event) Result {
	start := time.Now()
	defer close(events)

	emit := func(e Event) {
		select {
		case events <- e:
		case <-ctx.Done():
		}
	}

	emit(Event{Kind: EventKindLifecycle, Phase: "start", Message: "run started"})

	messages := []provider.Message{
		provider.NewTextMessage(provider.RoleSystem, req.SystemPrompt),
		provider.NewTextMessage(provider.RoleUser, fmt.Sprintf(
			"Work on this card in %s:\n\nTitle: %s\nDescription: %s",
			req.WorktreePath, req.CardTitle, req.CardDescription)),
	}

	model := req.Model
	if model == "" {
		model = "gemini-2.5-flash" // sensible default
	}

	cr := &provider.CompletionRequest{
		Model:    model,
		Messages: messages,
		Stream:   true,
	}

	stream, err := d.provider.Stream(ctx, cr)
	if err != nil {
		emit(Event{Kind: EventKindError, Message: err.Error()})
		return Result{
			Status: "failed",
			Error:  err.Error(),
			ElapsedMs: DurationMS(start),
		}
	}

	var fullText string
	var toolCalls []provider.ContentPart
	var usage provider.TokenUsage

	for event := range stream {
		if ctx.Err() != nil {
			emit(Event{Kind: EventKindLifecycle, Phase: "cancelled", Message: "run cancelled by user"})
			return Result{Status: "stopped", ElapsedMs: DurationMS(start)}
		}
		switch event.Type {
		case provider.StreamEventText:
			fullText += event.Text
			emit(Event{Kind: EventKindText, Message: event.Text})
		case provider.StreamEventToolUse:
			if event.ToolUse != nil {
				toolCalls = append(toolCalls, *event.ToolUse)
				emit(Event{Kind: EventKindToolStart, Phase: event.ToolUse.ToolName,
					Message: fmt.Sprintf("tool: %s", event.ToolUse.ToolName),
					Metadata: map[string]any{"input": event.ToolUse.ToolInput}})
			}
		case provider.StreamEventDone:
			if event.Usage != nil {
				usage = *event.Usage
			}
		case provider.StreamEventError:
			emit(Event{Kind: EventKindError, Message: event.Err.Error()})
			return Result{
				Status: "failed",
				Error:  event.Err.Error(),
				TokenIn: int64(usage.PromptTokens),
				TokenOut: int64(usage.CompletionTokens),
				ElapsedMs: DurationMS(start),
			}
		}
	}

	emit(Event{Kind: EventKindLifecycle, Phase: "complete", Message: "run completed"})

	return Result{
		Status:        "completed",
		ResultSummary: truncate(fullText, 500),
		TokenIn:       int64(usage.PromptTokens),
		TokenOut:      int64(usage.CompletionTokens),
		ElapsedMs:     DurationMS(start),
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// AgentRunner is a thin wrapper that lets the Go driver reuse the existing
// agent.Runner when full tool registry + memory is desired.
type AgentRunner struct {
	runner *agent.Runner
	log    *zap.Logger
}

// NewAgentRunner creates a driver that delegates to an agent.Runner.
func NewAgentRunner(r *agent.Runner, log *zap.Logger) *AgentRunner {
	return &AgentRunner{runner: r, log: log}
}

// Name returns "go".
func (d *AgentRunner) Name() string { return "go" }

// Run delegates to the underlying runner's chat loop.
func (d *AgentRunner) Run(ctx context.Context, req RunRequest, events chan<- Event) Result {
	// TODO: wire agent.Runner chat loop with worktree context.
	// For now, fall back to the simple provider driver above.
	return (&GoDriver{log: d.log}).Run(ctx, req, events)
}
