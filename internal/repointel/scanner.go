package repointel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/devops/pipeline"
	"github.com/opsintelligence/opsintelligence/internal/provider"
	"go.uber.org/zap"
)

// ── Scanner ───────────────────────────────────────────────────────────────────

// Scanner runs a CVE and bottleneck analysis on an already-indexed repo,
// using the LLMRouter so that rate limits and circuit breakers are respected.
type Scanner struct {
	router *pipeline.LLMRouter
	log    *zap.Logger
}

// NewScanner constructs a Scanner.
func NewScanner(router *pipeline.LLMRouter, log *zap.Logger) *Scanner {
	return &Scanner{router: router, log: log}
}

// Scan analyses the RepoMemory and returns a ScanResult.
// The caller is responsible for persisting the result.
func (s *Scanner) Scan(ctx context.Context, entry RepoEntry, mem *RepoMemory) (*ScanResult, error) {
	if s.log != nil {
		s.log.Info("scanning repo", zap.String("repo", entry.ID))
	}

	route, err := s.router.Route(ctx, 999, []string{"go.mod", "Dockerfile"})
	if err != nil {
		return nil, fmt.Errorf("scanner: route LLM: %w", err)
	}

	result, err := s.scanWithLLM(ctx, route, entry, mem)
	if err != nil {
		s.router.RecordFailure(route.ProviderID)
		return nil, fmt.Errorf("scanner: LLM call: %w", err)
	}
	s.router.RecordSuccess(route.ProviderID)

	result.RepoID = entry.ID
	result.ScannedAt = time.Now()

	if s.log != nil {
		s.log.Info("scan complete",
			zap.String("repo", entry.ID),
			zap.String("risk", result.RiskLevel),
			zap.Int("cves", len(result.CVEs)),
			zap.Int("bottlenecks", len(result.Bottlenecks)),
		)
	}
	return result, nil
}

// ── LLM scan ─────────────────────────────────────────────────────────────────

func (s *Scanner) scanWithLLM(
	ctx context.Context,
	route pipeline.RouteResult,
	entry RepoEntry,
	mem *RepoMemory,
) (*ScanResult, error) {
	prompt := buildScanPrompt(entry, mem)

	req := &provider.CompletionRequest{
		Model: route.Model,
		// 2048 was too tight: scan responses include CVEs + bottlenecks +
		// suggestions, and a real repo regularly blows past it. Gemini
		// stops mid-JSON, producing "unexpected end of JSON input".
		MaxTokens:    8192,
		SystemPrompt: scanSystemPrompt,
		Messages: []provider.Message{
			{
				Role: provider.RoleUser,
				Content: []provider.ContentPart{
					{Type: provider.ContentTypeText, Text: prompt},
				},
			},
		},
	}

	resp, err := route.Provider.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	// If the provider tells us the response was cut off at the token
	// limit, return a clear error instead of a generic JSON parse fail
	// — that way the TUI surfaces "raise MaxTokens" rather than a
	// confusing "raw: { risk_…" trail-off.
	if resp.FinishReason == provider.FinishReasonLength {
		return nil, fmt.Errorf("scanner: LLM response truncated at token limit (model=%s, finish_reason=length); raise MaxTokens or shrink prompt", route.Model)
	}

	return parseScanJSON(resp.Text())
}

const scanSystemPrompt = `You are a security and reliability expert performing a code intelligence scan.

Given the repo metadata and codebase summary, identify:
1. Known or likely CVEs in listed dependencies (use your training knowledge).
2. Performance and reliability bottlenecks in the architecture.
3. Architecture-level improvement suggestions.

Return ONLY a JSON object matching this schema exactly:

{
  "risk_level": "critical|high|medium|low|info",
  "summary": "2-3 sentence executive summary of the risk posture",
  "cves": [
    {
      "severity": "critical|high|medium|low",
      "package": "package-name",
      "version": "version string or empty",
      "description": "what is vulnerable and why",
      "fix": "recommended remediation",
      "cve_ids": ["CVE-2024-XXXX"]
    }
  ],
  "bottlenecks": [
    {
      "severity": "high|medium|low",
      "location": "file path or component name",
      "description": "description of the bottleneck",
      "fix": "recommended fix"
    }
  ],
  "suggestions": [
    {
      "priority": "high|medium|low",
      "area": "e.g. caching, observability, API design",
      "suggestion": "specific actionable suggestion"
    }
  ]
}

Return ONLY the JSON. No markdown fences. No explanation.`

