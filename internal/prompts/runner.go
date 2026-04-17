package prompts

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// Runner executes SmartPrompts and Chains against an LLM provider.
//
// The Runner is deliberately lightweight: it does not register tools,
// consult memory, or call the main agent loop. It is a building block
// that the agent uses via a `chain_run` tool when it wants a focused,
// multi-step reasoning pass without risking runaway iteration.
type Runner struct {
	// Provider is the LLM backend. Required.
	Provider provider.Provider
	// Lib is the SmartPrompt + Chain catalogue. Required.
	Lib *Library
	// DefaultModel is used when a step does not override `model:`.
	DefaultModel string
	// DefaultTemperature is used when a step does not override.
	DefaultTemperature float64
	// MaxStepsPerChain caps any single RunChain invocation, regardless of
	// the chain's declared length. Default 8.
	MaxStepsPerChain int
}

// RunChain executes the named chain, piping each step's output into the
// next step's template context as `.prev`. All initial `inputs` are
// exposed as `.<key>` in every step's body, and the full per-step
// history is exposed as `.steps` (a slice of StepResult).
func (r *Runner) RunChain(ctx context.Context, chainID string, inputs map[string]any) (*ChainResult, error) {
	if r.Lib == nil {
		return nil, fmt.Errorf("prompts: runner has no library")
	}
	if r.Provider == nil {
		return nil, fmt.Errorf("prompts: runner has no provider")
	}
	chain, ok := r.Lib.Chain(chainID)
	if !ok {
		return nil, fmt.Errorf("prompts: unknown chain %q", chainID)
	}
	if inputs == nil {
		inputs = map[string]any{}
	}
	for _, req := range chain.Inputs {
		if _, present := inputs[req]; !present {
			return nil, fmt.Errorf("prompts: chain %q requires input %q", chainID, req)
		}
	}
	cap := r.MaxStepsPerChain
	if cap <= 0 {
		cap = 8
	}
	if len(chain.Steps) < cap {
		cap = len(chain.Steps)
	}

	started := time.Now()
	out := &ChainResult{ChainID: chainID}
	var prev string
	for i := 0; i < cap; i++ {
		stepID := chain.Steps[i]
		ctxData := map[string]any{}
		for k, v := range inputs {
			ctxData[k] = v
		}
		ctxData["prev"] = prev
		ctxData["step_index"] = i
		ctxData["step_count"] = cap
		ctxData["steps"] = append([]StepResult(nil), out.Steps...)

		step, err := r.runStep(ctx, stepID, ctxData)
		out.Steps = append(out.Steps, step)
		out.Usage.PromptTokens += step.Usage.PromptTokens
		out.Usage.CompletionTokens += step.Usage.CompletionTokens
		out.Usage.TotalTokens += step.Usage.TotalTokens
		if err != nil {
			out.Steps[len(out.Steps)-1].Error = err.Error()
			out.Latency = time.Since(started)
			return out, fmt.Errorf("chain %q step %q: %w", chainID, stepID, err)
		}
		prev = step.Output
	}
	out.Final = prev
	out.Latency = time.Since(started)
	return out, nil
}

// RunPrompt executes a single SmartPrompt by ID and returns the raw text
// output. Useful for ad-hoc meta prompts (e.g. self-critique on an
// arbitrary draft) where there is no named chain.
func (r *Runner) RunPrompt(ctx context.Context, id string, inputs map[string]any) (StepResult, error) {
	if r.Lib == nil {
		return StepResult{}, fmt.Errorf("prompts: runner has no library")
	}
	if r.Provider == nil {
		return StepResult{}, fmt.Errorf("prompts: runner has no provider")
	}
	return r.runStep(ctx, id, inputs)
}

func (r *Runner) runStep(ctx context.Context, id string, data map[string]any) (StepResult, error) {
	sp, ok := r.Lib.Prompt(id)
	if !ok {
		return StepResult{PromptID: id}, fmt.Errorf("unknown prompt %q", id)
	}
	body, err := renderTemplate(sp.Body, data)
	if err != nil {
		return StepResult{PromptID: id}, fmt.Errorf("render body: %w", err)
	}
	system, err := renderTemplate(sp.System, data)
	if err != nil {
		return StepResult{PromptID: id}, fmt.Errorf("render system: %w", err)
	}

	model := sp.Model
	if model == "" {
		model = r.DefaultModel
	}
	temp := r.DefaultTemperature
	if sp.Temperature != nil {
		temp = *sp.Temperature
	}

	req := &provider.CompletionRequest{
		Model:        model,
		SystemPrompt: strings.TrimSpace(system),
		Messages: []provider.Message{
			provider.NewTextMessage(provider.RoleUser, strings.TrimSpace(body)),
		},
		Temperature: temp,
		MaxTokens:   sp.MaxTokens,
	}
	if sp.TopP != nil {
		req.TopP = *sp.TopP
	}

	started := time.Now()
	resp, err := r.Provider.Complete(ctx, req)
	if err != nil {
		return StepResult{PromptID: id, Model: model, Latency: time.Since(started)}, err
	}
	return StepResult{
		PromptID: id,
		Output:   strings.TrimSpace(resp.Text()),
		Model:    model,
		Usage:    resp.Usage,
		Latency:  time.Since(started),
	}, nil
}

// renderTemplate applies Go text/template substitution against `data`.
// Missing keys render as empty strings (option "missingkey=zero") so a
// template like `{{.pr_url}}` never blows up when the caller hasn't
// supplied it yet.
func renderTemplate(tpl string, data map[string]any) (string, error) {
	if strings.TrimSpace(tpl) == "" {
		return "", nil
	}
	t, err := template.New("prompt").Option("missingkey=zero").Parse(tpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
