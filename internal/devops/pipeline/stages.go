package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/devops/github"
	"github.com/opsintelligence/opsintelligence/internal/devops/sandbox"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// ── Shared input ─────────────────────────────────────────────────────────────

// StageInput is passed to every stage. Immutable per pipeline run.
type StageInput struct {
	RunID     string
	Owner     string
	Repo      string // short name
	FullRepo  string // "owner/repo"
	Number    int
	HeadRef   string
	CommitSHA string
	PRURL     string
	Focus     string
	DryRun    bool

	// RepoContext is optional repo intelligence context injected into the LLM
	// review prompt. Set by the caller (e.g. tools/devops.go) when a Manager
	// has indexed the repo. Formatted markdown from RepoMemory.ReviewContext().
	RepoContext string
}

// ── Stage 1: Fetch ───────────────────────────────────────────────────────────

// FetchResult holds the PR metadata and processed diff from Stage 1.
type FetchResult struct {
	PR         *github.PullRequest
	Files      []github.PRFile
	Annotated  string
	ValidLines map[string]map[int]bool
	DiffLines  int // total added+modified lines (used by classifier)
	FilePaths  []string
}

// StageFetch fetches PR metadata and builds the annotated diff.
func StageFetch(ctx context.Context, in StageInput, ghClient *github.Client, agent *TraceAgent) (*FetchResult, error) {
	start := time.Now()
	result, err := doFetch(ctx, in, ghClient)
	ev := StageEvent{
		RunID:      in.RunID,
		Stage:      StageFetchName,
		StartedAt:  start,
		DurationMs: time.Since(start).Milliseconds(),
		CommitSHA:  in.CommitSHA,
		Repo:       in.FullRepo,
		PRURL:      in.PRURL,
		PRNumber:   in.Number,
	}
	if err != nil {
		ev.Success = false
		ev.Error = err.Error()
	} else {
		ev.Success = true
		out, _ := json.Marshal(map[string]any{
			"pr_title":   result.PR.Title,
			"files":      len(result.Files),
			"diff_lines": result.DiffLines,
		})
		ev.Output = out
		// Carry CommitSHA forward when caller didn't have it yet.
		if ev.CommitSHA == "" {
			ev.CommitSHA = result.PR.Head.SHA
		}
	}
	agent.Emit(ev)
	return result, err
}

func doFetch(ctx context.Context, in StageInput, c *github.Client) (*FetchResult, error) {
	pr, err := c.GetPullRequest(ctx, in.Owner, in.Repo, in.Number)
	if err != nil {
		return nil, fmt.Errorf("stage fetch: get PR: %w", err)
	}
	if pr.State == "closed" {
		return nil, fmt.Errorf("stage fetch: PR #%d is already closed", in.Number)
	}

	files, err := c.GetPullRequestFiles(ctx, in.Owner, in.Repo, in.Number)
	if err != nil {
		return nil, fmt.Errorf("stage fetch: get PR files: %w", err)
	}

	annotated, validLines := buildAnnotatedDiff(files)

	// Count diff lines and collect file paths for the classifier.
	var diffLines int
	paths := make([]string, 0, len(files))
	for _, f := range files {
		diffLines += f.Additions + f.Changes
		paths = append(paths, f.Filename)
	}

	commitSHA := pr.Head.SHA
	if in.CommitSHA != "" {
		commitSHA = in.CommitSHA
	}

	return &FetchResult{
		PR:         pr,
		Files:      files,
		Annotated:  annotated,
		ValidLines: validLines,
		DiffLines:  diffLines,
		FilePaths:  paths,
	}, fmt.Errorf("%w", setCommitSHA(&in, commitSHA)) // propagate SHA; safe nil check below
}

// setCommitSHA is a no-op helper that always returns nil; used to propagate
// the commit SHA into StageInput without a separate return value.
func setCommitSHA(in *StageInput, sha string) error {
	in.CommitSHA = sha
	return nil
}

