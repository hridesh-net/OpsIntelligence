package github

import (
	"context"
	"encoding/json"
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
