package sandbox

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/devops"
)

// CIKind identifies which CI system a detected configuration belongs to.
type CIKind string

const (
	CIKindGitHubActions CIKind = "github_actions"
	CIKindGitLabCI      CIKind = "gitlab_ci"
	CIKindCircleCI      CIKind = "circleci"
	CIKindUnknown       CIKind = "unknown"
)

// CIConfig holds a detected CI configuration file.
type CIConfig struct {
	Kind     CIKind
	FilePath string // e.g. ".github/workflows/ci.yml"
	Content  string // raw YAML content
}

// Detector fetches CI config files from the GitHub Contents API.
// It uses devops.HTTPDoer directly to stay decoupled from the github client package.
type Detector struct {
	baseURL string // e.g. "https://api.github.com"
	token   string
	http    devops.HTTPDoer
}

// NewDetector creates a Detector using the provided GitHub API base URL and token.
func NewDetector(baseURL, token string, httpClient devops.HTTPDoer) *Detector {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &Detector{baseURL: baseURL, token: token, http: httpClient}
}

// Detect checks the given branch/ref for CI config files in priority order:
//  1. .github/workflows/*.yml  → CIKindGitHubActions
//  2. .gitlab-ci.yml           → CIKindGitLabCI
//  3. .circleci/config.yml     → CIKindCircleCI
//
// Returns CIKindUnknown (not an error) when no CI config is found.
func (d *Detector) Detect(ctx context.Context, owner, repo, ref string) (*CIConfig, error) {
	// 1. GitHub Actions
	wf, err := d.firstWorkflowFile(ctx, owner, repo, ref)
	if err != nil {
		return nil, err
	}
	if wf != "" {
		content, err := d.fetchFileContent(ctx, owner, repo, wf, ref)
		if err != nil {
			return nil, err
		}
		return &CIConfig{Kind: CIKindGitHubActions, FilePath: wf, Content: content}, nil
	}

	// 2. GitLab CI
	glContent, err := d.fetchFileContent(ctx, owner, repo, ".gitlab-ci.yml", ref)
	if err != nil {
		return nil, err
	}
	if glContent != "" {
		return &CIConfig{Kind: CIKindGitLabCI, FilePath: ".gitlab-ci.yml", Content: glContent}, nil
	}

	// 3. CircleCI
	ccContent, err := d.fetchFileContent(ctx, owner, repo, ".circleci/config.yml", ref)
	if err != nil {
		return nil, err
	}
	if ccContent != "" {
		return &CIConfig{Kind: CIKindCircleCI, FilePath: ".circleci/config.yml", Content: ccContent}, nil
	}

	return &CIConfig{Kind: CIKindUnknown}, nil
}

// firstWorkflowFile lists .github/workflows/ and returns the path of the first
// .yml or .yaml file found, or "" if the directory doesn't exist.
func (d *Detector) firstWorkflowFile(ctx context.Context, owner, repo, ref string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/contents/.github/workflows?ref=%s",
		d.baseURL, owner, repo, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	d.setHeaders(req)

	resp, err := d.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sandbox: list workflows: %d", resp.StatusCode)
	}

	var entries []struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Type string `json:"type"`
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return "", err
	}
	if err := json.Unmarshal(buf.Bytes(), &entries); err != nil {
		return "", fmt.Errorf("sandbox: parse workflow listing: %w", err)
	}

	for _, e := range entries {
		if e.Type == "file" && (strings.HasSuffix(e.Name, ".yml") || strings.HasSuffix(e.Name, ".yaml")) {
			return e.Path, nil
		}
	}
	return "", nil
}

// fetchFileContent fetches a file at the given path+ref via the GitHub Contents API.
// Returns ("", nil) when the file does not exist (404).
func (d *Detector) fetchFileContent(ctx context.Context, owner, repo, path, ref string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s",
		d.baseURL, owner, repo, path, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	d.setHeaders(req)

	resp, err := d.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sandbox: fetch %s: %d", path, resp.StatusCode)
	}

	var envelope struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return "", err
	}
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		return "", fmt.Errorf("sandbox: parse contents response: %w", err)
	}

	if envelope.Encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(envelope.Content, "\n", ""))
		if err != nil {
			return "", fmt.Errorf("sandbox: decode content: %w", err)
		}
		return string(decoded), nil
	}
	return envelope.Content, nil
}

func (d *Detector) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", devops.UserAgent)
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
}
