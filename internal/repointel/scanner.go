package repointel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/devops/pipeline"
	"github.com/opsintelligence/opsintelligence/internal/provider"
	"github.com/opsintelligence/opsintelligence/internal/repointel/cveclient"
	"go.uber.org/zap"
)

// ── Scanner ───────────────────────────────────────────────────────────────────

// Scanner runs a CVE and bottleneck analysis on an already-indexed repo,
// using the LLMRouter so that rate limits and circuit breakers are respected.
type Scanner struct {
	router *pipeline.LLMRouter
	log    *zap.Logger
	osv    *cveclient.Client // nil-safe; LLM-only path used when nil
}

// NewScanner constructs a Scanner. An OSV client is created by default so
// dependencies are checked against the live advisory feed; tests can replace
// it via SetOSVClient.
func NewScanner(router *pipeline.LLMRouter, log *zap.Logger) *Scanner {
	return &Scanner{router: router, log: log, osv: cveclient.NewClient()}
}

// SetOSVClient overrides the OSV client (nil disables the OSV pre-pass).
// Intended for tests; production callers can leave the default.
func (s *Scanner) SetOSVClient(c *cveclient.Client) { s.osv = c }

// Scan analyses the RepoMemory and returns a ScanResult.
// The caller is responsible for persisting the result.
func (s *Scanner) Scan(ctx context.Context, entry RepoEntry, mem *RepoMemory) (*ScanResult, error) {
	if s.log != nil {
		s.log.Info("scanning repo", zap.String("repo", entry.ID))
	}

	// Ground-truth pass: query OSV.dev for every parsed dependency before
	// going to the LLM. The LLM gets the real hits as context, so its job
	// shrinks from "recall CVEs from training data" to "explain and rank
	// what's actually known to affect this version." Anything OSV finds
	// gets Source: "osv"; anything new the LLM proposes gets Source: "llm".
	osvFindings := s.preScanOSV(ctx, mem)

	route, err := s.router.Route(ctx, 999, []string{"go.mod", "Dockerfile"})
	if err != nil {
		return nil, fmt.Errorf("scanner: route LLM: %w", err)
	}

	result, err := s.scanWithLLM(ctx, route, entry, mem, osvFindings)
	if err != nil {
		s.router.RecordFailure(route.ProviderID)
		return nil, fmt.Errorf("scanner: LLM call: %w", err)
	}
	s.router.RecordSuccess(route.ProviderID)

	result.RepoID = entry.ID
	result.ScannedAt = time.Now()
	result.CVEs = mergeCVEFindings(osvFindings, result.CVEs)

	if s.log != nil {
		s.log.Info("scan complete",
			zap.String("repo", entry.ID),
			zap.String("risk", result.RiskLevel),
			zap.Int("cves", len(result.CVEs)),
			zap.Int("osv_cves", len(osvFindings)),
			zap.Int("bottlenecks", len(result.Bottlenecks)),
		)
	}
	return result, nil
}

