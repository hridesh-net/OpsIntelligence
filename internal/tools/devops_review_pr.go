package tools

// devops_review_pr.go implements devops.github.review_pr — a first-class
// binary tool that fetches a PR's metadata and diff, calls the configured LLM
// to produce severity-graded findings with file:line anchors, validates those
// anchors against the actual diff, and submits a formal GitHub review with
// inline line-level comments in a single API call.
//
// No chain runner, no skill markdown, no shell subprocess.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/devops/github"
	"github.com/opsintelligence/opsintelligence/internal/devops/sandbox"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// ─────────────────────────────────────────────────────────────────────────────
// Tool struct & registration
// ─────────────────────────────────────────────────────────────────────────────

type githubReviewPRTool struct {
	c                *github.Client
	defaultOrg       string
	prov             provider.Provider
	model            string
	allowDraftReview bool // from devops.github.allow_draft_review
	sandbox          *sandbox.Runner // nil = pipeline sandbox disabled
}

func (t *githubReviewPRTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "devops.github.review_pr",
		Description: "Full inline PR review in one call: fetches the PR metadata and unified diff, " +
			"calls the configured LLM to identify severity-graded findings with exact file:line anchors, " +
			"validates those anchors against the real diff, then submits a formal GitHub review with " +
			"inline line-level comments (APPROVE / REQUEST_CHANGES / COMMENT). " +
			"Use instead of chain_run+submit_review when you want a single, atomic review with no intermediate steps. " +
			"Accepts a full GitHub PR URL via pr_url (e.g. https://github.com/owner/repo/pull/42) — " +
			"owner, repo, and number are extracted automatically in that case. " +
			"Requires the configured GitHub token to have pull_requests:write on the target repo.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"pr_url": map[string]any{
					"type":        "string",
					"description": "Full GitHub PR URL (e.g. https://github.com/owner/repo/pull/42). When provided, owner/repo/number are extracted automatically — you do not need to supply them separately.",
				},
				"owner": map[string]any{
					"type":        "string",
					"description": "Org or user owning the repo (default: devops.github.default_org). Ignored when pr_url is provided.",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name. May include owner/ prefix. Ignored when pr_url is provided.",
				},
				"number": map[string]any{
					"description": "Pull request number (integer or string). Ignored when pr_url is provided.",
				},
				"focus": map[string]any{
					"type":        "string",
					"description": "Optional review focus: 'security', 'performance', 'correctness', or free-text. Defaults to general code quality.",
				},
				"dry_run": map[string]any{
					"type":        "boolean",
					"description": "If true, return the review payload as JSON without posting it to GitHub.",
				},
			},
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Execute
// ─────────────────────────────────────────────────────────────────────────────

