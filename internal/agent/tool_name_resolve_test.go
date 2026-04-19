package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/provider"
)

func TestResolveCatalogToolName_bedrockAlias(t *testing.T) {
	reg := NewToolRegistry()
	reg.Register(mockTool{name: "devops.github.list_prs"})
	r := &Runner{tools: reg}

	got := r.resolveCatalogToolName("devops_github_list_prs")
	if got != "devops.github.list_prs" {
		t.Fatalf("resolve: got %q", got)
	}
}

type mockTool struct{ name string }

func (m mockTool) Definition() provider.ToolDef {
	return provider.ToolDef{Name: m.name, Description: "x", InputSchema: provider.ToolParameter{Type: "object"}}
}

func (m mockTool) Execute(_ context.Context, _ json.RawMessage) (string, error) { return "", nil }
