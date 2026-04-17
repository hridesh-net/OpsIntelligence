// Package sonar provides a minimal SonarQube / SonarCloud Web API client
// used by OpsIntelligence to monitor quality gates, new-code issues, and
// security hotspots.
//
// SonarQube auth uses a Basic header with the user token as the username
// and an empty password. This client encapsulates that scheme.
package sonar

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

// Config holds SonarQube client configuration.
type Config struct {
	BaseURL          string // e.g. https://sonar.example.com
	Token            string // user or project token
	ProjectKeyPrefix string // optional prefix applied when callers pass short names
}

// Client talks to the SonarQube Web API.
type Client struct {
	cfg  Config
	http devops.HTTPDoer
}

// New builds a Sonar client.
func New(cfg Config, httpClient devops.HTTPDoer) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: httpClient}
}

// QualityGateStatus is the response payload for /api/qualitygates/project_status.
type QualityGateStatus struct {
	ProjectStatus ProjectStatus `json:"projectStatus"`
}

// ProjectStatus captures the overall gate status plus condition details.
type ProjectStatus struct {
	Status     string      `json:"status"` // OK, ERROR, WARN, NONE
	Conditions []Condition `json:"conditions"`
}

// Condition is one quality-gate condition.
type Condition struct {
	Status         string `json:"status"`
	MetricKey      string `json:"metricKey"`
	Comparator     string `json:"comparator"`
	ErrorThreshold string `json:"errorThreshold"`
	ActualValue    string `json:"actualValue"`
}

// QualityGate fetches the current gate status for a project key.
func (c *Client) QualityGate(ctx context.Context, projectKey string) (*QualityGateStatus, error) {
	key := c.resolveKey(projectKey)
	u := fmt.Sprintf("%s/api/qualitygates/project_status?projectKey=%s", c.cfg.BaseURL, url.QueryEscape(key))
	req, err := c.newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var qg QualityGateStatus
	if err := json.Unmarshal(buf.Bytes(), &qg); err != nil {
		return nil, fmt.Errorf("sonar: decode quality gate: %w", err)
	}
	return &qg, nil
}

// Issue is a trimmed Sonar issue payload.
type Issue struct {
	Key      string `json:"key"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"` // BLOCKER, CRITICAL, MAJOR, MINOR, INFO
	Status   string `json:"status"`
	Message  string `json:"message"`
	Project  string `json:"project"`
	Component string `json:"component"`
	Type     string `json:"type"` // BUG, VULNERABILITY, CODE_SMELL
	Line     int    `json:"line"`
}

// IssueSearch is the trimmed response payload.
type IssueSearch struct {
	Total  int     `json:"total"`
	Issues []Issue `json:"issues"`
}

// SearchIssues queries /api/issues/search with the given extra params (e.g. severities=BLOCKER,CRITICAL).
// projectKey is always scoped via componentKeys; params is merged in.
func (c *Client) SearchIssues(ctx context.Context, projectKey string, extra url.Values) (*IssueSearch, error) {
	q := url.Values{}
	q.Set("componentKeys", c.resolveKey(projectKey))
	q.Set("ps", "50")
	for k, v := range extra {
		for _, item := range v {
			q.Add(k, item)
		}
	}
	u := c.cfg.BaseURL + "/api/issues/search?" + q.Encode()
	req, err := c.newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var out IssueSearch
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("sonar: decode issues: %w", err)
	}
	return &out, nil
}

// Hotspot is a trimmed security hotspot payload.
type Hotspot struct {
	Key              string `json:"key"`
	Component        string `json:"component"`
	SecurityCategory string `json:"securityCategory"`
	VulnerabilityProbability string `json:"vulnerabilityProbability"`
	Status           string `json:"status"`
	Line             int    `json:"line"`
	Message          string `json:"message"`
}

// HotspotSearch is the trimmed response.
type HotspotSearch struct {
	Paging struct {
		Total int `json:"total"`
	} `json:"paging"`
	Hotspots []Hotspot `json:"hotspots"`
}

// HotspotsSearch queries /api/hotspots/search for a project.
func (c *Client) HotspotsSearch(ctx context.Context, projectKey string) (*HotspotSearch, error) {
	u := fmt.Sprintf("%s/api/hotspots/search?projectKey=%s&ps=50", c.cfg.BaseURL, url.QueryEscape(c.resolveKey(projectKey)))
	req, err := c.newRequest(ctx, http.MethodGet, u)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := devops.DoJSON(ctx, c.http, req, &buf); err != nil {
		return nil, err
	}
	var out HotspotSearch
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("sonar: decode hotspots: %w", err)
	}
	return &out, nil
}

// Ping calls /api/system/ping to confirm credentials.
func (c *Client) Ping(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodGet, c.cfg.BaseURL+"/api/authentication/validate")
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
	if c.cfg.Token != "" {
		// Sonar auth: token as username, empty password.
		r.SetBasicAuth(c.cfg.Token, "")
	}
	return r, nil
}

func (c *Client) resolveKey(key string) string {
	if c.cfg.ProjectKeyPrefix != "" && !strings.HasPrefix(key, c.cfg.ProjectKeyPrefix) {
		return c.cfg.ProjectKeyPrefix + key
	}
	return key
}
