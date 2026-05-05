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

// PRReviewMethodologyGetTool reads the configured PR review methodology from disk.
type PRReviewMethodologyGetTool struct{ Path string }

func (t PRReviewMethodologyGetTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "pr_review.methodology.get",
		Description: "Read the current PR review methodology / ruleset configured for this agent. " +
			"Returns the markdown content of methodology.md, or a notice if none is configured. " +
			"Use pr_review.methodology.set to define or update the methodology.",
		InputSchema: provider.ToolParameter{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func (t PRReviewMethodologyGetTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	data, err := os.ReadFile(t.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return "No PR review methodology configured. Use pr_review.methodology.set to define one.", nil
		}
		return "", fmt.Errorf("read methodology: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "Methodology file exists but is empty. Use pr_review.methodology.set to configure it.", nil
	}
	return string(data), nil
}

// PRReviewMethodologySetTool writes or replaces the PR review methodology on disk.
// The content is injected into the pr_review agent's system prompt on every spawn.
type PRReviewMethodologySetTool struct{ Path string }

func (t PRReviewMethodologySetTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "pr_review.methodology.set",
		Description: "Write or replace the PR review methodology / ruleset. " +
			"The markdown content is stored in methodology.md and automatically injected " +
			"into the pr_review specialist's system prompt on every run. " +
			"Include review rules, focus areas, severity guidance, and any team conventions.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"content": map[string]any{
					"type": "string",
					"description": "Full markdown content of the PR review methodology " +
						"(rules, focus areas, severity guidance, team conventions, etc.).",
				},
			},
			Required: []string{"content"},
		},
	}
}

func (t PRReviewMethodologySetTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
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
		return "", fmt.Errorf("create methodology dir: %w", err)
	}
	if err := os.WriteFile(t.Path, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write methodology: %w", err)
	}
	return fmt.Sprintf(
		"PR review methodology saved (%d bytes). "+
			"It will be injected into the pr_review agent's system prompt on the next specialist spawn.",
		len(args.Content),
	), nil
}