func (t *githubReviewPRTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	// Use json.Number for the number field so the LLM can pass it as either
	// an integer (42) or a string ("42") without causing an unmarshal error.
	var raw struct {
		Owner  string          `json:"owner"`
		Repo   string          `json:"repo"`
		Number json.Number     `json:"number"`
		Focus  string          `json:"focus"`
		DryRun bool            `json:"dry_run"`
		PRURL  string          `json:"pr_url"` // convenience: full GitHub PR URL
	}
	if err := json.Unmarshal(input, &raw); err != nil {
		return "", fmt.Errorf("review_pr: invalid input: %w", err)
	}

	owner := raw.Owner
	repo := raw.Repo
	focus := raw.Focus
	dryRun := raw.DryRun

	// ── Resolve from a full PR URL when provided ─────────────────────────────
	// Handles inputs like pr_url="https://github.com/org/repo/pull/42" or
	// when the LLM mistakenly puts a URL in the repo field.
	for _, candidate := range []string{raw.PRURL, repo, owner} {
		if o, r, n, ok := parsePRURL(candidate); ok {
			if owner == "" {
				owner = o
			}
			if repo == "" || candidate == repo || candidate == owner {
				repo = r
			}
			if raw.Number == "" {
				raw.Number = json.Number(strconv.Itoa(n))
			}
			break
		}
	}

	// ── Parse PR number — accept int or quoted string ─────────────────────────
	var number int
	if raw.Number != "" {
		n, err := strconv.Atoi(strings.TrimSpace(raw.Number.String()))
		if err != nil {
			return "", fmt.Errorf("review_pr: number %q is not a valid PR number", raw.Number)
		}
		number = n
	}

	var err error
	owner, repo, err = resolveOwnerRepo(owner, repo, t.defaultOrg)
	if err != nil {
		return "", err
	}
	if number < 1 {
		return "", fmt.Errorf("review_pr: number must be a positive PR number (got %q)", raw.Number)
	}
	if t.prov == nil {
		return "", fmt.Errorf("review_pr: no LLM provider configured")
	}

	// Alias back so the rest of the function body doesn't need changing.
	a := struct {
		Owner  string
		Repo   string
		Number int
		Focus  string
		DryRun bool
	}{owner, repo, number, focus, dryRun}

	// ── Step 1: fetch PR metadata ────────────────────────────────────────────
	pr, err := t.c.GetPullRequest(ctx, a.Owner, a.Repo, a.Number)
	if err != nil {
		return "", fmt.Errorf("review_pr: fetch pr: %w", err)
	}
	if pr.State == "closed" {
		return "", fmt.Errorf("review_pr: PR #%d is already closed", a.Number)
	}
	if pr.Draft && !t.allowDraftReview {
		// Return a clean informational result (not an error) so the task
		// status shows "completed" rather than "failed". The review pool
		// will still log that it was skipped.
		out := map[string]any{
			"skipped": true,
			"reason":  fmt.Sprintf("PR #%d is a draft — no review posted (set allow_draft_review: true in config to review drafts)", a.Number),
			"pr_url":  pr.HTMLURL,
		}
		b, _ := json.Marshal(out)
		return string(b), nil
	}

	// ── Step 2: fetch per-file diffs ─────────────────────────────────────────
	files, err := t.c.GetPullRequestFiles(ctx, a.Owner, a.Repo, a.Number)
	if err != nil {
		return "", fmt.Errorf("review_pr: fetch pr files: %w", err)
	}
	if len(files) == 0 {
		return `{"message":"no changed files found in PR"}`, nil
	}

	// Build the annotated diff string and the valid-lines index simultaneously.
	annotated, validLines := buildAnnotatedDiff(files)

	// ── Step 2.5: run CI pipeline sandbox (optional) ─────────────────────────
	var sandboxResult *sandbox.Result
	if t.sandbox != nil {
		sandboxResult, _ = t.sandbox.Run(ctx, a.Owner, a.Repo, pr.Head.Ref)
		// errors are non-fatal; sandboxResult.Skipped=true on soft failures
	}

	// ── Step 3: call LLM for analysis ────────────────────────────────────────
	findings, err := t.analyzeWithLLM(ctx, pr, annotated, a.Focus, sandboxResult)
	if err != nil {
		return "", fmt.Errorf("review_pr: llm analysis: %w", err)
	}

	// ── Step 4: validate findings against actual diff lines ──────────────────
	var comments []github.ReviewComment
	var skipped []string
	for _, f := range findings {
		if f.Path == "" || f.Line < 1 {
			skipped = append(skipped, fmt.Sprintf("%s:%d (no anchor)", f.Path, f.Line))
			continue
		}
		lineSet, ok := validLines[f.Path]
		if !ok {
			skipped = append(skipped, fmt.Sprintf("%s:%d (file not in diff)", f.Path, f.Line))
			continue
		}
		if !lineSet[f.Line] {
			// Clamp to the nearest valid line in the same file rather than
			// dropping the finding entirely.
			nearest := nearestLine(lineSet, f.Line)
			if nearest == 0 {
				skipped = append(skipped, fmt.Sprintf("%s:%d (no valid lines)", f.Path, f.Line))
				continue
			}
			f.Line = nearest
		}
		comments = append(comments, github.ReviewComment{
			Path: f.Path,
			Line: f.Line,
			Side: "RIGHT",
			Body: formatFindingBody(f),
		})
	}

	// ── Step 5: decide verdict & compose top-level body ───────────────────────
	event, body := buildVerdict(findings, skipped, pr, a.Owner, a.Repo, sandboxResult)

	// ── Step 6: dry-run short-circuit ────────────────────────────────────────
	if a.DryRun {
		payload := map[string]any{
			"event":    event,
			"body":     body,
			"comments": comments,
			"skipped":  skipped,
		}
		b, _ := json.MarshalIndent(payload, "", "  ")
		return string(b), nil
	}

	// ── Step 7: submit review ────────────────────────────────────────────────
	rev := github.ReviewRequest{
		CommitID: pr.Head.SHA,
		Body:     body,
		Event:    event,
		Comments: comments,
	}
	resp, err := t.c.CreateReview(ctx, a.Owner, a.Repo, a.Number, rev)
	if err != nil {
		return "", fmt.Errorf("review_pr: submit review: %w", err)
	}

	out := map[string]any{
		"review_id":        resp.ID,
		"review_url":       resp.HTMLURL,
		"verdict":          event,
		"inline_comments":  len(comments),
		"findings_total":   len(findings),
		"findings_skipped": len(skipped),
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Diff parsing helpers
// ─────────────────────────────────────────────────────────────────────────────

// buildAnnotatedDiff converts the per-file PRFile patches into:
//   - annotated: a compact string that shows each changed line with its
//     new-file line number, suitable for inclusion in the LLM prompt.
//   - validLines: map[filePath]map[lineNumber]bool of lines the LLM may
//     anchor comments to (only added/modified RIGHT-side lines).
func buildAnnotatedDiff(files []github.PRFile) (annotated string, validLines map[string]map[int]bool) {
	validLines = make(map[string]map[int]bool)
	const maxAnnotatedBytes = 24_000
	var sb strings.Builder

	for _, f := range files {
		if f.Patch == "" {
			// binary or very large file
			sb.WriteString(fmt.Sprintf("--- %s (no patch: %s, +%d/-%d) ---\n", f.Filename, f.Status, f.Additions, f.Deletions))
			continue
		}

		header := fmt.Sprintf("\n### %s (+%d/-%d)\n", f.Filename, f.Additions, f.Deletions)
		if sb.Len()+len(header) > maxAnnotatedBytes {
			sb.WriteString("\n[diff truncated]\n")
			break
		}
		sb.WriteString(header)

		lineSet := make(map[int]bool)
		newLine := 0 // current line number in the new file

		for _, raw := range strings.Split(f.Patch, "\n") {
			if strings.HasPrefix(raw, "@@") {
				// e.g. "@@ -10,6 +15,8 @@"
				newLine = parseHunkNewStart(raw)
				sb.WriteString(raw + "\n")
				continue
			}
			if newLine == 0 {
				continue
			}
			switch {
			case strings.HasPrefix(raw, "+"):
				// Added line on the RIGHT side — annotate with real line number.
				annotated := fmt.Sprintf("%5d + %s", newLine, raw[1:])
				if sb.Len()+len(annotated)+1 <= maxAnnotatedBytes {
					sb.WriteString(annotated + "\n")
				}
				lineSet[newLine] = true
				newLine++
			case strings.HasPrefix(raw, "-"):
				// Removed line — belongs to LEFT side; no RIGHT-side line increment.
				if sb.Len()+len(raw)+1 <= maxAnnotatedBytes {
					sb.WriteString(fmt.Sprintf("      - %s\n", raw[1:]))
				}
			default:
				// Context line — present in both sides.
				if sb.Len()+len(raw)+1 <= maxAnnotatedBytes {
					sb.WriteString(fmt.Sprintf("%5d   %s\n", newLine, raw))
				}
				newLine++
			}
		}

		if len(lineSet) > 0 {
			validLines[f.Filename] = lineSet
		}
	}

	return sb.String(), validLines
}

// parseHunkNewStart extracts the starting new-file line number from a unified
// diff hunk header such as "@@ -10,6 +15,8 @@ func foo()".
func parseHunkNewStart(hunk string) int {
	// Find "+N" or "+N,M" between the @@ markers.
	start := strings.Index(hunk, "+")
	if start < 0 {
		return 0
	}
	rest := hunk[start+1:]
	end := strings.IndexAny(rest, ", @\t\n")
	if end < 0 {
		end = len(rest)
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return n
}

// nearestLine returns the valid line number in lineSet closest to target,
// or 0 if lineSet is empty.
func nearestLine(lineSet map[int]bool, target int) int {
	best, bestDist := 0, int(^uint(0)>>1) // MaxInt
	for line := range lineSet {
		d := target - line
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best, bestDist = line, d
		}
	}
	return best
}

// ─────────────────────────────────────────────────────────────────────────────
// LLM analysis
// ─────────────────────────────────────────────────────────────────────────────

// prFinding is a single issue the LLM identified in the diff.
type prFinding struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Severity string `json:"severity"` // critical | high | medium | nit
	Issue    string `json:"issue"`
	Fix      string `json:"fix"`
}

const reviewSystemPrompt = `You are a senior software engineer performing a pull request code review.
Your job is to identify real, grounded issues in the diff you are shown.

Rules:
- Only report issues present in the diff lines shown. Do not invent problems.
- Each finding MUST cite the exact new-file line number shown in the annotated diff (format: "NNNNN + <code>").
- Severity levels: critical (security, data loss, auth bypass), high (correctness bug, silent failure),
  medium (maintainability, error handling gap, perf regression on hot path), nit (style, naming, minor).
- Skip issues a linter or type-checker catches automatically (formatting, unused imports, type errors).
- Skip pre-existing issues on context lines (no leading "+").
- If you find no issues, return an empty JSON array [].

Respond with ONLY a JSON array — no prose, no markdown fences, no extra keys:
[
  {
    "path": "relative/path/to/file.ext",
    "line": <integer new-file line number>,
    "severity": "critical|high|medium|nit",
    "issue": "<one sentence: what is wrong>",
    "fix": "<one sentence: concrete remediation>"
  }
]`

func (t *githubReviewPRTool) analyzeWithLLM(
	ctx context.Context,
	pr *github.PullRequest,
	annotatedDiff string,
	focus string,
	pipelineResult *sandbox.Result,
) ([]prFinding, error) {
	focusLine := "general code quality"
	if strings.TrimSpace(focus) != "" {
		focusLine = strings.TrimSpace(focus)
	}

	userMsg := fmt.Sprintf(
		"PR #%d — %s\nAuthor: %s\nBase: %s ← Head: %s\nFocus: %s\n\nDescription:\n%s\n\nAnnotated diff (line numbers are new-file RIGHT-side positions):\n%s",
		pr.Number, pr.Title, pr.User.Login,
		pr.Base.Ref, pr.Head.Ref,
		focusLine,
		truncateStr(pr.Body, 1500),
		annotatedDiff,
	)
	if pipelineResult != nil && !pipelineResult.Skipped {
		userMsg += "\n\n" + sandbox.FormatForLLM(pipelineResult)
	}

	req := &provider.CompletionRequest{
		Model:        t.model,
		SystemPrompt: reviewSystemPrompt,
		Messages:     []provider.Message{provider.NewTextMessage(provider.RoleUser, userMsg)},
		MaxTokens:    2048,
		Temperature:  0.15, // low temp → more deterministic, fewer hallucinations
	}

	resp, err := t.prov.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	raw := strings.TrimSpace(resp.Text())
	// Strip accidental markdown fences if the LLM adds them.
	raw = stripJSONFences(raw)

	var findings []prFinding
	if err := json.Unmarshal([]byte(raw), &findings); err != nil {
		// Return what we have as a diagnostic instead of crashing.
		return nil, fmt.Errorf("parse llm findings (raw=%q): %w", truncateStr(raw, 200), err)
	}
	return findings, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Formatting helpers
// ─────────────────────────────────────────────────────────────────────────────

// formatFindingBody produces the GitHub-flavoured Markdown body for one inline
// review comment.
func formatFindingBody(f prFinding) string {
	sev := severityEmoji(f.Severity)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s **%s** — %s\n\n", sev, capitalise(f.Severity), f.Issue))
	if strings.TrimSpace(f.Fix) != "" {
		sb.WriteString(fmt.Sprintf("**Suggested fix:** %s", f.Fix))
	}
	return sb.String()
}

func severityEmoji(s string) string {
	switch strings.ToLower(s) {
	case "critical":
		return "🔴"
	case "high":
		return "🟠"
	case "medium":
		return "🟡"
	case "nit":
		return "💬"
	default:
		return "⚠️"
	}
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// buildVerdict decides the review event (APPROVE / REQUEST_CHANGES / COMMENT)
// and composes the top-level review body based on the findings.
func buildVerdict(findings []prFinding, skipped []string, pr *github.PullRequest, owner, repo string, pipelineResult *sandbox.Result) (event, body string) {
	var criticals, highs, mediums, nits int
	for _, f := range findings {
		switch strings.ToLower(f.Severity) {
		case "critical":
			criticals++
		case "high":
			highs++
		case "medium":
			mediums++
		default:
			nits++
		}
	}

	switch {
	case criticals > 0 || highs > 0:
		event = "REQUEST_CHANGES"
	case len(findings) > 0:
		event = "COMMENT"
	default:
		event = "APPROVE"
	}

	var sb strings.Builder
	switch event {
	case "APPROVE":
		sb.WriteString("## ✅ Verdict: Ship\n\nNo issues found in the changed lines. Looks good!\n")
	case "REQUEST_CHANGES":
		sb.WriteString("## 🔴 Verdict: Hold\n\nFound blocking issues that must be addressed before merge.\n")
	default:
		sb.WriteString("## 🟡 Verdict: Hold-with-fixes\n\nNon-blocking issues — please review inline comments.\n")
	}

	sb.WriteString(fmt.Sprintf("\n**PR:** [%s/%s#%d](%s)  \n", owner, repo, pr.Number, pr.HTMLURL))
	sb.WriteString(fmt.Sprintf("**Author:** @%s | **Base:** `%s` ← `%s`\n\n", pr.User.Login, pr.Base.Ref, pr.Head.Ref))

	if len(findings) > 0 {
		sb.WriteString("### Summary\n")
		if criticals > 0 {
			sb.WriteString(fmt.Sprintf("- 🔴 Critical: %d\n", criticals))
		}
		if highs > 0 {
			sb.WriteString(fmt.Sprintf("- 🟠 High: %d\n", highs))
		}
		if mediums > 0 {
			sb.WriteString(fmt.Sprintf("- 🟡 Medium: %d\n", mediums))
		}
		if nits > 0 {
			sb.WriteString(fmt.Sprintf("- 💬 Nit: %d\n", nits))
		}
		sb.WriteString("\nSee inline comments for details.\n")
	}

	if len(skipped) > 0 {
		sb.WriteString(fmt.Sprintf("\n<details><summary>%d finding(s) dropped (line not in diff)</summary>\n\n", len(skipped)))
		for _, s := range skipped {
			sb.WriteString(fmt.Sprintf("- %s\n", s))
		}
		sb.WriteString("</details>\n")
	}

	if pipelineResult != nil && !pipelineResult.Skipped {
		sb.WriteString(sandbox.FormatForReviewBody(pipelineResult))
		// Pipeline failure: don't approve even if LLM finds no code issues
		if !pipelineResult.Succeeded && event == "APPROVE" {
			event = "COMMENT"
		}
	}

	sb.WriteString("\n---\n🤖 Generated with [OpsIntelligence](https://github.com/hridesh-net/OpsIntelligence) · `devops.github.review_pr`\n")

	return event, sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// String utilities
// ─────────────────────────────────────────────────────────────────────────────

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	// Strip ```json … ``` or ``` … ```
	for _, fence := range []string{"```json", "```"} {
		if strings.HasPrefix(s, fence) {
			s = strings.TrimPrefix(s, fence)
			s = strings.TrimSuffix(s, "```")
			s = strings.TrimSpace(s)
			break
		}
	}
	return s
}

// parsePRURL parses a GitHub PR URL of the form:
//
//	https://github.com/<owner>/<repo>/pull/<number>
//
// Returns (owner, repo, number, true) on success or ("", "", 0, false) otherwise.
// Also handles variants without the https:// scheme or with trailing slashes/fragments.
func parsePRURL(raw string) (owner, repo string, number int, ok bool) {
	s := strings.TrimSpace(raw)
	// Strip scheme.
	for _, prefix := range []string{"https://", "http://"} {
		s = strings.TrimPrefix(s, prefix)
	}
	// Must start with github.com/
	if !strings.HasPrefix(s, "github.com/") {
		return
	}
	s = strings.TrimPrefix(s, "github.com/")
	// Strip fragment/query.
	if i := strings.IndexAny(s, "#?"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	// Expect: <owner>/<repo>/pull/<number>
	parts := strings.Split(s, "/")
	if len(parts) < 4 {
		return
	}
	if parts[2] != "pull" {
		return
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n < 1 {
		return
	}
	return parts[0], parts[1], n, true
}
