// Package devops provides first-class DevOps platform clients used by
// OpsIntelligence agent tools: GitHub, GitLab, Jenkins, and SonarQube.
//
// Each subpackage exposes a thin HTTP client with narrow methods that cover
// the read-mostly workflow the agent needs (list PRs / MRs, read pipeline
// status, check a quality gate). A few explicit agent tools perform writes
// (e.g. GitHub PR conversation comments) using the same configured tokens.
//
// All clients are safe for concurrent use and respect context cancellation.
package devops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	maxRetries    = 3
	retryBaseWait = 500 * time.Millisecond
)

// UserAgent identifies OpsIntelligence requests in provider logs.
const UserAgent = "OpsIntelligence-DevOps/1.0 (+https://github.com/opsintelligence/opsintelligence)"

// DefaultTimeout is the per-request HTTP timeout applied when a caller does
// not attach a deadline to the context.
const DefaultTimeout = 15 * time.Second

// HTTPDoer matches the subset of *http.Client used by devops clients. Tests
// inject a stub via httptest servers or custom transports.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// DoJSON executes req with the provided client, asserts a 2xx status, and
// copies the response body into dst when dst is non-nil. Retries up to
// maxRetries times on transient server errors (5xx, 429). Returns the status
// code plus a trimmed error message on failure.
func DoJSON(ctx context.Context, client HTTPDoer, req *http.Request, dst io.Writer) (int, error) {
	if client == nil {
		return 0, errors.New("devops: nil http client")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}

	var (
		lastStatus int
		lastErr    error
	)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			wait := retryBaseWait * time.Duration(1<<uint(attempt-1))
			select {
			case <-ctx.Done():
				return lastStatus, fmt.Errorf("devops: %w after %d retries", ctx.Err(), attempt)
			case <-time.After(wait):
			}
		}

		resp, err := client.Do(req.WithContext(ctx))
		if err != nil {
			lastErr = fmt.Errorf("devops: http do: %w", err)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			lastStatus = resp.StatusCode
			lastErr = fmt.Errorf("devops: %s -> %d: %s", req.URL.Path, resp.StatusCode, string(snippet))
			continue
		}

		if resp.StatusCode >= 400 {
			snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return resp.StatusCode, fmt.Errorf("devops: %s -> %d: %s", req.URL.Path, resp.StatusCode, string(snippet))
		}

		if dst != nil {
			if _, err := io.Copy(dst, resp.Body); err != nil {
				resp.Body.Close()
				return resp.StatusCode, fmt.Errorf("devops: read body: %w", err)
			}
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}

	return lastStatus, lastErr
}
