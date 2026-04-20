package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/devops/github"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// ─────────────────────────────────────────────────────────────────────────────
// Stub LLM provider
// ─────────────────────────────────────────────────────────────────────────────

type stubProvider struct {
	response string
	err      error
}

func (s *stubProvider) Name() string { return "stub" }
func (s *stubProvider) Complete(_ context.Context, _ *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &provider.CompletionResponse{
		Content: []provider.ContentPart{{Type: provider.ContentTypeText, Text: s.response}},
	}, nil
}
func (s *stubProvider) Stream(_ context.Context, _ *provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 1)
	ch <- provider.StreamEvent{Type: provider.StreamEventDone}
	close(ch)
	return ch, nil
}
func (s *stubProvider) ListModels(_ context.Context) ([]provider.ModelInfo, error)  { return nil, nil }
func (s *stubProvider) HealthCheck(_ context.Context) error                          { return nil }
func (s *stubProvider) Embed(_ context.Context, _ string, _ string) ([]float32, error) {
	return nil, nil
}
func (s *stubProvider) ValidateModel(_ context.Context, _ string) error { return nil }

// ─────────────────────────────────────────────────────────────────────────────
// buildAnnotatedDiff
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildAnnotatedDiff_AddsNewFileLines(t *testing.T) {
	t.Parallel()
	files := []github.PRFile{
		{
			Filename:  "backend/lambda/index.mjs",
			Status:    "added",
			Additions: 2,
			Patch:     "@@ -0,0 +7,2 @@\n+// comment\n+const TABLE_NAME = process.env.TABLE_NAME || \"BruditeInfo\";",
		},
	}
	annotated, validLines := buildAnnotatedDiff(files)

	if !strings.Contains(annotated, "7 +") && !strings.Contains(annotated, "7") {
		t.Errorf("expected line 7 in annotated diff, got:\n%s", annotated)
	}
	set, ok := validLines["backend/lambda/index.mjs"]
	if !ok {
		t.Fatal("expected file in validLines")
	}
	if !set[7] || !set[8] {
		t.Errorf("expected lines 7 and 8 in valid set, got %v", set)
	}
}

