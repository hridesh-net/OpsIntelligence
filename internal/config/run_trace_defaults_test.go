package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadForDoctor_runTraceAutoDefault(t *testing.T) {
	t.Parallel()
	path := filepath.Join("testdata", "doctor", "valid_minimal.yaml")
	cfg, _, err := LoadForDoctor(path)
	if err != nil {
		t.Fatalf("LoadForDoctor: %v", err)
	}
	if cfg == nil {
		t.Fatal("nil cfg")
	}
	want := filepath.Join(cfg.StateDir, "logs", "runtrace.ndjson")
	if cfg.Agent.RunTraceFile != want {
		t.Fatalf("RunTraceFile = %q, want %q", cfg.Agent.RunTraceFile, want)
	}
	if cfg.Agent.RunTraceMode != "auto" {
		t.Fatalf("RunTraceMode = %q, want auto", cfg.Agent.RunTraceMode)
	}
}

func TestLoadForDoctor_runTraceModeOff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	content := `version: 1
state_dir: "` + dir + `"
agent:
  run_trace_mode: off
routing:
  default: "ollama/llama3.2"
providers:
  ollama:
    base_url: "http://127.0.0.1:11434"
    default_model: "llama3.2"
gateway:
  host: "127.0.0.1"
  port: 18790
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := LoadForDoctor(cfgPath)
	if err != nil {
		t.Fatalf("LoadForDoctor: %v", err)
	}
	if cfg.Agent.RunTraceFile != "" || cfg.Agent.RunTraceSubagentFile != "" {
		t.Fatalf("expected tracing disabled, got master=%q sub=%q", cfg.Agent.RunTraceFile, cfg.Agent.RunTraceSubagentFile)
	}
	if cfg.Agent.RunTraceMode != "off" {
		t.Fatalf("RunTraceMode = %q", cfg.Agent.RunTraceMode)
	}
}

func TestApplyAgentRunTrace_envDisables(t *testing.T) {
	t.Setenv("OPSINTELLIGENCE_RUN_TRACE_MODE", "off")
	cfg := &Config{
		StateDir: t.TempDir(),
		Agent: AgentConfig{
			RunTraceMode: "auto",
			RunTraceFile: "logs/custom.ndjson",
		},
	}
	applyAgentRunTrace(cfg)
	if cfg.Agent.RunTraceFile != "" {
		t.Fatalf("expected empty RunTraceFile, got %q", cfg.Agent.RunTraceFile)
	}
}

func TestApplyAgentRunTrace_envFileOverride(t *testing.T) {
	t.Setenv("OPSINTELLIGENCE_RUN_TRACE_FILE", "logs/from-env.ndjson")
	cfg := &Config{
		StateDir: t.TempDir(),
		Agent:    AgentConfig{},
	}
	applyAgentRunTrace(cfg)
	if !strings.HasSuffix(cfg.Agent.RunTraceFile, "logs/from-env.ndjson") {
		t.Fatalf("RunTraceFile = %q", cfg.Agent.RunTraceFile)
	}
}
