package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/devops/github"
	"github.com/opsintelligence/opsintelligence/internal/devops/gitlab"
	"github.com/opsintelligence/opsintelligence/internal/devops/jenkins"
	"github.com/opsintelligence/opsintelligence/internal/devops/sonar"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// NewReviewFn returns a ReviewFn backed by devops.github.review_pr for use with PRReviewCmdHandler.
// Returns nil when GitHub integration is not configured.
func NewReviewFn(cfg config.DevOpsConfig, prov provider.Provider, model string) ReviewFn {
	if !cfg.GitHub.Enabled || cfg.GitHub.Token == "" {
		return nil
	}
	httpc := &http.Client{Timeout: 20 * time.Second}
	gh := github.New(github.Config{
		Token:      cfg.GitHub.Token,
		BaseURL:    cfg.GitHub.BaseURL,
		DefaultOrg: cfg.GitHub.DefaultOrg,
	}, httpc)
	tool := &githubReviewPRTool{
		c:                gh,
		defaultOrg:       cfg.GitHub.DefaultOrg,
		prov:             prov,
		model:            model,
		allowDraftReview: cfg.GitHub.AllowDraftReview,
	}
	return func(ctx context.Context, owner, repo string, number int) (string, error) {
		input, err := json.Marshal(map[string]any{
			"owner":  owner,
			"repo":   repo,
			"number": number,
		})
		if err != nil {
			return "", err
		}
		return tool.Execute(ctx, input)
	}
}

// DevOpsTools returns the agent tools for each enabled DevOps integration.
// Tools are only returned for providers whose config is enabled and has the
// minimum credentials to operate. Callers should register the returned tools
// on the shared ToolRegistry at startup.
//
// prov and model are the active LLM provider and model ID used by tools that
// call the LLM internally (e.g. devops.github.review_pr). Pass nil/empty to
// disable those tools.
func DevOpsTools(cfg config.DevOpsConfig, prov provider.Provider, model string) []agent.Tool {
	httpc := &http.Client{Timeout: 20 * time.Second}
	var out []agent.Tool
	if cfg.GitHub.Enabled && cfg.GitHub.Token != "" {
		gh := github.New(github.Config{
			Token:      cfg.GitHub.Token,
			BaseURL:    cfg.GitHub.BaseURL,
			DefaultOrg: cfg.GitHub.DefaultOrg,
		}, httpc)
		out = append(out,
			&githubListPRsTool{c: gh, defaultOrg: cfg.GitHub.DefaultOrg},
			&githubGetPRTool{c: gh, defaultOrg: cfg.GitHub.DefaultOrg},
			&githubPRDiffTool{c: gh, defaultOrg: cfg.GitHub.DefaultOrg},
			&githubPRCommentTool{c: gh, defaultOrg: cfg.GitHub.DefaultOrg},
			&githubSubmitReviewTool{c: gh, defaultOrg: cfg.GitHub.DefaultOrg},
			&githubWorkflowRunsTool{c: gh, defaultOrg: cfg.GitHub.DefaultOrg},
			&githubCombinedStatusTool{c: gh, defaultOrg: cfg.GitHub.DefaultOrg},
			&githubReviewPRTool{
				c:                gh,
				defaultOrg:       cfg.GitHub.DefaultOrg,
				prov:             prov,
				model:            model,
				allowDraftReview: cfg.GitHub.AllowDraftReview,
			},
		)
	}
	if cfg.GitLab.Enabled && cfg.GitLab.Token != "" && cfg.GitLab.BaseURL != "" {
		gl := gitlab.New(gitlab.Config{BaseURL: cfg.GitLab.BaseURL, Token: cfg.GitLab.Token}, httpc)
		out = append(out,
			&gitlabListMRsTool{c: gl},
			&gitlabListPipelinesTool{c: gl},
		)
	}
	if cfg.Jenkins.Enabled && cfg.Jenkins.Token != "" && cfg.Jenkins.BaseURL != "" {
		jk := jenkins.New(jenkins.Config{BaseURL: cfg.Jenkins.BaseURL, User: cfg.Jenkins.User, Token: cfg.Jenkins.Token}, httpc)
		out = append(out,
			&jenkinsGetJobTool{c: jk},
		)
	}
	if cfg.Sonar.Enabled && cfg.Sonar.Token != "" && cfg.Sonar.BaseURL != "" {
		sn := sonar.New(sonar.Config{BaseURL: cfg.Sonar.BaseURL, Token: cfg.Sonar.Token, ProjectKeyPrefix: cfg.Sonar.ProjectKeyPrefix}, httpc)
		out = append(out,
			&sonarQualityGateTool{c: sn},
			&sonarSearchIssuesTool{c: sn},
		)
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────
// GitHub tools
// ─────────────────────────────────────────────────────────────────────────

type githubListPRsTool struct {
	c          *github.Client
	defaultOrg string
}

func (t *githubListPRsTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.github.list_prs",
		Description: "List pull requests for an owner/repo. Filter by state (open, closed, all).",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{"type": "string", "description": "Org/user (default: devops.github.default_org)."},
				"repo":  map[string]any{"type": "string", "description": "Repository name."},
				"state": map[string]any{"type": "string", "description": "open|closed|all (default open)."},
			},
			Required: []string{"repo"},
		},
	}
}

