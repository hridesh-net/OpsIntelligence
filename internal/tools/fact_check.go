package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/memory"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// FactCheckTool verifies a claim against indexed semantic memory and returns
// grounding evidence with a confidence rating.  Call this before stating any
// specific fact, number, version, or technical detail to avoid hallucination.
type FactCheckTool struct {
	EmbedFn  func(ctx context.Context, text string) ([]float32, error)
	SearchFn func(ctx context.Context, vec []float32, limit int) ([]memory.Document, error)
}

func (t FactCheckTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "fact_check",
		Description: "Verify a specific claim against indexed knowledge and semantic memory. " +
			"Returns grounding evidence and a confidence level (HIGH / MEDIUM / LOW / NONE). " +
			"Use before stating facts, versions, numbers, or technical details that are not already " +
			"backed by tool outputs in this conversation.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"claim": map[string]any{
					"type":        "string",
					"description": "The specific claim or fact to verify (be precise and concise)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of evidence chunks to retrieve (default 5)",
				},
			},
			Required: []string{"claim"},
		},
	}
}

func (t FactCheckTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var args struct {
		Claim string `json:"claim"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Claim) == "" {
		return "fact_check: claim is empty", nil
	}
	if args.Limit <= 0 {
		args.Limit = 5
	}

	vec, err := t.EmbedFn(ctx, args.Claim)
	if err != nil {
		return fmt.Sprintf("fact_check: embedding failed (%v) — verify this claim with web_search or tool outputs before stating it.", err), nil
	}

	docs, err := t.SearchFn(ctx, vec, args.Limit)
	if err != nil || len(docs) == 0 {
		return fmt.Sprintf(
			"fact_check: CONFIDENCE=NONE — no supporting evidence found in knowledge base for %q.\n"+
				"Do NOT state this as fact. Verify with web_search, tool outputs, or ask the user.", args.Claim), nil
	}

	topScore := docs[0].Score
	confidence := confidenceLabel(topScore)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("fact_check for: %q\n", args.Claim))
	sb.WriteString(fmt.Sprintf("CONFIDENCE=%s (top similarity score: %.3f)\n\n", confidence, topScore))
	sb.WriteString("Evidence:\n")
	for i, d := range docs {
		excerpt := strings.TrimSpace(d.Content)
		if len(excerpt) > 600 {
			excerpt = excerpt[:600] + "…"
		}
		sb.WriteString(fmt.Sprintf("[%d] score=%.3f  source=%s\n%s\n\n", i+1, d.Score, d.Source, excerpt))
	}

	if confidence == "LOW" || confidence == "NONE" {
		sb.WriteString("⚠ Low confidence — do not assert this claim without further verification.\n")
	}
	return sb.String(), nil
}

func confidenceLabel(score float32) string {
	switch {
	case score >= 0.88:
		return "HIGH"
	case score >= 0.72:
		return "MEDIUM"
	case score >= 0.55:
		return "LOW"
	default:
		return "NONE"
	}
}