// preScanOSV walks the dependency list and queries the OSV ecosystem feed.
// Network errors are logged and swallowed — we never let an OSV outage stop
// a scan; the LLM-only path still produces a usable result.
func (s *Scanner) preScanOSV(ctx context.Context, mem *RepoMemory) []CVEFinding {
	if s.osv == nil || mem == nil || len(mem.Dependencies) == 0 {
		return nil
	}
	eco := cveclient.EcosystemFor(mem.PrimaryLang)
	if eco == "" {
		return nil
	}
	var out []CVEFinding
	for _, dep := range mem.Dependencies {
		if dep.Name == "" {
			continue
		}
		vulns, err := s.osv.QueryPackage(ctx, eco, dep.Name, dep.Version)
		if err != nil {
			if s.log != nil {
				s.log.Debug("osv query failed",
					zap.String("pkg", dep.Name), zap.String("ver", dep.Version),
					zap.Error(err))
			}
			continue
		}
		for _, v := range vulns {
			out = append(out, CVEFinding{
				Severity:      v.Severity,
				Package:       v.Package,
				Version:       v.Affected,
				Description:   firstNonEmpty(v.Summary, v.Details),
				Fix:           recommendedFix(v),
				CVEIDs:        cveIDs(v),
				Source:        "osv",
				References:    v.References,
				FixedVersions: v.FixedVersions,
				Ecosystem:     v.Ecosystem,
			})
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// cveIDs collects CVE-prefixed aliases plus the primary ID when it itself is
// a CVE. GHSA aliases get filtered out so the surfaced list stays portable.
func cveIDs(v cveclient.Vulnerability) []string {
	var out []string
	if strings.HasPrefix(v.ID, "CVE-") {
		out = append(out, v.ID)
	}
	for _, a := range v.Aliases {
		if strings.HasPrefix(a, "CVE-") {
			out = append(out, a)
		}
	}
	return out
}

func recommendedFix(v cveclient.Vulnerability) string {
	if len(v.FixedVersions) == 0 {
		return ""
	}
	return "upgrade to " + strings.Join(v.FixedVersions, " / ")
}

// mergeCVEFindings combines OSV hits with LLM-proposed CVEs. When the LLM
// rediscovers an OSV record (same package + same CVE alias) we keep the OSV
// version since it carries verified metadata.
func mergeCVEFindings(osv, llm []CVEFinding) []CVEFinding {
	seen := map[string]struct{}{}
	keyFor := func(f CVEFinding) string {
		if len(f.CVEIDs) > 0 {
			return strings.ToLower(f.Package) + "|" + strings.ToLower(f.CVEIDs[0])
		}
		return strings.ToLower(f.Package) + "|" + strings.ToLower(f.Description)
	}
	out := make([]CVEFinding, 0, len(osv)+len(llm))
	for _, f := range osv {
		out = append(out, f)
		seen[keyFor(f)] = struct{}{}
	}
	for _, f := range llm {
		if _, dup := seen[keyFor(f)]; dup {
			continue
		}
		if f.Source == "" {
			f.Source = "llm"
		}
		out = append(out, f)
	}
	return out
}

// ── LLM scan ─────────────────────────────────────────────────────────────────

func (s *Scanner) scanWithLLM(
	ctx context.Context,
	route pipeline.RouteResult,
	entry RepoEntry,
	mem *RepoMemory,
	osvHits []CVEFinding,
) (*ScanResult, error) {
	prompt := buildScanPrompt(entry, mem, osvHits)

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

func buildScanPrompt(entry RepoEntry, mem *RepoMemory, osvHits []CVEFinding) string {
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
	if len(osvHits) > 0 {
		// Anchor the model with the live OSV feed so it doesn't fabricate
		// CVEs from training data and so it explains real ones in context.
		sb.WriteString("\n### Known vulnerabilities from OSV.dev (ground truth — already detected)\n")
		for _, hit := range osvHits {
			ids := strings.Join(hit.CVEIDs, ", ")
			if ids == "" {
				ids = "(no CVE alias)"
			}
			fix := hit.Fix
			if fix == "" && len(hit.FixedVersions) > 0 {
				fix = "upgrade to " + strings.Join(hit.FixedVersions, " / ")
			}
			sb.WriteString(fmt.Sprintf("- %s@%s [%s] %s — %s. Fix: %s\n",
				hit.Package, hit.Version, hit.Severity, ids, hit.Description, fix))
		}
		sb.WriteString("\nThese are confirmed; you do not need to rediscover them. ")
		sb.WriteString("Focus the `cves` field on additional risks you can infer (e.g. vulnerable transitive deps, misconfigurations, or CVEs in libraries that OSV missed). ")
		sb.WriteString("Do not duplicate OSV entries.\n")
	}

	sb.WriteString("\nIdentify additional CVEs (beyond the OSV list above), bottlenecks, and architecture suggestions as described. Return JSON.")
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
