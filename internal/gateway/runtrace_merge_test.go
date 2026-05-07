package gateway

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Confirms the merge does not drop the smaller stream when one source is
// dramatically noisier than the other (regression test for the original
// "noisy master starves sub-agent trace" bug).
func TestMergeRunTraceSources_keepsAllSourcesRepresented(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	master := filepath.Join(dir, "master.ndjson")
	sub := filepath.Join(dir, "sub.ndjson")

	var mb bytes.Buffer
	for i := 0; i < 600; i++ {
		line := map[string]any{
			"kind": "model_iteration", "t": "2026-05-06T12:00:00.000000001Z",
			"iteration": i + 1,
		}
		b, _ := json.Marshal(line)
		mb.Write(append(b, '\n'))
	}
	if err := os.WriteFile(master, mb.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	var sb bytes.Buffer
	for i := 0; i < 10; i++ {
		line := map[string]any{
			"kind": "task_start", "t": "2026-05-06T11:00:00.000000001Z",
			"runner_role": "specialist:test", "query_preview": "hello",
		}
		b, _ := json.Marshal(line)
		sb.Write(append(b, '\n'))
	}
	if err := os.WriteFile(sub, sb.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	sources := []traceSource{
		{path: master, label: "master"},
		{path: sub, label: "subagents"},
	}
	lines, _, paths, err := mergeRunTraceSources(sources, 800)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("paths: %v", paths)
	}
	var fromSubFile int
	for _, raw := range lines {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if rr, _ := m["runner_role"].(string); rr == "specialist:test" {
			fromSubFile++
		}
	}
	if fromSubFile == 0 {
		t.Fatalf("expected lines from sub trace file in merged output, got %d lines total", len(lines))
	}
}

// Verifies the discovery walker picks up per-run sub-agent runtrace files
// under <LogsSubagentsDir>/<name>/<run-id>/runtrace.ndjson — the layout used
// by specialist and async sub-agent runs.
func TestDiscoverTraceSources_includesPerRunSubagentFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	logsAgent := filepath.Join(root, "logs", "agent")
	logsSub := filepath.Join(root, "logs", "subagents")
	if err := os.MkdirAll(logsAgent, 0o755); err != nil {
		t.Fatal(err)
	}
	specDir := filepath.Join(logsSub, "pr_review", "a0689349")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	masterPath := filepath.Join(logsAgent, "runtrace.ndjson")
	if err := os.WriteFile(masterPath, []byte(`{"kind":"task_start","t":"2026-05-06T11:00:00Z"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(specDir, "runtrace.ndjson")
	if err := os.WriteFile(specPath, []byte(`{"kind":"task_start","t":"2026-05-06T11:01:00Z"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := &AuthService{
		RunTraceMaster:   masterPath,
		RunTraceSubagent: filepath.Join(logsSub, "runtrace.ndjson"),
		LogsAgentDir:     logsAgent,
		LogsSubagentsDir: logsSub,
	}

	t.Run("subagent picks up per-run dir", func(t *testing.T) {
		sources := svc.discoverTraceSources("subagent")
		var found bool
		for _, s := range sources {
			if s.path == specPath {
				found = true
				if s.label != "sub:pr_review:a0689349" {
					t.Fatalf("label = %q", s.label)
				}
			}
		}
		if !found {
			t.Fatalf("expected per-run trace in sources, got %+v", sources)
		}
	})

	t.Run("all merges master + subagents", func(t *testing.T) {
		sources := svc.discoverTraceSources("all")
		var hasMaster, hasSpec bool
		for _, s := range sources {
			if s.path == masterPath {
				hasMaster = true
			}
			if s.path == specPath {
				hasSpec = true
			}
		}
		if !hasMaster || !hasSpec {
			t.Fatalf("expected both master and per-run sub trace, got %+v", sources)
		}
	})
}
