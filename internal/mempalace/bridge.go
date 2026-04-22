package mempalace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// RecallResult is a single hit returned by MemPalace recall.
type RecallResult struct {
	Key     string  `json:"key"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// Bridge wraps the bootstrapped MemPalace CLI so Go code can recall and store
// entries without going through the MCP server.  It is intentionally thin —
// the authoritative data always lives in the Python MemPalace world; this is
// just a subprocess adapter.
//
// Prerequisite: Ensure() must have been called at least once for StateDir.
type Bridge struct {
	StateDir string
	Log      *zap.Logger
}

// Recall queries the MemPalace world for entries semantically similar to query.
// Returns at most limit results.  Handles both JSON and plain-text CLI output.
func (b *Bridge) Recall(ctx context.Context, query string, limit int) ([]RecallResult, error) {
	cli, err := b.cliPath()
	if err != nil {
		return nil, err
	}
	world := ManagedWorldDir(b.StateDir)
	if limit <= 0 {
		limit = 5
	}

	// Try the structured JSON flag first; fall back to plain recall if the
	// installed version of mempalace does not support --json.
	raw, jsonErr := b.runCLI(ctx, cli, "recall", world,
		"--query", query,
		"--limit", fmt.Sprintf("%d", limit),
		"--json",
	)
	if jsonErr == nil {
		var results []RecallResult
		if err := json.Unmarshal(raw, &results); err == nil {
			return results, nil
		}
		// JSON parse failed — fall through and treat output as plain text
	}

	// Plain text fallback
	raw, err = b.runCLI(ctx, cli, "recall", world,
		"--query", query,
		"--limit", fmt.Sprintf("%d", limit),
	)
	if err != nil {
		return nil, fmt.Errorf("mempalace recall: %w", err)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil, nil
	}
	return []RecallResult{{Key: "recall", Content: text, Score: 1.0}}, nil
}

// Store persists a key+content pair into the MemPalace world.
func (b *Bridge) Store(ctx context.Context, key, content string) error {
	cli, err := b.cliPath()
	if err != nil {
		return err
	}
	world := ManagedWorldDir(b.StateDir)
	_, err = b.runCLI(ctx, cli, "store", world, "--key", key, "--content", content)
	return err
}

// Ready reports whether the MemPalace venv and world are fully initialised.
func (b *Bridge) Ready() bool {
	venvRoot := ManagedVenvRoot(b.StateDir)
	cli := VenvMempalaceCLI(venvRoot)
	if _, err := os.Stat(cli); err != nil {
		vpy := VenvInterpreter(venvRoot)
		if _, err2 := os.Stat(vpy); err2 != nil {
			return false
		}
	}
	_, err := os.Stat(WorldInitMarker(b.StateDir))
	return err == nil
}

// cliPath returns the absolute path to the mempalace CLI binary (or module
// entry-point) inside the managed venv, returning a helpful error if the
// venv has not been bootstrapped yet.
func (b *Bridge) cliPath() (string, error) {
	venvRoot := ManagedVenvRoot(b.StateDir)
	cli := VenvMempalaceCLI(venvRoot)
	if _, err := os.Stat(cli); err == nil {
		return cli, nil
	}
	// Some installs put the CLI under Scripts/ (Windows) — check the interpreter instead.
	vpy := VenvInterpreter(venvRoot)
	if _, err := os.Stat(vpy); err == nil {
		// Will invoke via `python -m mempalace`
		return "", nil // signal runCLI to use python -m mode
	}
	return "", fmt.Errorf("mempalace bridge: venv not found at %q — run `opsintelligence mempalace setup` first",
		venvRoot)
}

// runCLI executes the MemPalace CLI with the given arguments.
// When cliExe is empty it falls back to `python -m mempalace`.
func (b *Bridge) runCLI(ctx context.Context, cliExe string, args ...string) ([]byte, error) {
	var cmd *exec.Cmd
	if cliExe != "" {
		cmd = exec.CommandContext(ctx, cliExe, args...)
	} else {
		vpy := VenvInterpreter(ManagedVenvRoot(b.StateDir))
		mArgs := append([]string{"-m", "mempalace"}, args...)
		cmd = exec.CommandContext(ctx, vpy, mArgs...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w\nstderr: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// NewBridge is a convenience constructor.
func NewBridge(stateDir string, log *zap.Logger) *Bridge {
	return &Bridge{StateDir: stateDir, Log: log}
}

// ─────────────────────────────────────────────
// MemPalace-aware semantic search (for runner injection)
// ─────────────────────────────────────────────

// SearchResults converts MemPalace recall hits into a formatted string suitable
// for system-prompt injection.  Returns "" when no results are available or the
// bridge is not ready.
func (b *Bridge) FormatForPrompt(ctx context.Context, query string, limit int) string {
	if b == nil || !b.Ready() {
		return ""
	}
	results, err := b.Recall(ctx, query, limit)
	if err != nil {
		if b.Log != nil {
			b.Log.Debug("mempalace recall failed", zap.String("query", query), zap.Error(err))
		}
		return ""
	}
	if len(results) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## MemPalace Retrieved Knowledge\n")
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("**[%d]** key=%s score=%.2f\n%s\n\n",
			i+1, r.Key, r.Score, r.Content))
	}
	return sb.String()
}

// ─────────────────────────────────────────────
// Path helper (used internally)
// ─────────────────────────────────────────────

// venvMempalaceCLIAlt tries alternative executable paths (cross-platform).
func venvMempalaceCLIAlt(venvRoot string) []string {
	return []string{
		VenvMempalaceCLI(venvRoot),
		filepath.Join(venvRoot, "Scripts", "mempalace.exe"), // Windows
		filepath.Join(venvRoot, "Scripts", "mempalace"),     // Windows no ext
	}
}