func TestBuildAnnotatedDiff_SkipsBinaryFiles(t *testing.T) {
	t.Parallel()
	files := []github.PRFile{
		{Filename: "assets/logo.png", Status: "added", Additions: 0, Patch: ""},
	}
	annotated, validLines := buildAnnotatedDiff(files)
	if !strings.Contains(annotated, "assets/logo.png") {
		t.Errorf("expected filename in annotated output")
	}
	if _, ok := validLines["assets/logo.png"]; ok {
		t.Error("binary file should not be in validLines")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// parseHunkNewStart
// ─────────────────────────────────────────────────────────────────────────────

func TestParseHunkNewStart(t *testing.T) {
	t.Parallel()
	tests := []struct {
		hunk string
		want int
	}{
		{"@@ -0,0 +1,10 @@", 1},
		{"@@ -10,6 +15,8 @@ func foo()", 15},
		{"@@ -1 +1 @@", 1},
		{"@@ -0,0 +7,2 @@", 7},
		{"not a hunk", 0},
	}
	for _, tc := range tests {
		got := parseHunkNewStart(tc.hunk)
		if got != tc.want {
			t.Errorf("parseHunkNewStart(%q) = %d, want %d", tc.hunk, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// nearestLine
// ─────────────────────────────────────────────────────────────────────────────

func TestNearestLine(t *testing.T) {
	t.Parallel()
	set := map[int]bool{5: true, 10: true, 20: true}
	if got := nearestLine(set, 12); got != 10 {
		t.Errorf("nearestLine(set, 12) = %d, want 10", got)
	}
	if got := nearestLine(set, 17); got != 20 {
		t.Errorf("nearestLine(set, 17) = %d, want 20", got)
	}
	if got := nearestLine(nil, 5); got != 0 {
		t.Errorf("nearestLine(nil, 5) = %d, want 0", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// stripJSONFences
// ─────────────────────────────────────────────────────────────────────────────

func TestStripJSONFences(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"[{}]", "[{}]"},
		{"```json\n[{}]\n```", "[{}]"},
		{"```\n[{}]\n```", "[{}]"},
		{"  ```json\n[{}]\n```  ", "[{}]"},
	}
	for _, tc := range tests {
		got := stripJSONFences(tc.in)
		if got != tc.want {
			t.Errorf("stripJSONFences(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// buildVerdict
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildVerdict_NoFindings_Approve(t *testing.T) {
	t.Parallel()
	pr := &github.PullRequest{Number: 1, HTMLURL: "https://github.com/acme/api/pull/1",
		User: github.User{Login: "alice"}, Base: github.Ref{Ref: "main"}, Head: github.Ref{Ref: "feat/x"}}
	event, body := buildVerdict(nil, nil, pr, "acme", "api")
	if event != "APPROVE" {
		t.Errorf("event = %q, want APPROVE", event)
	}
	if !strings.Contains(body, "Ship") {
		t.Errorf("body missing 'Ship': %s", body)
	}
}

func TestBuildVerdict_CriticalFindings_RequestChanges(t *testing.T) {
	t.Parallel()
	findings := []prFinding{
		{Path: "main.go", Line: 5, Severity: "critical", Issue: "SQL injection", Fix: "Use parameterised queries."},
	}
	pr := &github.PullRequest{Number: 2, HTMLURL: "https://github.com/acme/api/pull/2",
		User: github.User{Login: "bob"}, Base: github.Ref{Ref: "main"}, Head: github.Ref{Ref: "feat/y"}}
	event, body := buildVerdict(findings, nil, pr, "acme", "api")
	if event != "REQUEST_CHANGES" {
		t.Errorf("event = %q, want REQUEST_CHANGES", event)
	}
	if !strings.Contains(body, "Hold") {
		t.Errorf("body missing 'Hold': %s", body)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Execute — dry_run with httptest server
// ─────────────────────────────────────────────────────────────────────────────

func newTestGitHubServer(t *testing.T, number int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/pulls/"+itoa(number)) && r.Header.Get("Accept") == "application/vnd.github+json":
			// GetPullRequest
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":   number,
				"state":    "open",
				"title":    "feat: add contact form backend",
				"body":     "Connects the contact form to DynamoDB.",
				"html_url": "https://github.com/acme/repo/pull/2",
				"draft":    false,
				"user":     map[string]any{"login": "prerna"},
				"head":     map[string]any{"ref": "feat/ui-improvements", "sha": "abc123", "repo": map[string]any{"full_name": "acme/repo"}},
				"base":     map[string]any{"ref": "main", "sha": "def456", "repo": map[string]any{"full_name": "acme/repo"}},
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/files"):
			// GetPullRequestFiles
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"filename":  "backend/lambda/index.mjs",
					"status":    "added",
					"additions": 2,
					"deletions": 0,
					"changes":   2,
					"patch":     "@@ -0,0 +7,2 @@\n+// comment\n+const TABLE_NAME = process.env.TABLE_NAME || \"BruditeInfo\";",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func itoa(n int) string {
	return strings.TrimSpace(strings.ReplaceAll(string(rune(n+'0')), "\x00", ""))
}

func TestGithubReviewPRTool_DryRun(t *testing.T) {
	t.Parallel()

	srv := newTestGitHubServer(t, 2)
	defer srv.Close()

	llmResp := `[{"path":"backend/lambda/index.mjs","line":8,"severity":"critical","issue":"Hard-coded fallback table name silently targets wrong table.","fix":"Remove the || \"BruditeInfo\" fallback so TABLE_NAME is required."}]`

	tool := &githubReviewPRTool{
		c:          github.New(github.Config{Token: "tok", BaseURL: srv.URL}, srv.Client()),
		defaultOrg: "acme",
		prov:       &stubProvider{response: llmResp},
		model:      "stub-model",
	}

	input := json.RawMessage(`{"repo":"repo","number":2,"dry_run":true}`)
	out, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("parse output: %v\nraw: %s", err, out)
	}
	if payload["event"] != "REQUEST_CHANGES" {
		t.Errorf("event = %v, want REQUEST_CHANGES", payload["event"])
	}
	comments, ok := payload["comments"].([]any)
	if !ok || len(comments) == 0 {
		t.Errorf("expected at least one inline comment, got: %v", payload["comments"])
	}
	first := comments[0].(map[string]any)
	if first["path"] != "backend/lambda/index.mjs" {
		t.Errorf("comment path = %v, want backend/lambda/index.mjs", first["path"])
	}
}

func TestGithubReviewPRTool_InvalidLine_Clamped(t *testing.T) {
	t.Parallel()

	srv := newTestGitHubServer(t, 2)
	defer srv.Close()

	// LLM returns line 99 which doesn't exist — tool should clamp to nearest valid line (7 or 8).
	llmResp := `[{"path":"backend/lambda/index.mjs","line":99,"severity":"medium","issue":"Hardcoded fallback.","fix":"Remove fallback."}]`

	tool := &githubReviewPRTool{
		c:          github.New(github.Config{Token: "tok", BaseURL: srv.URL}, srv.Client()),
		defaultOrg: "acme",
		prov:       &stubProvider{response: llmResp},
		model:      "stub",
	}
	input := json.RawMessage(`{"repo":"repo","number":2,"dry_run":true}`)
	out, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(out), &payload)

	comments, _ := payload["comments"].([]any)
	if len(comments) == 0 {
		t.Fatal("expected comment to be clamped and kept, not dropped")
	}
	first := comments[0].(map[string]any)
	line := int(first["line"].(float64))
	if line != 7 && line != 8 {
		t.Errorf("clamped line = %d, want 7 or 8", line)
	}
}

func TestGithubReviewPRTool_NoProvider_Error(t *testing.T) {
	t.Parallel()
	tool := &githubReviewPRTool{
		c:          github.New(github.Config{Token: "tok"}, nil),
		defaultOrg: "acme",
		prov:       nil,
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"repo":"repo","number":1}`))
	if err == nil || !strings.Contains(err.Error(), "no LLM provider") {
		t.Errorf("expected 'no LLM provider' error, got %v", err)
	}
}

func TestGithubReviewPRTool_EmptyFindings_Approve(t *testing.T) {
	t.Parallel()

	srv := newTestGitHubServer(t, 2)
	defer srv.Close()

	tool := &githubReviewPRTool{
		c:          github.New(github.Config{Token: "tok", BaseURL: srv.URL}, srv.Client()),
		defaultOrg: "acme",
		prov:       &stubProvider{response: "[]"},
		model:      "stub",
	}
	input := json.RawMessage(`{"repo":"repo","number":2,"dry_run":true}`)
	out, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(out), &payload)
	if payload["event"] != "APPROVE" {
		t.Errorf("event = %v, want APPROVE", payload["event"])
	}
}
