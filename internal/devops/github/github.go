// Package github provides a minimal GitHub REST v3 client used by
// OpsIntelligence for PR review and GitHub Actions workflow monitoring.
//
// Read endpoints cover evidence gathering; a small write path exists for
// posting PR/issue conversation comments when the agent has an explicit tool call.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/devops"
)

// Config holds GitHub client configuration.
type Config struct {
	Token      string // Personal access token or App installation token
	BaseURL    string // default https://api.github.com
	DefaultOrg string
}

// Client talks to the GitHub REST v3 API.
type Client struct {
	cfg  Config
	http devops.HTTPDoer
}

// New builds a GitHub client. Pass a nil http.Client to use http.DefaultClient.
func New(cfg Config, httpClient devops.HTTPDoer) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.github.com"
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: httpClient}
}

// PullRequest is a trimmed view of the GitHub PR resource the agent cares about.
type PullRequest struct {
	Number    int    `json:"number"`
	State     string `json:"state"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	HTMLURL   string `json:"html_url"`
	Draft     bool   `json:"draft"`
	User      User   `json:"user"`
	Head      Ref    `json:"head"`
	Base      Ref    `json:"base"`
	UpdatedAt string `json:"updated_at"`
	CreatedAt string `json:"created_at"`
}

// User is a trimmed GitHub user payload.
type User struct {
	Login string `json:"login"`
}

// Ref describes a head or base reference.
type Ref struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo Repo   `json:"repo"`
}

// Repo is a trimmed repository payload.
type Repo struct {
	FullName string `json:"full_name"`
}

// ListPullRequests returns PRs for owner/repo filtered by state (open, closed, all).
func (c *Client) ListPullRequests(ctx context.Context, owner, repo, state string) ([]PullRequest, error) {
	if state == "" {
		state = "open"
	}
	u := fmt.Sprintf("%s/repos/%s/%s/pulls?state=%s&per_page=50", c.cfg.BaseURL, owner, repo, url.QueryEscape(state))
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var prs []PullRequest
	if err := json.Unmarshal(buf.Bytes(), &prs); err != nil {
		return nil, fmt.Errorf("github: decode pulls: %w", err)
	}
	return prs, nil
}

// GetPullRequest fetches a single PR.
func (c *Client) GetPullRequest(ctx context.Context, owner, repo string, number int) (*PullRequest, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", c.cfg.BaseURL, owner, repo, number)
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var pr PullRequest
	if err := json.Unmarshal(buf.Bytes(), &pr); err != nil {
		return nil, fmt.Errorf("github: decode pr: %w", err)
	}
	return &pr, nil
}

// GetPullRequestDiff returns the unified diff text for a PR.
func (c *Client) GetPullRequestDiff(ctx context.Context, owner, repo string, number int) (string, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", c.cfg.BaseURL, owner, repo, number)
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3.diff")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// PRFile describes a single changed file in a pull request.
type PRFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"` // added, removed, modified, renamed
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Changes   int    `json:"changes"`
	Patch     string `json:"patch,omitempty"` // unified diff fragment; absent for binary files
}

// GetPullRequestFiles returns the list of files changed in a PR, each with
// its per-file unified diff patch. Callers can use the patch to derive valid
// line numbers for inline review comments.
func (c *Client) GetPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]PRFile, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files?per_page=100", c.cfg.BaseURL, owner, repo, number)
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var files []PRFile
	if err := json.Unmarshal(buf.Bytes(), &files); err != nil {
		return nil, fmt.Errorf("github: decode pr files: %w", err)
	}
	return files, nil
}

// WorkflowRun is a trimmed Actions run payload.
type WorkflowRun struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	HeadBranch   string `json:"head_branch"`
	HeadSHA      string `json:"head_sha"`
	Status       string `json:"status"`     // queued, in_progress, completed
	Conclusion   string `json:"conclusion"` // success, failure, cancelled, ...
	HTMLURL      string `json:"html_url"`
	Event        string `json:"event"`
	UpdatedAt    string `json:"updated_at"`
	RunStartedAt string `json:"run_started_at"`
}

