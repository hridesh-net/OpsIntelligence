package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentFlow defines the ordered execution pipeline for a specialist agent.
// Stored at <agentsConfigDir>/<agent-name>/flow.yaml.
// When present, it is rendered into the agent's system prompt at spawn time,
// directing stage-by-stage execution with optional condition gating.
type AgentFlow struct {
	ID     string           `yaml:"id"`
	Name   string           `yaml:"name,omitempty"`
	Stages []AgentFlowStage `yaml:"stages"`
}

// AgentFlowStage is one phase of the agent execution pipeline.
type AgentFlowStage struct {
	// ID is a short machine-readable identifier (e.g. "linter", "security").
	ID string `yaml:"id"`
	// Name is the human-readable stage label shown in the system prompt.
	Name string `yaml:"name"`
	// Description is injected verbatim as stage instructions.
	Description string `yaml:"description"`
	// Condition gates this stage. Supported built-in values:
	//   always (default), jira_linked, sonar_enabled, github_enabled, gitlab_enabled
	// Unrecognised values are treated as always.
	Condition string `yaml:"condition,omitempty"`
	// ToolHints lists tools the agent should prefer for this stage. Advisory only.
	ToolHints []string `yaml:"tool_hints,omitempty"`
}

// FlowEvalContext carries runtime integration flags used to evaluate stage conditions.
type FlowEvalContext struct {
	JiraLinked    bool
	SonarEnabled  bool
	GitHubEnabled bool
	GitLabEnabled bool
}

func (e FlowEvalContext) eval(cond string) bool {
	switch strings.ToLower(strings.TrimSpace(cond)) {
	case "", "always":
		return true
	case "jira_linked":
		return e.JiraLinked
	case "sonar_enabled":
		return e.SonarEnabled
	case "github_enabled":
		return e.GitHubEnabled
	case "gitlab_enabled":
		return e.GitLabEnabled
	default:
		return true
	}
}

// LoadAgentFlow reads and parses flow.yaml at path.
// Returns (nil, nil) when the file does not exist.
func LoadAgentFlow(path string) (*AgentFlow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read agent flow %s: %w", path, err)
	}
	var flow AgentFlow
	if err := yaml.Unmarshal(data, &flow); err != nil {
		return nil, fmt.Errorf("parse agent flow %s: %w", path, err)
	}
	return &flow, nil
}

// WriteAgentFlow validates content as YAML and writes it to
// <agentsConfigDir>/<agentName>/flow.yaml, creating the directory if needed.
func WriteAgentFlow(agentsConfigDir, agentName, content string) error {
	var flow AgentFlow
	if err := yaml.Unmarshal([]byte(content), &flow); err != nil {
		return fmt.Errorf("invalid flow YAML: %w", err)
	}
	dir := filepath.Join(agentsConfigDir, agentName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create agent flow dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "flow.yaml"), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write agent flow: %w", err)
	}
	return nil
}

// ReadAgentFlowRaw returns the raw YAML content of the flow file for agentName.
// Returns ("", nil) when no flow.yaml is configured.
func ReadAgentFlowRaw(agentsConfigDir, agentName string) (string, error) {
	path := filepath.Join(agentsConfigDir, agentName, "flow.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read agent flow: %w", err)
	}
	return string(data), nil
}

// RenderFlowForPrompt produces markdown instructions for the agent's execution pipeline.
// Stages whose conditions are not met by evalCtx are shown as SKIP entries.
// Returns an empty string when flow is nil or has no stages.
func RenderFlowForPrompt(flow *AgentFlow, evalCtx FlowEvalContext) string {
	if flow == nil || len(flow.Stages) == 0 {
		return ""
	}
	title := flow.Name
	if title == "" {
		title = flow.ID
	}
	var sb strings.Builder
	sb.WriteString("## Execution Pipeline")
	if title != "" {
		sb.WriteString(" — ")
		sb.WriteString(title)
	}
	sb.WriteString("\nExecute the following stages **in order**. ")
	sb.WriteString("Skip only stages whose condition label shows **SKIP**.\n\n")

	for i, s := range flow.Stages {
		cond := s.Condition
		if cond == "" {
			cond = "always"
		}
		sb.WriteString(fmt.Sprintf("### Stage %d — %s\n", i+1, s.Name))
		if evalCtx.eval(s.Condition) {
			sb.WriteString(fmt.Sprintf("**Condition**: `%s` ✓\n", cond))
			if s.Description != "" {
				sb.WriteString(strings.TrimSpace(s.Description))
				sb.WriteString("\n")
			}
			if len(s.ToolHints) > 0 {
				sb.WriteString("**Preferred tools**: ")
				sb.WriteString(strings.Join(s.ToolHints, ", "))
				sb.WriteString("\n")
			}
		} else {
			sb.WriteString(fmt.Sprintf("**Condition**: `%s` — ⚠️ **SKIP** (integration not configured)\n", cond))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// LoadFlowContextFn is the exported entry point used by main.go when wiring
// context loaders for custom agents.
func LoadFlowContextFn(agentName, agentsConfigDir string, evalCtx FlowEvalContext) string {
	return loadFlowContext(agentName, agentsConfigDir, evalCtx)
}

// loadFlowContext loads and renders flow.yaml for agentName from agentsConfigDir.
// Returns empty string when no flow.yaml exists.
func loadFlowContext(agentName, agentsConfigDir string, evalCtx FlowEvalContext) string {
	if agentsConfigDir == "" {
		return ""
	}
	path := filepath.Join(agentsConfigDir, agentName, "flow.yaml")
	flow, err := LoadAgentFlow(path)
	if err != nil || flow == nil {
		return ""
	}
	return RenderFlowForPrompt(flow, evalCtx)
}