// buildAnnotatedDiff builds the annotated diff and valid-lines index.
// Mirrors the logic in devops_review_pr.go but lives here to avoid an import cycle.
func buildAnnotatedDiff(files []github.PRFile) (string, map[string]map[int]bool) {
	validLines := make(map[string]map[int]bool)
	const maxAnnotatedBytes = 24_000
	var sb strings.Builder

	for _, f := range files {
		if f.Patch == "" {
			sb.WriteString(fmt.Sprintf("--- %s (no patch: %s, +%d/-%d) ---\n",
				f.Filename, f.Status, f.Additions, f.Deletions))
			continue
		}
		header := fmt.Sprintf("\n### %s (+%d/-%d)\n", f.Filename, f.Additions, f.Deletions)
		if sb.Len()+len(header) > maxAnnotatedBytes {
			sb.WriteString("\n[diff truncated]\n")
			break
		}
		sb.WriteString(header)

		lineSet := make(map[int]bool)
		newLine := 0

		for _, raw := range strings.Split(f.Patch, "\n") {
			if strings.HasPrefix(raw, "@@") {
				newLine = parseHunkNewStart(raw)
				sb.WriteString(raw + "\n")
				continue
			}
			if newLine == 0 {
				continue
			}
			switch {
			case strings.HasPrefix(raw, "+"):
				line := fmt.Sprintf("%5d + %s", newLine, raw[1:])
				if sb.Len()+len(line)+1 <= maxAnnotatedBytes {
					sb.WriteString(line + "\n")
				}
				lineSet[newLine] = true
				newLine++
			case strings.HasPrefix(raw, "-"):
				if sb.Len()+len(raw)+1 <= maxAnnotatedBytes {
					sb.WriteString(fmt.Sprintf("      - %s\n", raw[1:]))
				}
			default:
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

func parseHunkNewStart(hunk string) int {
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

// ── Stage 2: Analyse ─────────────────────────────────────────────────────────

// AnalyseResult holds the parsed CI config and extracted test commands.
type AnalyseResult struct {
	CIConfig *sandbox.CIConfig // may be nil if no CI detected
	Commands []TestCommand
}

// StageAnalyse fetches and parses the CI config to extract test commands.
func StageAnalyse(ctx context.Context, in StageInput, fetch *FetchResult, det *sandbox.Detector, agent *TraceAgent) (*AnalyseResult, error) {
	start := time.Now()
	result, err := doAnalyse(ctx, in, fetch, det)
	ev := StageEvent{
		RunID:      in.RunID,
		Stage:      StageAnalyseName,
		StartedAt:  start,
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		ev.Success = false
		ev.Error = err.Error()
	} else {
		ev.Success = true
		ciKind := "none"
		if result.CIConfig != nil {
			ciKind = string(result.CIConfig.Kind)
		}
		out, _ := json.Marshal(map[string]any{
			"ci_kind":  ciKind,
			"commands": len(result.Commands),
		})
		ev.Output = out
	}
	agent.Emit(ev)
	return result, err
}

func doAnalyse(ctx context.Context, in StageInput, fetch *FetchResult, det *sandbox.Detector) (*AnalyseResult, error) {
	if det == nil {
		return &AnalyseResult{}, nil
	}
	headRef := in.HeadRef
	if fetch != nil && fetch.PR != nil {
		headRef = fetch.PR.Head.Ref
	}
	ci, err := det.Detect(ctx, in.Owner, in.Repo, headRef)
	if err != nil || ci == nil || ci.Kind == sandbox.CIKindUnknown {
		return &AnalyseResult{}, nil
	}
	parser := &CIParser{}
	cmds := parser.Parse(ci)
	return &AnalyseResult{CIConfig: ci, Commands: cmds}, nil
}

// ── Stage 3: Sandbox ─────────────────────────────────────────────────────────

// SandboxResult wraps the sandbox runner result.
type SandboxResult struct {
	Inner     *sandbox.Result
	Commands  []TestCommand // from stage 2
	SubOutput map[string]SubprocessResult // command → subprocess result (when runner unavailable)
}

// SubprocessResult is the output of running a test command directly in a subprocess.
type SubprocessResult struct {
	Command  string
	ExitCode int
	Output   string // combined stdout+stderr, capped
	Duration time.Duration
}

const maxSubprocOutput = 8 * 1024 // 8 KB

// StageSandbox runs the CI pipeline sandbox. If Docker is unavailable and
// we have parsed test commands, it falls back to direct subprocess execution.
func StageSandbox(ctx context.Context, in StageInput, fetch *FetchResult, analyse *AnalyseResult, runner *sandbox.Runner, agent *TraceAgent) (*SandboxResult, error) {
	start := time.Now()
	result, err := doSandbox(ctx, in, fetch, analyse, runner)
	ev := StageEvent{
		RunID:      in.RunID,
		Stage:      StageSandboxName,
		StartedAt:  start,
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		ev.Success = false
		ev.Error = err.Error()
	} else {
		ev.Success = true
		passed := result.Inner == nil || result.Inner.Skipped || result.Inner.Succeeded
		out, _ := json.Marshal(map[string]any{
			"passed":         passed,
			"skipped":        result.Inner == nil || result.Inner.Skipped,
			"subprocess_cmds": len(result.SubOutput),
		})
		ev.Output = out
	}
	agent.Emit(ev)
	return result, err
}

func doSandbox(ctx context.Context, in StageInput, fetch *FetchResult, analyse *AnalyseResult, runner *sandbox.Runner) (*SandboxResult, error) {
	headRef := in.HeadRef
	if fetch != nil && fetch.PR != nil {
		headRef = fetch.PR.Head.Ref
	}

	// Try the full Docker-based sandbox runner first.
	if runner != nil {
		res, err := runner.Run(ctx, in.Owner, in.Repo, headRef)
		if err != nil {
			return nil, err
		}
		sr := &SandboxResult{Inner: res}
		if analyse != nil {
			sr.Commands = analyse.Commands
		}
		return sr, nil
	}

	// No sandbox runner (Docker unavailable or not configured).
	// Fall back to running extracted test commands directly in a subprocess
	// inside a temporary directory. This is shallow but gives real signal.
	sr := &SandboxResult{SubOutput: make(map[string]SubprocessResult)}
	if analyse == nil || len(analyse.Commands) == 0 {
		sr.Inner = &sandbox.Result{Skipped: true, SkipReason: "no sandbox runner and no test commands extracted"}
		return sr, nil
	}

	// Create a temp workspace — just for running commands, no repo checkout here.
	tmpDir, err := os.MkdirTemp("", "opsintel-subprocess-*")
	if err != nil {
		sr.Inner = &sandbox.Result{Skipped: true, SkipReason: "could not create temp dir: " + err.Error()}
		return sr, nil
	}
	defer os.RemoveAll(tmpDir)

	allPassed := true
	for _, tc := range analyse.Commands {
		res := runSubprocess(ctx, tc, tmpDir)
		sr.SubOutput[tc.Stage+":"+tc.Command] = res
		if res.ExitCode != 0 {
			allPassed = false
		}
	}
	inner := &sandbox.Result{
		Succeeded: allPassed,
	}
	sr.Inner = inner
	sr.Commands = analyse.Commands
	return sr, nil
}

func runSubprocess(ctx context.Context, tc TestCommand, dir string) SubprocessResult {
	shell := tc.Shell
	if shell == "" {
		shell = "bash"
	}
	start := time.Now()

	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var cmd *exec.Cmd
	switch shell {
	case "powershell", "pwsh":
		cmd = exec.CommandContext(cmdCtx, "powershell", "-Command", tc.Command)
	default:
		cmd = exec.CommandContext(cmdCtx, shell, "-c", tc.Command)
	}
	cmd.Dir = dir

	// Capture combined output, capped.
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = -1
		}
	}
	outStr := string(out)
	if len(outStr) > maxSubprocOutput {
		outStr = outStr[:maxSubprocOutput] + "\n[output truncated]"
	}
	return SubprocessResult{
		Command:  tc.Command,
		ExitCode: code,
		Output:   outStr,
		Duration: time.Since(start),
	}
}

// sandboxPassed returns true when the sandbox result indicates tests passed.
func sandboxPassed(sr *SandboxResult) bool {
	if sr == nil || sr.Inner == nil || sr.Inner.Skipped {
		return true // skipped = neutral
	}
	return sr.Inner.Succeeded
}

// ── Stage 4: Review (LLM) ────────────────────────────────────────────────────

// ReviewResult holds the LLM-generated review findings.
type ReviewResult struct {
	Findings   []prFinding
	Event      string // APPROVE | REQUEST_CHANGES | COMMENT
	Body       string // top-level review body (markdown)
	Comments   []github.ReviewComment
	ModelUsed  string
	IsLocal    bool
	ProviderID string
	Tokens     ProviderTokenUsage
	Tools      []string
	Skills     []string
}

// StageReview calls the LLM via the router to produce findings and comments.
func StageReview(ctx context.Context, in StageInput, fetch *FetchResult, sandboxRes *SandboxResult, router *LLMRouter, agent *TraceAgent) (*ReviewResult, error) {
	start := time.Now()
	result, err := doReview(ctx, in, fetch, sandboxRes, router)
	ev := StageEvent{
		RunID:      in.RunID,
		Stage:      StageReviewName,
		StartedAt:  start,
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		ev.Success = false
		ev.Error = err.Error()
		// ProviderID is not known on error path — failure recorded in doReview.
	} else {
		ev.Success = true
		ev.ModelUsed = result.ModelUsed
		ev.LocalIntel = result.IsLocal
		ev.Tools = result.Tools
		ev.Skills = result.Skills
		ev.Tokens = result.Tokens
		router.RecordSuccess(result.ProviderID)
		out, _ := json.Marshal(map[string]any{
			"findings": len(result.Findings),
			"verdict":  result.Event,
		})
		ev.Output = out
	}
	agent.Emit(ev)
	return result, err
}

func doReview(ctx context.Context, in StageInput, fetch *FetchResult, sandboxRes *SandboxResult, router *LLMRouter) (*ReviewResult, error) {
	if fetch == nil || fetch.PR == nil {
		return nil, fmt.Errorf("stage review: no PR metadata")
	}

	route, err := router.Route(ctx, fetch.DiffLines, fetch.FilePaths)
	if err != nil {
		return nil, fmt.Errorf("stage review: route: %w", err)
	}

	var sandboxInner *sandbox.Result
	if sandboxRes != nil {
		sandboxInner = sandboxRes.Inner
	}

	findings, tokens, err := callLLM(ctx, route.Provider, route.Model, fetch.PR, fetch.Annotated, in.Focus, in.RepoContext, sandboxInner)
	if err != nil {
		return nil, fmt.Errorf("stage review: llm: %w", err)
	}

	// Validate findings against the real diff.
	var comments []github.ReviewComment
	for _, f := range findings {
		if f.Path == "" || f.Line < 1 {
			continue
		}
		lineSet, ok := fetch.ValidLines[f.Path]
		if !ok {
			continue
		}
		if !lineSet[f.Line] {
			f.Line = nearestLine(lineSet, f.Line)
			if f.Line == 0 {
				continue
			}
		}
		comments = append(comments, github.ReviewComment{
			Path: f.Path,
			Line: f.Line,
			Side: "RIGHT",
			Body: formatFindingBody(f),
		})
	}

	event, body := buildVerdict(findings, fetch.PR, in.Owner, in.Repo, sandboxInner)

	return &ReviewResult{
		Findings:   findings,
		Event:      event,
		Body:       body,
		Comments:   comments,
		ModelUsed:  route.Model,
		IsLocal:    route.IsLocal,
		ProviderID: route.ProviderID,
		Tokens:     tokens,
	}, nil
}

// callLLM sends the review prompt to prov and parses findings.
func callLLM(ctx context.Context, prov provider.Provider, model string, pr *github.PullRequest, annotatedDiff, focus, repoContext string, pipelineResult *sandbox.Result) ([]prFinding, ProviderTokenUsage, error) {
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
	// Prepend repo intelligence context when available so the LLM understands
	// the codebase conventions and common issue patterns before seeing the diff.
	if repoContext != "" {
		userMsg = repoContext + "\n\n---\n\n" + userMsg
	}

	req := &provider.CompletionRequest{
		Model:        model,
		SystemPrompt: reviewSystemPrompt,
		Messages:     []provider.Message{provider.NewTextMessage(provider.RoleUser, userMsg)},
		MaxTokens:    2048,
		Temperature:  0.15,
	}

	resp, err := prov.Complete(ctx, req)
	if err != nil {
		return nil, ProviderTokenUsage{}, err
	}

	u := resp.Usage
	tokens := ProviderTokenUsage{
		Prompt:     u.PromptTokens,
		Completion: u.CompletionTokens,
		CacheRead:  u.CacheReadTokens,
		CacheWrite: u.CacheWriteTokens,
	}

	raw := strings.TrimSpace(resp.Text())
	raw = stripJSONFences(raw)

	var findings []prFinding
	if err := json.Unmarshal([]byte(raw), &findings); err != nil {
		return nil, tokens, fmt.Errorf("parse llm findings (raw=%q): %w", truncateStr(raw, 200), err)
	}
	return findings, tokens, nil
}

// ── Stage 5: Post ────────────────────────────────────────────────────────────

// PostResult records what was submitted to GitHub.
type PostResult struct {
	ReviewID   int64
	ReviewURL  string
	DryRun     bool
	Skipped    bool
	SkipReason string
}

// StagePost submits the review and inline comments to GitHub.
func StagePost(ctx context.Context, in StageInput, fetch *FetchResult, review *ReviewResult, sandboxRes *SandboxResult, ghClient *github.Client, agent *TraceAgent) (*PostResult, error) {
	start := time.Now()
	result, err := doPost(ctx, in, fetch, review, ghClient)
	ev := StageEvent{
		RunID:      in.RunID,
		Stage:      StagePostName,
		StartedAt:  start,
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		ev.Success = false
		ev.Error = err.Error()
	} else {
		ev.Success = true
		ev.Verdict = review.Event
		ev.InlineCount = len(review.Comments)
		ev.SandboxPass = sandboxPassed(sandboxRes)
		out, _ := json.Marshal(map[string]any{
			"review_id":  result.ReviewID,
			"review_url": result.ReviewURL,
			"dry_run":    result.DryRun,
		})
		ev.Output = out
	}
	agent.Emit(ev)
	return result, err
}

func doPost(ctx context.Context, in StageInput, fetch *FetchResult, review *ReviewResult, c *github.Client) (*PostResult, error) {
	if in.DryRun {
		return &PostResult{DryRun: true}, nil
	}
	if fetch == nil || fetch.PR == nil {
		return nil, fmt.Errorf("stage post: no PR metadata")
	}

	rev := github.ReviewRequest{
		CommitID: fetch.PR.Head.SHA,
		Body:     review.Body,
		Event:    review.Event,
		Comments: review.Comments,
	}
	resp, err := c.CreateReview(ctx, in.Owner, in.Repo, in.Number, rev)
	if err != nil {
		return nil, fmt.Errorf("stage post: submit review: %w", err)
	}
	return &PostResult{
		ReviewID:  resp.ID,
		ReviewURL: resp.HTMLURL,
	}, nil
}

// ── Shared review helpers ─────────────────────────────────────────────────────

// prFinding mirrors the type in devops_review_pr.go. Kept local to avoid
// import cycles between tools and pipeline packages.
type prFinding struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
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

func buildVerdict(findings []prFinding, pr *github.PullRequest, owner, repo string, sandboxResult *sandbox.Result) (event, body string) {
	counts := map[string]int{}
	for _, f := range findings {
		counts[f.Severity]++
	}

	event = "COMMENT"
	if counts["critical"] == 0 && counts["high"] == 0 {
		event = "APPROVE"
	} else {
		event = "REQUEST_CHANGES"
	}
	// Sandbox failure downgrades APPROVE → COMMENT.
	if sandboxResult != nil && !sandboxResult.Skipped && !sandboxResult.Succeeded && event == "APPROVE" {
		event = "COMMENT"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## PR Review: %s/%s#%d\n\n", owner, repo, pr.Number))
	if counts["critical"] > 0 || counts["high"] > 0 {
		sb.WriteString("**Action required** — blocking issues found.\n\n")
	} else if len(findings) > 0 {
		sb.WriteString("**Looks good** — minor suggestions only.\n\n")
	} else {
		sb.WriteString("**Approved** — no issues found.\n\n")
	}
	if len(findings) > 0 {
		sb.WriteString(fmt.Sprintf("| Severity | Count |\n|----------|-------|\n"))
		for _, sev := range []string{"critical", "high", "medium", "nit"} {
			if n := counts[sev]; n > 0 {
				sb.WriteString(fmt.Sprintf("| %s | %d |\n", sev, n))
			}
		}
	}
	if sandboxResult != nil && !sandboxResult.Skipped {
		sb.WriteString("\n")
		sb.WriteString(sandbox.FormatForReviewBody(sandboxResult))
	}
	return event, sb.String()
}

func formatFindingBody(f prFinding) string {
	emoji := map[string]string{
		"critical": "🚨",
		"high":     "🔴",
		"medium":   "🟡",
		"nit":      "💬",
	}
	e := emoji[f.Severity]
	if e == "" {
		e = "ℹ️"
	}
	return fmt.Sprintf("%s **[%s]** %s\n\n**Fix:** %s", e, strings.ToUpper(f.Severity), f.Issue, f.Fix)
}

func nearestLine(lineSet map[int]bool, target int) int {
	best, bestDist := 0, int(^uint(0)>>1)
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

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func stripJSONFences(s string) string {
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// resolveOwnerRepo separates "owner/repo" or sets owner from defaultOrg.
func resolveOwnerRepo(owner, repo, defaultOrg string) (string, string, error) {
	if strings.Contains(repo, "/") {
		parts := strings.SplitN(repo, "/", 2)
		return parts[0], parts[1], nil
	}
	if owner == "" {
		owner = defaultOrg
	}
	if owner == "" {
		return "", "", fmt.Errorf("owner/org not specified and devops.github.default_org not set")
	}
	return owner, repo, nil
}

// ── Subprocess workspace helpers ─────────────────────────────────────────────

// checkoutPR checks out the PR head ref into dir using git.
// Best-effort — failures are logged but don't stop the sandbox fallback path.
func checkoutPR(ctx context.Context, owner, repo, headRef, dir string) error {
	cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	cmd := exec.CommandContext(ctx, "git", "clone",
		"--depth=1", "--branch="+headRef, cloneURL, dir)
	cmd.Dir = filepath.Dir(dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clone: %w\n%s", err, out)
	}
	return nil
}