// ListWorkflowRuns lists recent runs for owner/repo, optionally filtered by branch.
func (c *Client) ListWorkflowRuns(ctx context.Context, owner, repo, branch string) ([]WorkflowRun, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/actions/runs?per_page=30", c.cfg.BaseURL, owner, repo)
	if branch != "" {
		u += "&branch=" + url.QueryEscape(branch)
	}
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var envelope struct {
		WorkflowRuns []WorkflowRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		return nil, fmt.Errorf("github: decode runs: %w", err)
	}
	return envelope.WorkflowRuns, nil
}

// CombinedStatus represents GitHub's combined commit status endpoint response.
type CombinedStatus struct {
	State    string          `json:"state"`
	Statuses []CommitContext `json:"statuses"`
	SHA      string          `json:"sha"`
}

// CommitContext is one check/status entry.
type CommitContext struct {
	Context     string `json:"context"`
	State       string `json:"state"`
	Description string `json:"description"`
	TargetURL   string `json:"target_url"`
}

// GetCombinedStatus reads the aggregated status for a commit or branch head ref.
func (c *Client) GetCombinedStatus(ctx context.Context, owner, repo, ref string) (*CombinedStatus, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/commits/%s/status", c.cfg.BaseURL, owner, repo, url.PathEscape(ref))
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var s CombinedStatus
	if err := json.Unmarshal(buf.Bytes(), &s); err != nil {
		return nil, fmt.Errorf("github: decode status: %w", err)
	}
	return &s, nil
}

// Ping calls /user (or /rate_limit) to confirm credentials.
// CreateIssueComment posts a comment on a GitHub issue or pull request (PRs use the issues API).
// body should be GitHub-flavored Markdown. Returns JSON with html_url and id on success.
func (c *Client) CreateIssueComment(ctx context.Context, owner, repo string, issueNumber int, body string) (string, error) {
	if issueNumber < 1 {
		return "", fmt.Errorf("github: issue number must be >= 1")
	}
	payload := map[string]string{"body": body}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	u := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", c.cfg.BaseURL, owner, repo, issueNumber)
	req, err := c.newRequest(ctx, http.MethodPost, u, raw)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return "", err
	}
	var out struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		return "", fmt.Errorf("github: decode issue comment: %w", err)
	}
	b, err := json.Marshal(map[string]any{"id": out.ID, "html_url": out.HTMLURL})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// FileContent is the decoded response from the contents API.
type FileContent struct {
	Path    string `json:"path"`
	SHA     string `json:"sha"`
	Size    int    `json:"size"`
	Content string `json:"content"` // decoded (not base64)
}

// GetFileContent fetches the content of a single file at the given ref (branch, tag, or SHA).
// Returns an error if the path points to a directory or the file exceeds 1 MB.
func (c *Client) GetFileContent(ctx context.Context, owner, repo, path, ref string) (*FileContent, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.cfg.BaseURL, owner, repo, url.PathEscape(path))
	if ref != "" {
		u += "?ref=" + url.QueryEscape(ref)
	}
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var raw struct {
		Path     string `json:"path"`
		SHA      string `json:"sha"`
		Size     int    `json:"size"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Type     string `json:"type"`
	}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("github: decode file content: %w", err)
	}
	if raw.Type == "dir" {
		return nil, fmt.Errorf("github: %s is a directory, not a file", path)
	}
	// GitHub returns base64 with newlines; strip them before decoding.
	decoded := strings.ReplaceAll(raw.Content, "\n", "")
	content, err := decodeBase64(decoded)
	if err != nil {
		return nil, fmt.Errorf("github: decode base64 content: %w", err)
	}
	return &FileContent{Path: raw.Path, SHA: raw.SHA, Size: raw.Size, Content: content}, nil
}

// PRReview is a pull-request review as returned by the GitHub API.
type PRReview struct {
	ID          int64  `json:"id"`
	User        string `json:"-"` // populated from raw.User.Login
	State       string `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
	HTMLURL     string `json:"html_url"`
}

