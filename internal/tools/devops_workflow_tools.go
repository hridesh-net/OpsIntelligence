package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// DevOpsWorkflowGetTool reads the configured DevOps workflow policy from disk.
type DevOpsWorkflowGetTool struct{ Path string }

func (t DevOpsWorkflowGetTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "devops.workflow.get",
		Description: "Read the current DevOps workflow policy configured for this agent. " +
			"Returns the markdown content of workflow.md, or a notice if none is configured. " +
			"Use devops.workflow.set to define or update it.",
		InputSchema: provider.ToolParameter{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func (t DevOpsWorkflowGetTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	data, err := os.ReadFile(t.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return "No DevOps workflow policy configured. Use devops.workflow.set to define one.", nil
		}
		return "", fmt.Errorf("read workflow policy: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "Workflow policy file exists but is empty. Use devops.workflow.set to configure it.", nil
	}
	return string(data), nil
}

// DevOpsWorkflowSetTool writes or replaces the DevOps workflow policy on disk.
// The content is injected into the devops agent's system prompt on every spawn.
type DevOpsWorkflowSetTool struct{ Path string }

func (t DevOpsWorkflowSetTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "devops.workflow.set",
		Description: "Write or replace the DevOps workflow policy / standards. " +
			"The markdown content is stored in workflow.md and automatically injected " +
			"into the devops specialist's system prompt on every run. " +
			"Include deployment approval rules, CI thresholds, branching conventions, and on-call escalation paths.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"content": map[string]any{
					"type": "string",
					"description": "Full markdown content of the DevOps workflow policy " +
						"(deployment rules, CI standards, branching conventions, escalation paths, etc.).",
				},
			},
			Required: []string{"content"},
		},
	}
}

func (t DevOpsWorkflowSetTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var args struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if strings.TrimSpace(args.Content) == "" {
		return "", fmt.Errorf("content must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o755); err != nil {
		return "", fmt.Errorf("create workflow policy dir: %w", err)
	}
	if err := os.WriteFile(t.Path, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write workflow policy: %w", err)
	}
	return fmt.Sprintf(
		"DevOps workflow policy saved (%d bytes). "+
			"It will be injected into the devops agent's system prompt on the next specialist spawn.",
		len(args.Content),
	), nil
}
