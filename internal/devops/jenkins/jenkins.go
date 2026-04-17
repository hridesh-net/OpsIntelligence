// Package jenkins provides a minimal Jenkins REST client used by
// OpsIntelligence for reading job and build status.
//
// Jenkins is typically authenticated with basic auth using a user ID and an
// API token (not a password). The client appends /api/json to job paths.
package jenkins

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

// Config holds Jenkins client configuration.
type Config struct {
	BaseURL string // e.g. https://jenkins.example.com
	User    string
	Token   string // API token
}

// Client talks to the Jenkins REST API.
type Client struct {
	cfg  Config
	http devops.HTTPDoer
}

// New builds a Jenkins client.
func New(cfg Config, httpClient devops.HTTPDoer) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: httpClient}
}

// Job is a trimmed Jenkins job payload.
type Job struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Color         string `json:"color"`   // blue=success, red=failed, yellow=unstable, aborted, etc.
	Buildable     bool   `json:"buildable"`
	LastBuild     *Build `json:"lastBuild"`
	LastCompleted *Build `json:"lastCompletedBuild"`
}

// Build is a trimmed Jenkins build payload.
type Build struct {
	Number    int    `json:"number"`
	URL       string `json:"url"`
	Result    string `json:"result"`    // SUCCESS, FAILURE, UNSTABLE, ABORTED
	Building  bool   `json:"building"`
	Duration  int64  `json:"duration"`  // ms
	Timestamp int64  `json:"timestamp"` // epoch ms
}

// GetJob fetches job metadata including the last build result.
//
// jobPath is the relative path under /job/, e.g. "folder/subfolder/my-job".
// Segments will be individually URL-path-escaped.
func (c *Client) GetJob(ctx context.Context, jobPath string) (*Job, error) {
	u := c.cfg.BaseURL + "/" + encodeJobPath(jobPath) + "/api/json?tree=" +
		url.QueryEscape("name,url,color,buildable,lastBuild[number,url,result,building,duration,timestamp],lastCompletedBuild[number,url,result,building,duration,timestamp]")
	req, err := c.newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var j Job
	if err := json.Unmarshal(buf.Bytes(), &j); err != nil {
		return nil, fmt.Errorf("jenkins: decode job: %w", err)
	}
	return &j, nil
}

// GetBuild fetches metadata for a specific build number.
func (c *Client) GetBuild(ctx context.Context, jobPath string, buildNumber int) (*Build, error) {
	u := fmt.Sprintf("%s/%s/%d/api/json?tree=number,url,result,building,duration,timestamp", c.cfg.BaseURL, encodeJobPath(jobPath), buildNumber)
	req, err := c.newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var b Build
	if err := json.Unmarshal(buf.Bytes(), &b); err != nil {
		return nil, fmt.Errorf("jenkins: decode build: %w", err)
	}
	return &b, nil
}

// Ping hits /api/json at the root to confirm credentials.
func (c *Client) Ping(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodGet, c.cfg.BaseURL+"/api/json?tree=nodeDescription")
	if err != nil {
		return err
	}
	_, err = devops.DoJSON(ctx, c.http, req, nil)
	return err
}

func (c *Client) newRequest(ctx context.Context, method, u string) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	if c.cfg.User != "" && c.cfg.Token != "" {
		r.SetBasicAuth(c.cfg.User, c.cfg.Token)
	}
	return r, nil
}

// encodeJobPath turns "folder/my-job" into "job/folder/job/my-job" with each segment URL-escaped.
func encodeJobPath(jobPath string) string {
	segs := strings.Split(strings.Trim(jobPath, "/"), "/")
	out := make([]string, 0, len(segs)*2)
	for _, s := range segs {
		if s == "" {
			continue
		}
		out = append(out, "job", url.PathEscape(s))
	}
	return strings.Join(out, "/")
}
