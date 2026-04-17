// Package gitlab provides a minimal GitLab REST v4 client used by
// OpsIntelligence for merge request review and pipeline monitoring.
package gitlab

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

// Config holds GitLab client configuration.
type Config struct {
	BaseURL string // e.g. https://gitlab.example.com
	Token   string // Personal access token or project access token
}

// Client talks to the GitLab REST v4 API.
type Client struct {
	cfg  Config
	http devops.HTTPDoer
}

// New builds a GitLab client.
func New(cfg Config, httpClient devops.HTTPDoer) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: httpClient}
}

// MergeRequest is a trimmed GitLab MR payload.
type MergeRequest struct {
	ID             int    `json:"id"`
	IID            int    `json:"iid"`
	ProjectID      int    `json:"project_id"`
	State          string `json:"state"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	WebURL         string `json:"web_url"`
	SourceBranch   string `json:"source_branch"`
	TargetBranch   string `json:"target_branch"`
	Author         User   `json:"author"`
	Draft          bool   `json:"draft"`
	MergeStatus    string `json:"merge_status"`
	DetailedStatus string `json:"detailed_merge_status"`
	UpdatedAt      string `json:"updated_at"`
	CreatedAt      string `json:"created_at"`
}

// User is a trimmed GitLab user.
type User struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

// ListMergeRequests returns MRs for project (numeric ID or url-encoded path).
func (c *Client) ListMergeRequests(ctx context.Context, project, state string) ([]MergeRequest, error) {
	if state == "" {
		state = "opened"
	}
	u := fmt.Sprintf("%s/api/v4/projects/%s/merge_requests?state=%s&per_page=50", c.cfg.BaseURL, url.PathEscape(project), url.QueryEscape(state))
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var mrs []MergeRequest
	if err := json.Unmarshal(buf.Bytes(), &mrs); err != nil {
		return nil, fmt.Errorf("gitlab: decode mrs: %w", err)
	}
	return mrs, nil
}

// Pipeline is a trimmed GitLab pipeline payload.
type Pipeline struct {
	ID        int    `json:"id"`
	IID       int    `json:"iid"`
	Ref       string `json:"ref"`
	SHA       string `json:"sha"`
	Status    string `json:"status"`
	Source    string `json:"source"`
	WebURL    string `json:"web_url"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ListPipelines returns pipelines for a project, optionally filtered by ref/status.
func (c *Client) ListPipelines(ctx context.Context, project, ref, status string) ([]Pipeline, error) {
	q := url.Values{}
	if ref != "" {
		q.Set("ref", ref)
	}
	if status != "" {
		q.Set("status", status)
	}
	q.Set("per_page", "30")
	u := fmt.Sprintf("%s/api/v4/projects/%s/pipelines?%s", c.cfg.BaseURL, url.PathEscape(project), q.Encode())
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var ps []Pipeline
	if err := json.Unmarshal(buf.Bytes(), &ps); err != nil {
		return nil, fmt.Errorf("gitlab: decode pipelines: %w", err)
	}
	return ps, nil
}

// Job is a trimmed pipeline job payload.
type Job struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Stage  string `json:"stage"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
	Ref    string `json:"ref"`
}

// GetPipelineJobs returns jobs for a specific pipeline.
func (c *Client) GetPipelineJobs(ctx context.Context, project string, pipelineID int) ([]Job, error) {
	u := fmt.Sprintf("%s/api/v4/projects/%s/pipelines/%d/jobs?per_page=100", c.cfg.BaseURL, url.PathEscape(project), pipelineID)
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var jobs []Job
	if err := json.Unmarshal(buf.Bytes(), &jobs); err != nil {
		return nil, fmt.Errorf("gitlab: decode jobs: %w", err)
	}
	return jobs, nil
}

// Ping calls /version to confirm credentials.
func (c *Client) Ping(ctx context.Context) error {
	u := c.cfg.BaseURL + "/api/v4/version"
	req, err := c.newRequest(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	_, err = devops.DoJSON(ctx, c.http, req, nil)
	return err
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
		r.Header.Set("PRIVATE-TOKEN", c.cfg.Token)
	}
	return r, nil
}