// ListPullRequestReviews returns all reviews submitted on a PR.
func (c *Client) ListPullRequestReviews(ctx context.Context, owner, repo string, number int) ([]PRReview, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews?per_page=100", c.cfg.BaseURL, owner, repo, number)
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var raw []struct {
		ID          int64  `json:"id"`
		User        struct{ Login string `json:"login"` } `json:"user"`
		State       string `json:"state"`
		Body        string `json:"body"`
		SubmittedAt string `json:"submitted_at"`
		HTMLURL     string `json:"html_url"`
	}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("github: decode pr reviews: %w", err)
	}
	out := make([]PRReview, len(raw))
	for i, r := range raw {
		out[i] = PRReview{ID: r.ID, User: r.User.Login, State: r.State,
			Body: r.Body, SubmittedAt: r.SubmittedAt, HTMLURL: r.HTMLURL}
	}
	return out, nil
}

// CheckRun is one entry from the check-runs API.
type CheckRun struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`     // queued, in_progress, completed
	Conclusion  string `json:"conclusion"` // success, failure, neutral, cancelled, skipped, ...
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	HTMLURL     string `json:"html_url"`
	// Output contains the summary and any annotations from the check run.
	Output struct {
		Title            string `json:"title"`
		Summary          string `json:"summary"`
		AnnotationsCount int    `json:"annotations_count"`
		AnnotationsURL   string `json:"annotations_url"`
	} `json:"output"`
}

// GetCheckRuns returns all check runs for the given ref (commit SHA or branch).
func (c *Client) GetCheckRuns(ctx context.Context, owner, repo, ref string) ([]CheckRun, error) {
	u := fmt.Sprintf("%s/repos/%s/%s/commits/%s/check-runs?per_page=100",
		c.cfg.BaseURL, owner, repo, url.PathEscape(ref))
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var envelope struct {
		CheckRuns []CheckRun `json:"check_runs"`
	}
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		return nil, fmt.Errorf("github: decode check runs: %w", err)
	}
	return envelope.CheckRuns, nil
}

func (c *Client) Ping(ctx context.Context) error {
	u := c.cfg.BaseURL + "/rate_limit"
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if _, err := devops.DoJSON(ctx, c.http, req, nil); err != nil {
		return err
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, u string, body []byte) (*http.Request, error) {
	var r *http.Request
	var err error
	if body != nil {
		r, err = http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	} else {
		r, err = http.NewRequestWithContext(ctx, method, u, nil)
	}
	if err != nil {
		return nil, err
	}
	if c.cfg.Token != "" {
		r.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return r, nil
}

// ─────────────────────────────────────────────────────────────────────────
// Pull request reviews (line-level comments + suggestion blocks + verdict)
// ─────────────────────────────────────────────────────────────────────────

// ReviewComment is a single line- or range-level review comment.
//
// Set Line (and optionally Side) for a single-line comment, or
// StartLine + StartSide + Line + Side for a multi-line range. Body may
// contain GitHub-flavored Markdown, including a ```suggestion``` fenced
// block the author can apply with one click.
//
// Reference:
// https://docs.github.com/en/rest/pulls/reviews#create-a-review-for-a-pull-request
type ReviewComment struct {
	Path      string `json:"path"`
	Body      string `json:"body"`
	Line      int    `json:"line,omitempty"`
	Side      string `json:"side,omitempty"`       // LEFT | RIGHT (default RIGHT when Line > 0)
	StartLine int    `json:"start_line,omitempty"` // multi-line: must be < Line
	StartSide string `json:"start_side,omitempty"` // multi-line: LEFT | RIGHT (defaults to Side)
	Position  int    `json:"position,omitempty"`   // legacy; prefer Line
}

// ReviewRequest is the payload for POST /repos/{o}/{r}/pulls/{n}/reviews.
//
// Event must be one of "APPROVE", "REQUEST_CHANGES", "COMMENT", or empty.
// An empty Event saves the review as a PENDING draft that a human can
// submit from the GitHub UI.
type ReviewRequest struct {
	CommitID string          `json:"commit_id,omitempty"`
	Body     string          `json:"body,omitempty"`
	Event    string          `json:"event,omitempty"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// ReviewResponse is the subset of the created-review payload we surface.
