package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/observability/correlation"
	"github.com/opsintelligence/opsintelligence/internal/observability/runtrace"
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
	Runner    *prompts.Runner
	TracePath string // optional NDJSON run trace (same file as agent.run_trace_file)
}

// NewChainRunTool returns a ChainRunTool backed by the given runner.
func NewChainRunTool(r *prompts.Runner, tracePath string) *ChainRunTool {
	return &ChainRunTool{Runner: r, TracePath: strings.TrimSpace(tracePath)}
}

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
					"type": "object",
					"description": "Inputs for the chain templates (e.g. {'pr_url': 'https://github.com/o/r/pull/1'}). " +
						"For chain id \"pr-review\" on GitHub, the outer agent should first call devops.github.pull_request and devops.github.pr_diff, " +
						"then pass optional strings github_pr_json, github_diff (truncate diff to ~24k chars), and github_ci_hint so the gather step has real evidence.",
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

	inputKeys := chainInputKeys(req.Inputs)

	switch req.Kind {
	case "chain":
		return t.runChain(ctx, req.ID, req.Inputs, wantTrace, inputKeys)
	case "prompt":
		return t.runSinglePrompt(ctx, req.ID, req.Inputs, inputKeys)
	case "auto":
		if _, ok := t.Runner.Lib.Chain(req.ID); ok {
			return t.runChain(ctx, req.ID, req.Inputs, wantTrace, inputKeys)
		}
		if _, ok := t.Runner.Lib.Prompt(req.ID); ok {
			return t.runSinglePrompt(ctx, req.ID, req.Inputs, inputKeys)
		}
		return unknownIDMessage(t.Runner.Lib, req.ID), nil
	default:
		return fmt.Sprintf("chain_run: unknown kind %q (expected chain|prompt|auto)", req.Kind), nil
	}
}

func chainInputKeys(inputs map[string]any) []string {
	if len(inputs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (t *ChainRunTool) runChain(ctx context.Context, id string, inputs map[string]any, trace bool, inputKeys []string) (string, error) {
	started := time.Now()
	t.traceChain(ctx, "chain_run_start", map[string]any{
		"chain_id":    id,
		"run_kind":    "chain",
		"input_keys":  inputKeys,
		"chain_model": t.Runner.DefaultModel,
	})
	result, err := t.Runner.RunChain(ctx, id, inputs)
	if err != nil {
		t.traceChain(ctx, "chain_run_error", map[string]any{
			"chain_id": id,
			"error":    err.Error(),
			"ms":       time.Since(started).Milliseconds(),
		})
		return fmt.Sprintf("chain_run: %s", err.Error()), nil
	}
	stepIDs := make([]string, len(result.Steps))
	stepModels := make([]string, len(result.Steps))
	stepTok := make([]int, len(result.Steps))
	for i, s := range result.Steps {
		stepIDs[i] = s.PromptID
		stepModels[i] = s.Model
		stepTok[i] = s.Usage.TotalTokens
	}
	t.traceChain(ctx, "chain_run_complete", map[string]any{
		"chain_id":     id,
		"ms":           time.Since(started).Milliseconds(),
		"step_prompts": stepIDs,
		"step_models":  stepModels,
		"step_tokens":  stepTok,
		"total_tokens": result.Usage.TotalTokens,
	})
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

func (t *ChainRunTool) runSinglePrompt(ctx context.Context, id string, inputs map[string]any, inputKeys []string) (string, error) {
	started := time.Now()
	t.traceChain(ctx, "chain_run_start", map[string]any{
		"chain_id":    id,
		"run_kind":    "prompt",
		"input_keys":  inputKeys,
		"chain_model": t.Runner.DefaultModel,
	})
	step, err := t.Runner.RunPrompt(ctx, id, inputs)
	if err != nil {
		t.traceChain(ctx, "chain_run_error", map[string]any{
			"chain_id": id,
			"error":    err.Error(),
			"ms":       time.Since(started).Milliseconds(),
		})
		return fmt.Sprintf("chain_run: %s", err.Error()), nil
	}
	t.traceChain(ctx, "chain_run_complete", map[string]any{
		"chain_id":     id,
		"run_kind":     "prompt",
		"ms":           time.Since(started).Milliseconds(),
		"step_prompts": []string{id},
		"step_models":  []string{step.Model},
		"step_tokens":  []int{step.Usage.TotalTokens},
		"total_tokens": step.Usage.TotalTokens,
	})
	return step.Output, nil
}

func (t *ChainRunTool) traceChain(ctx context.Context, eventKind string, fields map[string]any) {
	path := runtrace.OutputPathFrom(ctx)
	if path == "" {
		path = strings.TrimSpace(t.TracePath)
	}
	if path == "" {
		return
	}
	ev := map[string]any{}
	for k, v := range fields {
		ev[k] = v
	}
	ev["kind"] = eventKind
	if id := correlation.RequestID(ctx); id != "" {
		ev["request_id"] = id
	}
	if id := correlation.SessionID(ctx); id != "" {
		ev["session_id"] = id
	}
	if ch := correlation.Channel(ctx); ch != "" {
		ev["channel"] = ch
	}
	runtrace.Append(path, ev)
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
