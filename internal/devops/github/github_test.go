package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(handler http.HandlerFunc) (*Client, *httptest.Server) {
	srv := httptest.NewServer(handler)
	return New(Config{Token: "tok", BaseURL: srv.URL, DefaultOrg: "acme"}, srv.Client()), srv
}

func TestListPullRequests(t *testing.T) {
	t.Parallel()
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer tok" {
			t.Fatalf("missing/bad auth: %q", auth)
		}
		if !strings.HasPrefix(r.URL.Path, "/repos/acme/api/pulls") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Fatalf("unexpected state: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode([]PullRequest{{Number: 42, Title: "Fix it", State: "open"}})
	})
	defer srv.Close()

	prs, err := c.ListPullRequests(context.Background(), "acme", "api", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 42 || prs[0].Title != "Fix it" {
		t.Fatalf("unexpected prs: %+v", prs)
	}
}

func TestGetPullRequestDiff(t *testing.T) {
	t.Parallel()
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if accept := r.Header.Get("Accept"); accept != "application/vnd.github.v3.diff" {
			t.Fatalf("wrong accept: %q", accept)
		}
		_, _ = w.Write([]byte("diff --git a/foo b/foo\n+hello\n"))
	})
	defer srv.Close()
	diff, err := c.GetPullRequestDiff(context.Background(), "acme", "api", 42)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "+hello") {
		t.Fatalf("missing diff body: %q", diff)
	}
}

func TestCreateIssueComment(t *testing.T) {
	t.Parallel()
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method: %s", r.Method)
		}
		if !strings.HasPrefix(r.URL.Path, "/repos/acme/api/issues/42/comments") {
			t.Fatalf("path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 99, "html_url": "https://github.com/acme/api/issues/42#issuecomment-99"})
	})
	defer srv.Close()
	out, err := c.CreateIssueComment(context.Background(), "acme", "api", 42, "## OK\nShip it.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "html_url") || !strings.Contains(out, "issues/42#issuecomment") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestListWorkflowRuns(t *testing.T) {
	t.Parallel()
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("branch") != "main" {
			t.Fatalf("expected branch filter, got %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"workflow_runs": []WorkflowRun{{
				ID: 7, Name: "CI", HeadBranch: "main", Status: "completed", Conclusion: "failure",
			}},
		})
	})
	defer srv.Close()
	runs, err := c.ListWorkflowRuns(context.Background(), "acme", "api", "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Conclusion != "failure" {
		t.Fatalf("unexpected runs: %+v", runs)
	}
}

func TestDoJSONErrorMessage(t *testing.T) {
	t.Parallel()
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	})
	defer srv.Close()
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 in error: %v", err)
	}
}

func TestCreateReviewWithInlineComments(t *testing.T) {
	t.Parallel()
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method: %s", r.Method)
		}
		if r.URL.Path != "/repos/acme/api/pulls/42/reviews" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var in ReviewRequest
		if err := json.Unmarshal(raw, &in); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if in.Event != "REQUEST_CHANGES" {
			t.Fatalf("event: %s", in.Event)
		}
		if in.Body == "" {
			t.Fatal("expected non-empty review body")
		}
		if len(in.Comments) != 2 {
			t.Fatalf("comments: %d", len(in.Comments))
		}
		if in.Comments[0].Path != "internal/orders/handler.go" || in.Comments[0].Line != 88 || in.Comments[0].Side != "RIGHT" {
			t.Fatalf("unexpected first comment: %+v", in.Comments[0])
		}
		if in.Comments[1].StartLine != 40 || in.Comments[1].Line != 44 || in.Comments[1].StartSide != "RIGHT" {
			t.Fatalf("unexpected multiline comment: %+v", in.Comments[1])
		}
		_ = json.NewEncoder(w).Encode(ReviewResponse{
			ID:          123,
			HTMLURL:     "https://github.com/acme/api/pull/42#pullrequestreview-123",
			State:       "CHANGES_REQUESTED",
			SubmittedAt: "2026-04-20T14:00:00Z",
		})
	})
	defer srv.Close()

	resp, err := c.CreateReview(context.Background(), "acme", "api", 42, ReviewRequest{
		Event: "REQUEST_CHANGES",
		Body:  "Blocking concerns found.",
		Comments: []ReviewComment{
			{Path: "internal/orders/handler.go", Body: "Guard nil before use.", Line: 88},
			{Path: "internal/orders/service.go", Body: "Suggestion for this block.", StartLine: 40, Line: 44, Side: "RIGHT"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != 123 || !strings.Contains(resp.HTMLURL, "pullrequestreview-123") {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestCreateReviewRejectsInvalidCommentRange(t *testing.T) {
	t.Parallel()
	c := New(Config{Token: "tok", BaseURL: "https://api.github.com"}, nil)
	_, err := c.CreateReview(context.Background(), "acme", "api", 42, ReviewRequest{
		Event: "COMMENT",
		Body:  "Looks mostly good.",
		Comments: []ReviewComment{
			{Path: "foo.go", Body: "invalid range", StartLine: 22, Line: 22},
		},
	})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "start_line") {
		t.Fatalf("expected start_line error, got: %v", err)
	}
}