type ReviewResponse struct {
	ID          int64  `json:"id"`
	HTMLURL     string `json:"html_url"`
	State       string `json:"state"`
	SubmittedAt string `json:"submitted_at"`
}

// CreateReview submits a pull-request review in a single request. It posts
// a top-level body plus any number of line- or range-level comments and
// returns the created review record.
//
// Validates the payload before sending to surface the usual misuse (bad
// event, missing body on REQUEST_CHANGES/COMMENT, comment without line,
// start_line >= line) as a typed error rather than an opaque 422 from the
// API.
func (c *Client) CreateReview(ctx context.Context, owner, repo string, number int, rev ReviewRequest) (*ReviewResponse, error) {
	if number < 1 {
		return nil, fmt.Errorf("github: pr number must be >= 1")
	}
	switch rev.Event {
	case "", "APPROVE", "REQUEST_CHANGES", "COMMENT":
	default:
		return nil, fmt.Errorf("github: invalid event %q (expected APPROVE|REQUEST_CHANGES|COMMENT or empty for draft)", rev.Event)
	}
	if (rev.Event == "REQUEST_CHANGES" || rev.Event == "COMMENT") && strings.TrimSpace(rev.Body) == "" {
		return nil, fmt.Errorf("github: body is required when event is %s", rev.Event)
	}
	for i := range rev.Comments {
		cc := &rev.Comments[i]
		if strings.TrimSpace(cc.Path) == "" {
			return nil, fmt.Errorf("github: comments[%d] missing path", i)
		}
		if strings.TrimSpace(cc.Body) == "" {
			return nil, fmt.Errorf("github: comments[%d] missing body", i)
		}
		if cc.Position == 0 && cc.Line <= 0 {
			return nil, fmt.Errorf("github: comments[%d] requires line (or legacy position)", i)
		}
		if cc.Side == "" && cc.Line > 0 {
			cc.Side = "RIGHT"
		}
		switch cc.Side {
		case "", "LEFT", "RIGHT":
		default:
			return nil, fmt.Errorf("github: comments[%d] invalid side %q (expected LEFT or RIGHT)", i, cc.Side)
		}
		if cc.StartLine > 0 {
			if cc.StartLine >= cc.Line {
				return nil, fmt.Errorf("github: comments[%d] start_line (%d) must be < line (%d)", i, cc.StartLine, cc.Line)
			}
			if cc.StartSide == "" {
				cc.StartSide = cc.Side
			}
			switch cc.StartSide {
			case "LEFT", "RIGHT":
			default:
				return nil, fmt.Errorf("github: comments[%d] invalid start_side %q", i, cc.StartSide)
			}
		}
	}
	raw, err := json.Marshal(rev)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews", c.cfg.BaseURL, owner, repo, number)
	req, err := c.newRequest(ctx, http.MethodPost, u, raw)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var out ReviewResponse
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("github: decode review: %w", err)
	}
	return &out, nil
}

func decodeBase64(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Issue is a trimmed GitHub issue.
type Issue struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	State     string `json:"state"`
	HTMLURL   string `json:"html_url"`
	UpdatedAt string `json:"updated_at"`
}

// ListIssues returns issues (not PRs) for owner/repo.
func (c *Client) ListIssues(ctx context.Context, owner, repo, state string) ([]Issue, error) {
	if state == "" {
		state = "open"
	}
	u := fmt.Sprintf("%s/repos/%s/%s/issues?state=%s&per_page=100", c.cfg.BaseURL, owner, repo, state)
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var issues []Issue
	if err := json.Unmarshal(buf.Bytes(), &issues); err != nil {
		return nil, fmt.Errorf("github: decode issues: %w", err)
	}
	var out []Issue
	for _, i := range issues {
		if !strings.Contains(i.HTMLURL, "/pull/") {
			out = append(out, i)
		}
	}
	return out, nil
}