func buildScanPrompt(entry RepoEntry, mem *RepoMemory) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Repo: %s/%s\n", entry.Owner, entry.Name))
	if entry.Description != "" {
		sb.WriteString("Description: " + entry.Description + "\n")
	}
	sb.WriteString("\n## Codebase Summary\n")
	if mem != nil {
		sb.WriteString("Architecture: " + mem.Architecture + "\n")
		sb.WriteString("Primary language: " + mem.PrimaryLang + "\n")
		if len(mem.Languages) > 0 {
			sb.WriteString("Languages: " + strings.Join(mem.Languages, ", ") + "\n")
		}
		if len(mem.Dependencies) > 0 {
			sb.WriteString("\n### Dependencies\n")
			for _, d := range mem.Dependencies {
				sb.WriteString(fmt.Sprintf("- %s %s (%s)\n", d.Name, d.Version, d.Purpose))
			}
		}
		if len(mem.CommonIssues) > 0 {
			sb.WriteString("\n### Known Issue Patterns\n")
			for _, issue := range mem.CommonIssues {
				sb.WriteString("- " + issue + "\n")
			}
		}
		if mem.CISummary != "" {
			sb.WriteString("\nCI/CD: " + mem.CISummary + "\n")
		}
	}
	sb.WriteString("\nIdentify CVEs, bottlenecks, and architecture suggestions as described. Return JSON.")
	return sb.String()
}

// parseScanJSON unmarshals the LLM response into a ScanResult.
func parseScanJSON(raw string) (*ScanResult, error) {
	s := strings.TrimSpace(raw)
	// Strip markdown fences if present.
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}

	var out struct {
		RiskLevel   string `json:"risk_level"`
		Summary     string `json:"summary"`
		CVEs        []struct {
			Severity    string   `json:"severity"`
			Package     string   `json:"package"`
			Version     string   `json:"version"`
			Description string   `json:"description"`
			Fix         string   `json:"fix"`
			CVEIDs      []string `json:"cve_ids"`
		} `json:"cves"`
		Bottlenecks []struct {
			Severity    string `json:"severity"`
			Location    string `json:"location"`
			Description string `json:"description"`
			Fix         string `json:"fix"`
		} `json:"bottlenecks"`
		Suggestions []struct {
			Priority   string `json:"priority"`
			Area       string `json:"area"`
			Suggestion string `json:"suggestion"`
		} `json:"suggestions"`
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("parseScanJSON: %w (raw: %.200s)", err, s)
	}

	result := &ScanResult{
		RiskLevel: out.RiskLevel,
		Summary:   out.Summary,
	}
	if result.RiskLevel == "" {
		result.RiskLevel = "info"
	}
	for _, c := range out.CVEs {
		result.CVEs = append(result.CVEs, CVEFinding{
			Severity:    c.Severity,
			Package:     c.Package,
			Version:     c.Version,
			Description: c.Description,
			Fix:         c.Fix,
			CVEIDs:      c.CVEIDs,
		})
	}
	for _, b := range out.Bottlenecks {
		result.Bottlenecks = append(result.Bottlenecks, BottleneckFinding{
			Severity:    b.Severity,
			Location:    b.Location,
			Description: b.Description,
			Fix:         b.Fix,
		})
	}
	for _, sg := range out.Suggestions {
		result.Suggestions = append(result.Suggestions, ArchitectureSuggestion{
			Priority:   sg.Priority,
			Area:       sg.Area,
			Suggestion: sg.Suggestion,
		})
	}
	return result, nil
}
