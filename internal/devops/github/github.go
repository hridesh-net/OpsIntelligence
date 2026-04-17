// Package github provides a minimal GitHub REST v3 client used by
// OpsIntelligence for PR review and GitHub Actions workflow monitoring.
//
// The client is intentionally narrow: read-mostly endpoints required by
// agent tools. For write operations the agent should shell out to `gh`
// with explicit human approval or use MCP.
package github

import (
	"bytes"
	"context"
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
