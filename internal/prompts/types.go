// Package prompts implements the OpsIntelligence smart-prompt library and
// chain runtime.
//
// A SmartPrompt is a reusable prompt fragment stored on disk with YAML
// frontmatter (system instructions, output contract, next-step hint) and a
// Go text/template body (the user message).
//
// A Chain is an ordered list of SmartPrompt IDs. The Runner executes the
// steps sequentially, piping each step's output into the next step's
// template context as `.prev`, and returns the accumulated result plus
// the final step's text.
//
// Design choices borrowed (as techniques, not text) from modern agent
// system prompts published openly by other IDE/agent teams:
//
//   - Explicit reasoning phases with structured XML-like scaffolds.
//   - Self-critique pass before rendering the user-visible answer.
//   - Evidence-first rendering: every claim cites a source.
//   - Budget discipline: chains are bounded and cannot loop.
//   - Specialist prompts picked by task, not one monolithic instruction set.
package prompts

import (
	"time"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// OutputFormat describes the expected shape of a SmartPrompt's response.
type OutputFormat string

const (
	OutputText OutputFormat = "text"
	OutputJSON OutputFormat = "json"
	OutputXML  OutputFormat = "xml"
)

// OutputSpec declares the contract the step's output must honour. It is
// advisory: the runner does not hard-enforce schemas, but it surfaces
// violations so the LLM can self-correct in a downstream step.
type OutputSpec struct {
	Format   OutputFormat `yaml:"format" json:"format"`
	Required []string     `yaml:"required,omitempty" json:"required,omitempty"`
}

// SmartPrompt is a single reusable prompt fragment.
type SmartPrompt struct {
	// ID is the canonical identifier (e.g. "pr-review/gather"). Slash-
	// delimited so chains in the same family can share a namespace.
	ID string `yaml:"id" json:"id"`
	// Name is a short human-friendly label for `opsintelligence prompts ls`.
	Name string `yaml:"name" json:"name"`
	// Purpose is a one-liner surfaced to the agent in the Smart Prompts
	// Index so the model can pick the right chain on its own.
	Purpose string `yaml:"purpose" json:"purpose"`
	// System is the system-prompt fragment used when the step is run.
	// It is prepended to the runner's base system prompt.
	System string `yaml:"system" json:"system"`
	// Next (optional) is the preferred follow-up prompt ID when this step
	// is not already embedded in a named chain.
	Next string `yaml:"next,omitempty" json:"next,omitempty"`
	// Model is an optional per-step model override (e.g.
	// "anthropic/claude-3-7-sonnet-20250219" for the critique step).
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// Temperature, TopP, MaxTokens are optional sampling overrides.
	Temperature *float64 `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	TopP        *float64 `yaml:"top_p,omitempty" json:"top_p,omitempty"`
	MaxTokens   int      `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	// Output declares the expected shape of the step's response.
	Output OutputSpec `yaml:"output,omitempty" json:"output,omitempty"`
	// Tags are free-form labels for filtering in `prompts ls`.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	// Body is the user-message template (Go text/template syntax). Filled
	// from the Markdown body under the frontmatter on load.
	Body string `yaml:"-" json:"-"`
	// SourcePath is the filesystem path this prompt was loaded from
	// (for diagnostics and `prompts show`). Empty for embedded defaults.
	SourcePath string `yaml:"-" json:"source_path,omitempty"`
}

// Chain is an ordered sequence of SmartPrompt IDs executed as one unit.
type Chain struct {
	ID      string   `yaml:"id" json:"id"`
	Name    string   `yaml:"name" json:"name"`
	Purpose string   `yaml:"purpose" json:"purpose"`
	Inputs  []string `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Steps   []string `yaml:"steps" json:"steps"`
	// MaxSteps is a safety cap; defaults to len(Steps) when zero. Chains
	// never loop — MaxSteps only limits how many IDs the runner will
	// dereference from Next hints when a caller runs a SmartPrompt by ID
	// directly (no explicit chain).
	MaxSteps int `yaml:"max_steps,omitempty" json:"max_steps,omitempty"`
	// Tags are free-form labels for filtering and indexing.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	// SourcePath tracks where the chain file lives on disk.
	SourcePath string `yaml:"-" json:"source_path,omitempty"`
}

// StepResult is the outcome of a single SmartPrompt execution.
type StepResult struct {
	PromptID string              `json:"prompt_id"`
	Output   string              `json:"output"`
	Model    string              `json:"model"`
	Usage    provider.TokenUsage `json:"usage"`
	Latency  time.Duration       `json:"latency"`
	// Error is set when the step failed. The runner stops the chain at
	// the first error and returns the partial ChainResult.
	Error string `json:"error,omitempty"`
}

// ChainResult is the full trace of a chain execution.
type ChainResult struct {
	ChainID string              `json:"chain_id"`
	Steps   []StepResult        `json:"steps"`
	Final   string              `json:"final"`
	Usage   provider.TokenUsage `json:"usage"`
	Latency time.Duration       `json:"latency"`
}
