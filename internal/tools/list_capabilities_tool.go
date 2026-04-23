package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

type listCapabilitiesTool struct {
	registry *agent.ToolRegistry
}

func (t *listCapabilitiesTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "system.list_capabilities",
		Description: "Returns a full list of all tools and capabilities available to the agent, even those currently filtered by the routing layer. Use this if you suspect a tool exists but cannot see it in your current tool list.",
		InputSchema: provider.ToolParameter{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func (t *listCapabilitiesTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	if t.registry == nil {
		return "Error: Tool registry not available.", nil
	}

	defs := t.registry.Definitions()
	if len(defs) == 0 {
		return "No tools registered.", nil
	}

	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString("## All Agent Capabilities\n")
	sb.WriteString("Total tools registered: " + fmt.Sprint(len(defs)) + "\n\n")

	for _, n := range names {
		sb.WriteString("- " + n + "\n")
	}

	sb.WriteString("\nTo see details for a specific tool, use `find_tools` with the tool name as a query.")
	return sb.String(), nil
}
