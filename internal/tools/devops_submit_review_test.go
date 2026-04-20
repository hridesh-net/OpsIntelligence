package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/devops/github"
)

func TestGithubSubmitReviewToolExecuteSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method: %s", r.Method)
		}
		if r.URL.Path != "/repos/acme/api/pulls/42/reviews" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if payload["event"] != "COMMENT" {
			t.Fatalf("event: %v", payload["event"])
		}
		if payload["body"] == "" {
			t.Fatal("expected non-empty body")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           77,
			"html_url":     "https://github.com/acme/api/pull/42#pullrequestreview-77",
			"state":        "COMMENTED",
			"submitted_at": "2026-04-20T14:05:00Z",
		})
	}))
	defer srv.Close()

	tool := &githubSubmitReviewTool{
		c:          github.New(github.Config{Token: "tok", BaseURL: srv.URL}, srv.Client()),
		defaultOrg: "acme",
	}
	input := json.RawMessage(`{
		"repo":"api",
		"number":42,
		"event":"comment",
		"body":"Looks good overall.",
		"comments":[
			{"path":"internal/orders/handler.go","line":88,"body":"Please simplify this branch."}
		]
	}`)
	out, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("execute error: %v", err)
	}
	if !strings.Contains(out, `"id":77`) {
		t.Fatalf("missing review id in output: %s", out)
	}
	if !strings.Contains(out, `"comments":1`) {
		t.Fatalf("missing comments count in output: %s", out)
	}
}

func TestGithubSubmitReviewToolExecuteRequiresEvent(t *testing.T) {
	t.Parallel()

	tool := &githubSubmitReviewTool{
		c:          github.New(github.Config{Token: "tok", BaseURL: "https://api.github.com"}, nil),
		defaultOrg: "acme",
	}
	input := json.RawMessage(`{
		"repo":"api",
		"number":42,
		"body":"Missing event should fail."
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "event must be one of") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGithubSubmitReviewToolExecuteRequiresBody(t *testing.T) {
	t.Parallel()

	tool := &githubSubmitReviewTool{
		c:          github.New(github.Config{Token: "tok", BaseURL: "https://api.github.com"}, nil),
		defaultOrg: "acme",
	}
	input := json.RawMessage(`{
		"repo":"api",
		"number":42,
		"event":"APPROVE",
		"body":"   "
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "body is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGithubSubmitReviewToolExecuteRejectsInvalidRange(t *testing.T) {
	t.Parallel()

	tool := &githubSubmitReviewTool{
		c:          github.New(github.Config{Token: "tok", BaseURL: "https://api.github.com"}, nil),
		defaultOrg: "acme",
	}
	input := json.RawMessage(`{
		"repo":"api",
		"number":42,
		"event":"COMMENT",
		"body":"Review body.",
		"comments":[
			{"path":"foo.go","line":10,"start_line":10,"body":"Invalid range."}
		]
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "start_line") {
		t.Fatalf("unexpected error: %v", err)
	}
}
