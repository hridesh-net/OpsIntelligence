package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/prompts"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// ChainRunTool exposes the smart-prompt chain runtime to the LLM as an
// agent-callable tool. It lets the model hand off a focused, multi-step
// reasoning task (PR review, Sonar triage, CI/CD regression triage,
// incident scribe, …) to a bounded specialist prompt pipeline rather
// than attempting it inside the main loop's single context window.
//
// The tool only invokes prompts from the loaded Library. It will not
// execute arbitrary instructions from the caller — which is the point:
// chain names are a curated vocabulary the agent can pick from via the
// Smart Prompts Index in its system prompt.
type ChainRunTool struct {
	Runner *prompts.Runner
}

// NewChainRunTool returns a ChainRunTool backed by the given runner.
func NewChainRunTool(r *prompts.Runner) *ChainRunTool { return &ChainRunTool{Runner: r} }

// Definition is the schema advertised to the LLM.
func (t *ChainRunTool) Definition() provider.ToolDef {
	desc := "Run a named smart-prompt chain or single meta prompt. " +
		"Chains are curated multi-step reasoning pipelines (e.g. pr-review: gather→analyze→critique→render). " +
		"Pass the chain/prompt id and a JSON inputs object (e.g. {\"pr_url\": \"...\"}). " +
		"Returns the final rendered output plus a per-step trace. " +
		"Use `opsintelligence prompts ls` or the Smart Prompts Index at the top of your system prompt to discover available ids."
	return provider.ToolDef{
		Name:        "chain_run",
		Description: desc,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Chain id (e.g. 'pr-review') or prompt id (e.g. 'meta/self-critique').",
				},
				"kind": map[string]any{
					"type":        "string",
					"enum":        []string{"chain", "prompt", "auto"},
					"description": "How to resolve `id`. Default 'auto': chain first, then prompt.",
				},
				"inputs": map[string]any{
					"type":        "object",
					"description": "Free-form inputs referenced by the chain/prompt templates (e.g. {'pr_url': '...'}).",
				},
				"trace": map[string]any{
					"type":        "boolean",
					"description": "When true (default), include a compact per-step trace in the response. When false, return only the final output.",
				},
			},
			Required: []string{"id"},
		},
	}
}

// Execute resolves the id against the loaded Library and runs the
// appropriate chain or single prompt.
func (t *ChainRunTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if t.Runner == nil || t.Runner.Lib == nil {
		return "chain_run is disabled: no prompt library loaded", nil
	}

	var req struct {
		ID     string         `json:"id"`
		Kind   string         `json:"kind,omitempty"`
		Inputs map[string]any `json:"inputs,omitempty"`
		Trace  *bool          `json:"trace,omitempty"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &req); err != nil {
			return "", fmt.Errorf("chain_run: invalid input: %w", err)
		}
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		return "chain_run: missing required field 'id' — see the Smart Prompts Index for available ids", nil
	}
	if req.Kind == "" {
		req.Kind = "auto"
	}
	wantTrace := true
	if req.Trace != nil {
		wantTrace = *req.Trace
	}

	switch req.Kind {
	case "chain":
		return runChain(ctx, t.Runner, req.ID, req.Inputs, wantTrace)
	case "prompt":
		return runPrompt(ctx, t.Runner, req.ID, req.Inputs)
	case "auto":
		if _, ok := t.Runner.Lib.Chain(req.ID); ok {
			return runChain(ctx, t.Runner, req.ID, req.Inputs, wantTrace)
		}
		if _, ok := t.Runner.Lib.Prompt(req.ID); ok {
			return runPrompt(ctx, t.Runner, req.ID, req.Inputs)
		}
		return unknownIDMessage(t.Runner.Lib, req.ID), nil
	default:
		return fmt.Sprintf("chain_run: unknown kind %q (expected chain|prompt|auto)", req.Kind), nil
	}
}

func runChain(ctx context.Context, r *prompts.Runner, id string, inputs map[string]any, trace bool) (string, error) {
	result, err := r.RunChain(ctx, id, inputs)
	if err != nil {
		return fmt.Sprintf("chain_run: %s", err.Error()), nil
	}
	if !trace {
		return result.Final, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "## Chain: %s  (%d steps, %s)\n\n", result.ChainID, len(result.Steps), result.Latency.Truncate(1e6))
	for i, s := range result.Steps {
		fmt.Fprintf(&sb, "### Step %d — %s  (%s, %d tok)\n", i+1, s.PromptID, s.Latency.Truncate(1e6), s.Usage.TotalTokens)
		preview := s.Output
		if len(preview) > 800 {
			preview = preview[:800] + "… [truncated]"
		}
		sb.WriteString(preview)
		sb.WriteString("\n\n")
	}
	sb.WriteString("### Final\n")
	sb.WriteString(result.Final)
	return sb.String(), nil
}

func runPrompt(ctx context.Context, r *prompts.Runner, id string, inputs map[string]any) (string, error) {
	step, err := r.RunPrompt(ctx, id, inputs)
	if err != nil {
		return fmt.Sprintf("chain_run: %s", err.Error()), nil
	}
	return step.Output, nil
}

func unknownIDMessage(lib *prompts.Library, id string) string {
	chains := lib.ListChains()
	var ids []string
	for _, c := range chains {
		ids = append(ids, c.ID)
	}
	prompts := lib.ListPrompts()
	for _, p := range prompts {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return fmt.Sprintf("chain_run: no chain or prompt named %q. Known ids: %s", id, strings.Join(ids, ", "))
}

// ChainListTool lets the agent discover the currently loaded smart
// prompts at runtime. This is handy for teams that drop custom prompts
// into <state_dir>/prompts/ after boot.
type ChainListTool struct {
	Runner *prompts.Runner
}

// NewChainListTool constructs the discovery tool.
func NewChainListTool(r *prompts.Runner) *ChainListTool { return &ChainListTool{Runner: r} }

// Definition advertises the tool schema.
func (t *ChainListTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "chain_list",
		Description: "List available smart-prompt chains and meta prompts. Useful when you are unsure which chain to invoke via chain_run.",
		InputSchema: provider.ToolParameter{Type: "object"},
	}
}

// Execute returns the human-readable index.
func (t *ChainListTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	if t.Runner == nil || t.Runner.Lib == nil {
		return "chain_list: no prompt library loaded", nil
	}
	return t.Runner.Lib.Index(), nil
}

// Ensure the tools satisfy the agent.Tool interface at compile time.
var (
	_ agent.Tool = (*ChainRunTool)(nil)
	_ agent.Tool = (*ChainListTool)(nil)
)