type githubGetPRTool struct {
	c          *github.Client
	defaultOrg string
}

func (t *githubGetPRTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.github.pull_request",
		Description: "Fetch JSON metadata for one GitHub pull request (title, author, base/head refs, draft, URLs). Call this (with devops.github.pr_diff) before chain_run id=pr-review so the chain has real evidence.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"owner":  map[string]any{"type": "string", "description": "Org/user (default: devops.github.default_org)."},
				"repo":   map[string]any{"type": "string", "description": "Repository name."},
				"number": map[string]any{"type": "integer", "description": "Pull request number."},
			},
			Required: []string{"repo", "number"},
		},
	}
}

func (t *githubGetPRTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Owner, Repo string
		Number      int
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	var err error
	a.Owner, a.Repo, err = resolveOwnerRepo(a.Owner, a.Repo, t.defaultOrg)
	if err != nil {
		return "", err
	}
	if a.Number < 1 {
		return "", fmt.Errorf("number must be a positive PR number")
	}
	pr, err := t.c.GetPullRequest(ctx, a.Owner, a.Repo, a.Number)
	if err != nil {
		return "", err
	}
	body := pr.Body
	const maxBody = 6000
	if len(body) > maxBody {
		body = body[:maxBody] + "\n… (body truncated) …"
	}
	summary := struct {
		Number    int    `json:"number"`
		State     string `json:"state"`
		Draft     bool   `json:"draft"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		HTMLURL   string `json:"html_url"`
		User      string `json:"user"`
		HeadRef   string `json:"head_ref"`
		HeadSHA   string `json:"head_sha"`
		HeadRepo  string `json:"head_repo"`
		BaseRef   string `json:"base_ref"`
		BaseSHA   string `json:"base_sha"`
		BaseRepo  string `json:"base_repo"`
		UpdatedAt string `json:"updated_at"`
	}{
		Number:    pr.Number,
		State:     pr.State,
		Draft:     pr.Draft,
		Title:     pr.Title,
		Body:      body,
		HTMLURL:   pr.HTMLURL,
		User:      pr.User.Login,
		HeadRef:   pr.Head.Ref,
		HeadSHA:   pr.Head.SHA,
		HeadRepo:  pr.Head.Repo.FullName,
		BaseRef:   pr.Base.Ref,
		BaseSHA:   pr.Base.SHA,
		BaseRepo:  pr.Base.Repo.FullName,
		UpdatedAt: pr.UpdatedAt,
	}
	b, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (t *githubListPRsTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Owner, Repo, State string
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	var err error
	a.Owner, a.Repo, err = resolveOwnerRepo(a.Owner, a.Repo, t.defaultOrg)
	if err != nil {
		return "", err
	}
	prs, err := t.c.ListPullRequests(ctx, a.Owner, a.Repo, a.State)
	if err != nil {
		return "", err
	}
	if len(prs) == 0 {
		return fmt.Sprintf("No %s pull requests in %s/%s.", strings.ToLower(defaultString(a.State, "open")), a.Owner, a.Repo), nil
	}
	var b strings.Builder
	for _, p := range prs {
		fmt.Fprintf(&b, "#%d %s (by %s, %s → %s) %s\n", p.Number, p.Title, p.User.Login, p.Head.Ref, p.Base.Ref, p.HTMLURL)
	}
	return b.String(), nil
}

type githubPRDiffTool struct {
	c          *github.Client
	defaultOrg string
}

func (t *githubPRDiffTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.github.pr_diff",
		Description: "Fetch the unified diff for a pull request (truncated to 60KB for agent consumption).",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"owner":  map[string]any{"type": "string"},
				"repo":   map[string]any{"type": "string"},
				"number": map[string]any{"type": "integer", "description": "Pull request number."},
			},
			Required: []string{"repo", "number"},
		},
	}
}

type githubPRCommentTool struct {
	c          *github.Client
	defaultOrg string
}

func (t *githubPRCommentTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "devops.github.pr_comment",
		Description: "Post a Markdown comment on a GitHub pull request (conversation tab) using the configured devops GitHub token. " +
			"Use after the user explicitly asks to comment or post the review on the PR; requires a PAT with permission to create issue comments on the repo. " +
			"For formal file-level reviews with approve/request-changes AND inline line comments, prefer `devops.github.submit_review` instead — it posts the review + all inline comments atomically.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"owner":  map[string]any{"type": "string", "description": "Org/user (default: devops.github.default_org)."},
				"repo":   map[string]any{"type": "string", "description": "Repository name."},
				"number": map[string]any{"type": "integer", "description": "Pull request number (same as issue number for this API)."},
				"body":   map[string]any{"type": "string", "description": "GitHub-flavored Markdown comment body (max ~32KiB)."},
			},
			Required: []string{"repo", "number", "body"},
		},
	}
}

func (t *githubPRCommentTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Owner, Repo, Body string
		Number            int
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	var err error
	a.Owner, a.Repo, err = resolveOwnerRepo(a.Owner, a.Repo, t.defaultOrg)
	if err != nil {
		return "", err
	}
	if a.Number < 1 {
		return "", fmt.Errorf("number must be a positive PR number")
	}
	const maxBody = 32 * 1024
	body := a.Body
	if len(body) > maxBody {
		body = body[:maxBody] + "\n\n_(comment truncated for API size)_"
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("body is required")
	}
	return t.c.CreateIssueComment(ctx, a.Owner, a.Repo, a.Number, body)
}

// githubSubmitReviewTool posts a formal PR review (APPROVE / REQUEST_CHANGES /
// COMMENT or draft) in a single request, optionally with inline line-level
// comments. Mirrors the gh-pr-review skill's post-review.sh payload shape so
// the chain/skill markdown templates keep working without shelling out to `gh`.
type githubSubmitReviewTool struct {
	c          *github.Client
	defaultOrg string
}

func (t *githubSubmitReviewTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "devops.github.submit_review",
		Description: "Submit a formal GitHub pull-request review (APPROVE | REQUEST_CHANGES | COMMENT), optionally with inline line-level comments and ```suggestion``` blocks. " +
			"Mirrors the gh-pr-review skill payload so a single call posts the verdict body + all inline suggestions atomically. " +
			"Each `comments[]` entry targets `path` + `line` (side defaults to RIGHT); use `start_line`+`start_side` for multi-line ranges. " +
			"Use after the user explicitly asks to post the review; the configured GitHub token needs `pull_requests: write` on the repo.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"owner":     map[string]any{"type": "string", "description": "Org/user (default: devops.github.default_org)."},
				"repo":      map[string]any{"type": "string", "description": "Repository name."},
				"number":    map[string]any{"type": "integer", "description": "Pull request number."},
				"event":     map[string]any{"type": "string", "description": "APPROVE | REQUEST_CHANGES | COMMENT."},
				"body":      map[string]any{"type": "string", "description": "Top-level review body (Markdown)."},
				"commit_id": map[string]any{"type": "string", "description": "Optional commit SHA the review is against. Defaults to the PR's current HEAD when omitted."},
				"comments": map[string]any{
					"type":        "array",
					"description": "Optional inline review comments. Each entry must include path, body and line; side defaults to RIGHT.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path":       map[string]any{"type": "string", "description": "File path as it appears in the PR diff."},
							"body":       map[string]any{"type": "string", "description": "Comment body (Markdown). May include a ```suggestion``` block."},
							"line":       map[string]any{"type": "integer", "description": "Line number in the new file (RIGHT) or original file (LEFT)."},
							"side":       map[string]any{"type": "string", "description": "LEFT | RIGHT. Defaults to RIGHT."},
							"start_line": map[string]any{"type": "integer", "description": "For multi-line comments: first line of the range (must be < line)."},
							"start_side": map[string]any{"type": "string", "description": "For multi-line comments: LEFT | RIGHT. Defaults to side."},
						},
						"required": []string{"path", "body", "line"},
					},
				},
			},
			Required: []string{"repo", "number", "event", "body"},
		},
	}
}

func (t *githubSubmitReviewTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Owner    string                 `json:"owner"`
		Repo     string                 `json:"repo"`
		Number   int                    `json:"number"`
		Event    string                 `json:"event"`
		Body     string                 `json:"body"`
		CommitID string                 `json:"commit_id"`
		Comments []github.ReviewComment `json:"comments"`
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", fmt.Errorf("submit_review: invalid input: %w", err)
	}
	var err error
	a.Owner, a.Repo, err = resolveOwnerRepo(a.Owner, a.Repo, t.defaultOrg)
	if err != nil {
		return "", err
	}
	if a.Number < 1 {
		return "", fmt.Errorf("number must be a positive PR number")
	}

	// Normalise verdict + side casing so the LLM can be sloppy with case.
	event := strings.ToUpper(strings.TrimSpace(a.Event))
	switch event {
	case "APPROVE", "REQUEST_CHANGES", "COMMENT":
		// ok
	default:
		return "", fmt.Errorf("event must be one of APPROVE, REQUEST_CHANGES, COMMENT; got %q", a.Event)
	}

	// Top-level body constraints + truncation for safety.
	const maxBody = 60 * 1024
	body := a.Body
	if len(body) > maxBody {
		body = body[:maxBody] + "\n\n_(review body truncated for API size)_"
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("body is required")
	}

	// Cap the number of inline comments to stay well under GitHub's review
	// payload ceiling and avoid accidental comment storms.
	const maxComments = 200
	if len(a.Comments) > maxComments {
		return "", fmt.Errorf("too many inline comments (%d > %d); split into multiple reviews", len(a.Comments), maxComments)
	}

	// Per-comment normalisation + validation.
	const maxCommentBody = 32 * 1024
	comments := make([]github.ReviewComment, 0, len(a.Comments))
	for i, cm := range a.Comments {
		if strings.TrimSpace(cm.Path) == "" {
			return "", fmt.Errorf("comments[%d]: path is required", i)
		}
		if strings.TrimSpace(cm.Body) == "" {
			return "", fmt.Errorf("comments[%d] (%s): body is required", i, cm.Path)
		}
		if cm.Line < 1 && cm.Position < 1 {
			return "", fmt.Errorf("comments[%d] (%s): line (or position) must be a positive integer", i, cm.Path)
		}
		side := strings.ToUpper(strings.TrimSpace(cm.Side))
		switch side {
		case "", "LEFT", "RIGHT":
			// ok (empty means GitHub defaults to RIGHT when line is set)
		default:
			return "", fmt.Errorf("comments[%d] (%s): side must be LEFT, RIGHT, or empty; got %q", i, cm.Path, cm.Side)
		}
		if side == "" && cm.Line > 0 {
			side = "RIGHT"
		}
		cm.Side = side

		startSide := strings.ToUpper(strings.TrimSpace(cm.StartSide))
		switch startSide {
		case "", "LEFT", "RIGHT":
			// ok
		default:
			return "", fmt.Errorf("comments[%d] (%s): start_side must be LEFT, RIGHT, or empty; got %q", i, cm.Path, cm.StartSide)
		}
		if cm.StartLine > 0 {
			if cm.StartLine >= cm.Line {
				return "", fmt.Errorf("comments[%d] (%s): start_line (%d) must be less than line (%d)", i, cm.Path, cm.StartLine, cm.Line)
			}
			if startSide == "" {
				startSide = side
			}
		}
		cm.StartSide = startSide

		if len(cm.Body) > maxCommentBody {
			cm.Body = cm.Body[:maxCommentBody] + "\n\n_(comment truncated for API size)_"
		}
		comments = append(comments, cm)
	}

	rev := github.ReviewRequest{
		CommitID: strings.TrimSpace(a.CommitID),
		Body:     body,
		Event:    event,
		Comments: comments,
	}
	resp, err := t.c.CreateReview(ctx, a.Owner, a.Repo, a.Number, rev)
	if err != nil {
		return "", err
	}

	summary := struct {
		ID          int64  `json:"id"`
		HTMLURL     string `json:"html_url"`
		State       string `json:"state"`
		SubmittedAt string `json:"submitted_at"`
		Event       string `json:"event"`
		Comments    int    `json:"comments"`
	}{
		ID:          resp.ID,
		HTMLURL:     resp.HTMLURL,
		State:       resp.State,
		SubmittedAt: resp.SubmittedAt,
		Event:       event,
		Comments:    len(comments),
	}
	b, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (t *githubPRDiffTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Owner, Repo string
		Number      int
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	var err error
	a.Owner, a.Repo, err = resolveOwnerRepo(a.Owner, a.Repo, t.defaultOrg)
	if err != nil {
		return "", err
	}
	diff, err := t.c.GetPullRequestDiff(ctx, a.Owner, a.Repo, a.Number)
	if err != nil {
		return "", err
	}
	const max = 60_000
	if len(diff) > max {
		return diff[:max] + "\n… (diff truncated at 60KB) …", nil
	}
	return diff, nil
}

type githubWorkflowRunsTool struct {
	c          *github.Client
	defaultOrg string
}

func (t *githubWorkflowRunsTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.github.workflow_runs",
		Description: "List recent GitHub Actions workflow runs for a repo. Optionally filter by branch.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"owner":  map[string]any{"type": "string"},
				"repo":   map[string]any{"type": "string"},
				"branch": map[string]any{"type": "string", "description": "Optional branch filter."},
			},
			Required: []string{"repo"},
		},
	}
}

func (t *githubWorkflowRunsTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct{ Owner, Repo, Branch string }
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	var err error
	a.Owner, a.Repo, err = resolveOwnerRepo(a.Owner, a.Repo, t.defaultOrg)
	if err != nil {
		return "", err
	}
	runs, err := t.c.ListWorkflowRuns(ctx, a.Owner, a.Repo, a.Branch)
	if err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return fmt.Sprintf("No workflow runs found for %s/%s.", a.Owner, a.Repo), nil
	}
	var b strings.Builder
	for _, r := range runs {
		conclusion := r.Conclusion
		if conclusion == "" {
			conclusion = r.Status
		}
		fmt.Fprintf(&b, "[%s] %s on %s (%s) %s\n", conclusion, r.Name, r.HeadBranch, r.Event, r.HTMLURL)
	}
	return b.String(), nil
}

type githubCombinedStatusTool struct {
	c          *github.Client
	defaultOrg string
}

func (t *githubCombinedStatusTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.github.commit_status",
		Description: "Aggregate commit status (checks + required statuses) for a branch or SHA.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"owner": map[string]any{"type": "string"},
				"repo":  map[string]any{"type": "string"},
				"ref":   map[string]any{"type": "string", "description": "Branch name or commit SHA."},
			},
			Required: []string{"repo", "ref"},
		},
	}
}

func (t *githubCombinedStatusTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct{ Owner, Repo, Ref string }
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	var err error
	a.Owner, a.Repo, err = resolveOwnerRepo(a.Owner, a.Repo, t.defaultOrg)
	if err != nil {
		return "", err
	}
	cs, err := t.c.GetCombinedStatus(ctx, a.Owner, a.Repo, a.Ref)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s @ %s → %s\n", a.Repo, cs.SHA, cs.State)
	for _, s := range cs.Statuses {
		fmt.Fprintf(&b, "  [%s] %s — %s (%s)\n", s.State, s.Context, s.Description, s.TargetURL)
	}
	return b.String(), nil
}

// ─────────────────────────────────────────────────────────────────────────
// GitLab tools
// ─────────────────────────────────────────────────────────────────────────

type gitlabListMRsTool struct{ c *gitlab.Client }

func (t *gitlabListMRsTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.gitlab.list_mrs",
		Description: "List merge requests for a GitLab project (numeric ID or path like group/project).",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{"type": "string", "description": "Numeric ID or path."},
				"state":   map[string]any{"type": "string", "description": "opened|closed|merged|all (default opened)."},
			},
			Required: []string{"project"},
		},
	}
}

func (t *gitlabListMRsTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct{ Project, State string }
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	mrs, err := t.c.ListMergeRequests(ctx, a.Project, a.State)
	if err != nil {
		return "", err
	}
	if len(mrs) == 0 {
		return fmt.Sprintf("No %s merge requests in %s.", defaultString(a.State, "opened"), a.Project), nil
	}
	var b strings.Builder
	for _, m := range mrs {
		fmt.Fprintf(&b, "!%d %s (%s → %s) [%s] %s\n", m.IID, m.Title, m.SourceBranch, m.TargetBranch, m.DetailedStatus, m.WebURL)
	}
	return b.String(), nil
}

type gitlabListPipelinesTool struct{ c *gitlab.Client }

func (t *gitlabListPipelinesTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.gitlab.pipelines",
		Description: "List pipelines for a GitLab project, optionally filtered by ref or status.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{"type": "string"},
				"ref":     map[string]any{"type": "string"},
				"status":  map[string]any{"type": "string", "description": "success|failed|running|canceled|..."},
			},
			Required: []string{"project"},
		},
	}
}

func (t *gitlabListPipelinesTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct{ Project, Ref, Status string }
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	ps, err := t.c.ListPipelines(ctx, a.Project, a.Ref, a.Status)
	if err != nil {
		return "", err
	}
	if len(ps) == 0 {
		return fmt.Sprintf("No pipelines found for %s.", a.Project), nil
	}
	var b strings.Builder
	for _, p := range ps {
		fmt.Fprintf(&b, "#%d %s @ %s [%s] %s\n", p.ID, p.Ref, short(p.SHA, 8), p.Status, p.WebURL)
	}
	return b.String(), nil
}

// ─────────────────────────────────────────────────────────────────────────
// Jenkins tools
// ─────────────────────────────────────────────────────────────────────────

type jenkinsGetJobTool struct{ c *jenkins.Client }

func (t *jenkinsGetJobTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.jenkins.job_status",
		Description: "Fetch Jenkins job metadata including last build result. Use folder/subjob paths like 'platform/api-ci'.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"job_path": map[string]any{"type": "string", "description": "Job path, e.g. 'folder/sub/my-job'."},
			},
			Required: []string{"job_path"},
		},
	}
}

func (t *jenkinsGetJobTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		JobPath string `json:"job_path"`
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	j, err := t.c.GetJob(ctx, a.JobPath)
	if err != nil {
		return "", err
	}
	last := "(no build)"
	if j.LastBuild != nil {
		last = fmt.Sprintf("#%d %s", j.LastBuild.Number, j.LastBuild.Result)
	}
	return fmt.Sprintf("%s [color=%s, buildable=%v] last=%s %s", j.Name, j.Color, j.Buildable, last, j.URL), nil
}

// ─────────────────────────────────────────────────────────────────────────
// SonarQube tools
// ─────────────────────────────────────────────────────────────────────────

type sonarQualityGateTool struct{ c *sonar.Client }

func (t *sonarQualityGateTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.sonar.quality_gate",
		Description: "Fetch SonarQube quality gate status and conditions for a project key.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{"type": "string", "description": "Project key (project_key_prefix is applied automatically)."},
			},
			Required: []string{"project"},
		},
	}
}

func (t *sonarQualityGateTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct{ Project string }
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	qg, err := t.c.QualityGate(ctx, a.Project)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Quality gate: %s\n", qg.ProjectStatus.Status)
	for _, c := range qg.ProjectStatus.Conditions {
		fmt.Fprintf(&b, "  [%s] %s %s %s (actual=%s)\n", c.Status, c.MetricKey, c.Comparator, c.ErrorThreshold, c.ActualValue)
	}
	return b.String(), nil
}

type sonarSearchIssuesTool struct{ c *sonar.Client }

func (t *sonarSearchIssuesTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "devops.sonar.issues",
		Description: "Search Sonar issues. Accepts severities/types/statuses filters like Sonar's /api/issues/search.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"project":    map[string]any{"type": "string"},
				"severities": map[string]any{"type": "string", "description": "Comma-separated, e.g. BLOCKER,CRITICAL"},
				"types":      map[string]any{"type": "string", "description": "BUG,VULNERABILITY,CODE_SMELL"},
				"statuses":   map[string]any{"type": "string", "description": "OPEN,CONFIRMED,REOPENED"},
			},
			Required: []string{"project"},
		},
	}
}

func (t *sonarSearchIssuesTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Project, Severities, Types, Statuses string
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	extra := map[string][]string{}
	if a.Severities != "" {
		extra["severities"] = []string{a.Severities}
	}
	if a.Types != "" {
		extra["types"] = []string{a.Types}
	}
	if a.Statuses != "" {
		extra["statuses"] = []string{a.Statuses}
	}
	res, err := t.c.SearchIssues(ctx, a.Project, extra)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d total\n", res.Total)
	for _, i := range res.Issues {
		fmt.Fprintf(&b, "  [%s][%s] %s:%d %s — %s\n", i.Severity, i.Type, i.Component, i.Line, i.Rule, i.Message)
	}
	return b.String(), nil
}

// ─────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────

func defaultString(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func resolveOwnerRepo(owner, repo, defaultOrg string) (string, string, error) {
	if strings.Contains(repo, "/") {
		parts := strings.SplitN(repo, "/", 2)
		if owner == "" {
			owner = strings.TrimSpace(parts[0])
		}
		repo = strings.TrimSpace(parts[1])
	}
	if owner == "" {
		owner = defaultOrg
	}
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)

	if owner == "" {
		return "", "", fmt.Errorf("owner is required (no default_org configured)")
	}
	if repo == "" {
		return "", "", fmt.Errorf("repo is required")
	}
	return owner, repo, nil
}
