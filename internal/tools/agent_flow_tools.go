package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/agents"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// ── agent.flow.get ────────────────────────────────────────────────────────────

// AgentFlowGetTool reads the configured execution pipeline for a named agent.
type AgentFlowGetTool struct{ AgentsConfigDir string }

func (t AgentFlowGetTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "agent.flow.get",
		Description: "Read the execution pipeline (flow) configured for a named agent. " +
			"Returns the YAML or markdown content, or a notice if no flow is configured. " +
			"Use agent.flow.set to define or update it.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"agent_name": map[string]any{
					"type":        "string",
					"description": "The agent name (e.g. pr_review, devops, security, repointel, or a custom agent name).",
				},
			},
			Required: []string{"agent_name"},
		},
	}
}

func (t AgentFlowGetTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var args struct {
		AgentName string `json:"agent_name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.AgentName == "" {
		return "", fmt.Errorf("agent_name is required")
	}
	content, err := agents.ReadAgentFlowRaw(t.AgentsConfigDir, args.AgentName)
	if err != nil {
		return "", err
	}
	if content == "" {
		return fmt.Sprintf("No execution pipeline configured for agent %q. "+
			"Use agent.flow.set to define one.", args.AgentName), nil
	}
	return fmt.Sprintf("Flow for %q (YAML):\n\n%s", args.AgentName, content), nil
}

// ── agent.flow.set ────────────────────────────────────────────────────────────

// AgentFlowSetTool writes or replaces the execution pipeline for a named agent.
type AgentFlowSetTool struct{ AgentsConfigDir string }

func (t AgentFlowSetTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "agent.flow.set",
		Description: "Write or replace the execution pipeline for a named agent (stored as flow.yaml). " +
			"The pipeline is validated and injected into the agent's system prompt on every spawn. " +
			"Supports conditions: always, jira_linked, sonar_enabled, github_enabled, gitlab_enabled. " +
			"Schema: {id, name, stages: [{id, name, description, condition, tool_hints}]}",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"agent_name": map[string]any{
					"type":        "string",
					"description": "The agent name (e.g. pr_review, devops, security, repointel, or a custom name).",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "YAML pipeline definition. Must parse as AgentFlow with id and stages fields.",
				},
			},
			Required: []string{"agent_name", "content"},
		},
	}
}

func (t AgentFlowSetTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var args struct {
		AgentName string `json:"agent_name"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.AgentName == "" {
		return "", fmt.Errorf("agent_name is required")
	}
	if strings.TrimSpace(args.Content) == "" {
		return "", fmt.Errorf("content must not be empty")
	}
	if err := agents.WriteAgentFlow(t.AgentsConfigDir, args.AgentName, args.Content); err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"Execution pipeline saved for agent %q. "+
			"It will be injected into the agent's system prompt on the next spawn.",
		args.AgentName,
	), nil
}

// ── agent.create ─────────────────────────────────────────────────────────────

// AgentCreateTool creates a new custom specialist agent at runtime.
type AgentCreateTool struct {
	CustomAgentsDir string
	// RegisterFn registers the new agent into the live registry immediately,
	// so it is available without a restart. May be nil (restart required).
	RegisterFn func(def agents.AgentDef)
}

func (t AgentCreateTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "agent.create",
		Description: "Create a new custom specialist agent and register it immediately. " +
			"The agent definition is stored in config/agents/custom/<name>/agent.yaml " +
			"and becomes available for routing without a restart. " +
			"Use agent.flow.set to configure its execution pipeline after creation.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Unique agent name (lowercase, hyphens allowed). E.g. infra, datadog-ops.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "One-sentence description of what this agent specialises in.",
				},
				"keywords": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Phrases that trigger routing to this agent (case-insensitive substring matches).",
				},
				"system_prompt": map[string]any{
					"type":        "string",
					"description": "System prompt fragment injected when this agent runs. Describe its focus and rules.",
				},
				"allowed_tools": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Tool slugs this agent may use. Empty = all tools.",
				},
				"blocked_tools": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Tool slugs explicitly denied to this agent. Typically include subagent_run* to prevent recursion.",
				},
			},
			Required: []string{"name", "description", "keywords"},
		},
	}
}

func (t AgentCreateTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var args agents.CustomAgentDef
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	if len(args.Keywords) == 0 {
		return "", fmt.Errorf("at least one keyword is required for routing")
	}
	if err := agents.WriteCustomAgentDef(t.CustomAgentsDir, args); err != nil {
		return "", err
	}
	def := args.ToAgentDef()
	if t.RegisterFn != nil {
		t.RegisterFn(def)
	}
	return fmt.Sprintf(
		"Custom agent %q created and registered. "+
			"Use agent.flow.set to configure its execution pipeline, "+
			"and agent.list to verify registration.",
		args.Name,
	), nil
}

// ── agent.list ────────────────────────────────────────────────────────────────

// AgentListTool lists all registered agents (built-in + custom).
type AgentListTool struct {
	// AllFn returns all currently registered agent definitions.
	AllFn func() []agents.AgentDef
	// BuiltinNames is the set of names that cannot be removed.
	BuiltinNames map[string]bool
}

func (t AgentListTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "agent.list",
		Description: "List all registered specialist agents (built-in and custom) with their " +
			"descriptions and keyword counts. Use this to verify a newly created agent is active " +
			"or to check which agents are available for routing.",
		InputSchema: provider.ToolParameter{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func (t AgentListTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	if t.AllFn == nil {
		return "Agent registry not available.", nil
	}
	defs := t.AllFn()
	if len(defs) == 0 {
		return "No agents registered.", nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Registered agents (%d):\n\n", len(defs))
	for _, d := range defs {
		kind := "custom"
		if t.BuiltinNames[d.Name] {
			kind = "built-in"
		}
		fmt.Fprintf(&sb, "%-14s [%s]  keywords=%d\n  %s\n",
			d.Name, kind, len(d.Keywords), d.Description)
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}

// ── agent.remove ─────────────────────────────────────────────────────────────

// AgentRemoveTool removes a custom agent from the registry and disk.
type AgentRemoveTool struct {
	CustomAgentsDir string
	BuiltinNames    map[string]bool
	// UnregisterFn removes the agent from the live registry.
	UnregisterFn func(name string) bool
}

func (t AgentRemoveTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "agent.remove",
		Description: "Remove a custom specialist agent from the registry and delete its definition from disk. " +
			"Built-in agents (devops, security, repointel, pr_review) cannot be removed.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The custom agent name to remove.",
				},
			},
			Required: []string{"name"},
		},
	}
}

func (t AgentRemoveTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("parse input: %w", err)
	}
	if args.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	if t.BuiltinNames[args.Name] {
		return fmt.Sprintf("Cannot remove built-in agent %q. "+
			"Use agent.flow.set to override its pipeline instead.", args.Name), nil
	}
	if err := agents.RemoveCustomAgent(t.CustomAgentsDir, args.Name); err != nil {
		return "", err
	}
	if t.UnregisterFn != nil {
		t.UnregisterFn(args.Name)
	}
	return fmt.Sprintf("Custom agent %q removed from registry and disk.", args.Name), nil
}
