package gateway

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeRunTrace_splitsLineBudgetAcrossFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	master := filepath.Join(dir, "master.ndjson")
	sub := filepath.Join(dir, "sub.ndjson")

	var mb bytes.Buffer
	// 600 master lines (newer timestamps) — would dominate a naive "last 800 of 1600" merge.
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
	// 10 sub-agent lines (older) — must still appear when maxLines=800.
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

	lines, _, paths, err := mergeRunTrace(master, sub, 800)
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
