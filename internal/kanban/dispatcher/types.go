// Package dispatcher provides agent drivers that execute card tasks.
// Each driver spawns an agent (Go LLM provider or external CLI) and
// streams events back to the dispatch service.
package dispatcher

import "context"

// AgentDriver is the interface for any agent that can execute a card.
type AgentDriver interface {
	// Type returns the driver type: "go", "claude-code", "codex", "kimi-code", "mcp"
	Type() string

	// Run starts an agent execution with the given options.
	// Returns a channel of events and a cancel function.
	Run(ctx context.Context, opts RunOpts) (<-chan Event, context.CancelFunc, error)

	// SupportedModels returns available models for this driver.
	SupportedModels() []string
}

// RunOpts configures a single agent run.
type RunOpts struct {
	RunID        string
	CardID       string
	WorktreePath string
	Branch       string
	BaseBranch   string
	Prompt       string
	SystemPrompt string         // persona-injected
	Model        string
	ModelConfig  map[string]any // temperature, max_tokens, etc.
}

// Event is one agent output event streamed during a run.
type Event struct {
	Kind     string         `json:"kind"` // text | tool_start | tool_end | decision | error | done
	Phase    string         `json:"phase,omitempty"`
	Message  string         `json:"message"`
	Metadata map[string]any `json:"metadata,omitempty"` // tool name, args, result, cost, tokens
}

// IsDecision returns true if this event signals the agent needs human input.
func (e Event) IsDecision() bool {
	return e.Kind == "decision"
}

// IsDone returns true if this event signals the run is complete.
func (e Event) IsDone() bool {
	return e.Kind == "done" || e.Kind == "error"
}
