package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

func TestRunTrace_tailRequiresAuth(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "opsintelligence.yaml")
	yaml := `version: 1
state_dir: "` + dir + `"
routing:
  default: "ollama/llama3.2"
providers:
  ollama:
    base_url: "http://127.0.0.1:11434"
    default_model: "llama3.2"
gateway:
  host: "127.0.0.1"
  port: 18790
agent:
  run_trace_file: "logs/test-runtrace.ndjson"
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	tracePath := cfg.Agent.RunTraceFile
	if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
		t.Fatal(err)
	}
	line := map[string]any{"kind": "task_start", "t": "2026-01-01T00:00:00Z", "session_id": "s1"}
	b, _ := json.Marshal(line)
	if err := os.WriteFile(tracePath, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	svc, mux := newTestAuthService(t, cfg)
	seedUser(t, svc, "traceuser", "password-password-password")

	login := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "traceuser",
		"password": "password-password-password",
	}, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login: %d %s", login.StatusCode, readBody(login))
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtrace?which=master&max_lines=50", nil)
	for _, c := range login.Cookies() {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Which string            `json:"which"`
		Path  string            `json:"path"`
		Lines []json.RawMessage `json:"lines"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Path != tracePath {
		t.Fatalf("path %q want %q", body.Path, tracePath)
	}
	if len(body.Lines) != 1 {
		t.Fatalf("lines: %d", len(body.Lines))
	}
}

func TestRunTrace_forbiddenWithoutLogin(t *testing.T) {
	t.Parallel()
	svc, mux := newTestAuthService(t, nil)
	_ = svc
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtrace", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rr.Code)
	}
}
