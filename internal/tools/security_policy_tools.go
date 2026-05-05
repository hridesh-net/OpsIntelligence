package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// SecurityPolicyGetTool reads the active security policies (POLICIES.md + teams/*.md).
// It is read-only: security policies are managed by administrators, not by the agent.
type SecurityPolicyGetTool struct {
	// PolicyFn returns the concatenated policy content. Typically wired to
	// security.LoadPolicyBundle(stateDir) via a closure in main.go.
	PolicyFn func() string
}

func (t SecurityPolicyGetTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "security.policy.get",
		Description: "Read the active security policies for this deployment: POLICIES.md and all team/*.md " +
			"files, concatenated in order. These policies are injected into the security agent's system " +
			"prompt automatically, but this tool lets you inspect the current policy bundle explicitly.",
		InputSchema: provider.ToolParameter{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func (t SecurityPolicyGetTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	if t.PolicyFn == nil {
		return "No security policy function configured.", nil
	}
	content := t.PolicyFn()
	if strings.TrimSpace(content) == "" {
		return "No active security policies found. " +
			"Create POLICIES.md in the state directory or add markdown files under config/teams/ to define policies.", nil
	}
	return content, nil
}
