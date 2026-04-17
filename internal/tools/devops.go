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

// DevOpsTools returns the agent tools for each enabled DevOps integration.
// Tools are only returned for providers whose config is enabled and has the
// minimum credentials to operate. Callers should register the returned tools
// on the shared ToolRegistry at startup.
func DevOpsTools(cfg config.DevOpsConfig) []agent.Tool {
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
			&githubPRDiffTool{c: gh, defaultOrg: cfg.GitHub.DefaultOrg},
			&githubWorkflowRunsTool{c: gh, defaultOrg: cfg.GitHub.DefaultOrg},
			&githubCombinedStatusTool{c: gh, defaultOrg: cfg.GitHub.DefaultOrg},
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

func (t *githubListPRsTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Owner, Repo, State string
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	if a.Owner == "" {
		a.Owner = t.defaultOrg
	}
	if a.Owner == "" {
		return "", fmt.Errorf("owner is required (no default_org configured)")
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

func (t *githubPRDiffTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var a struct {
		Owner, Repo string
		Number      int
	}
	if err := json.Unmarshal(input, &a); err != nil {
		return "", err
	}
	if a.Owner == "" {
		a.Owner = t.defaultOrg
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
	if a.Owner == "" {
		a.Owner = t.defaultOrg
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
	if a.Owner == "" {
		a.Owner = t.defaultOrg
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
