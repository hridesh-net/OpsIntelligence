// Package dispatcher provides the Claude Code CLI driver.
package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// ClaudeCodeDriver spawns the Claude Code CLI (`claude -p`) and parses its output.
type ClaudeCodeDriver struct {
	log *zap.Logger
}

// NewClaudeCodeDriver creates a driver that spawns the claude CLI.
func NewClaudeCodeDriver(log *zap.Logger) *ClaudeCodeDriver {
	if log == nil {
		log = zap.NewNop()
	}
	return &ClaudeCodeDriver{log: log}
}

// Name returns "claude-code".
func (d *ClaudeCodeDriver) Name() string { return "claude-code" }

// Run executes claude -p in the worktree directory.
func (d *ClaudeCodeDriver) Run(ctx context.Context, req RunRequest, events chan<- Event) Result {
	start := time.Now()
	defer close(events)

	emit := func(e Event) {
		select {
		case events <- e:
		case <-ctx.Done():
		}
	}

	emit(Event{Kind: EventKindLifecycle, Phase: "start", Message: "claude-code run started"})

	prompt := fmt.Sprintf(
		"You are working on a kanban card.\n\nTitle: %s\nDescription: %s\n\n%s",
		req.CardTitle, req.CardDescription, req.SystemPrompt,
	)

	// Write prompt to a temp file so claude can read it.
	promptFile := filepath.Join(req.WorktreePath, ".kanban_prompt.md")
	if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
		emit(Event{Kind: EventKindError, Message: err.Error()})
		return Result{Status: "failed", Error: err.Error(), ElapsedMs: DurationMS(start)}
	}
	defer os.Remove(promptFile)

	cmd := exec.CommandContext(ctx, "claude", "-p", "--output-format", "stream-json", "--no-git", prompt)
	cmd.Dir = req.WorktreePath
	cmd.Env = append(os.Environ(),
		"CLAUDE_CODE_NO_TELEMETRY=1",
		"CLAUDE_CODE_NO_AUTO_UPDATE=1",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		emit(Event{Kind: EventKindError, Message: err.Error()})
		return Result{Status: "failed", Error: err.Error(), ElapsedMs: DurationMS(start)}
	}
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		emit(Event{Kind: EventKindError, Message: err.Error()})
		return Result{Status: "failed", Error: err.Error(), ElapsedMs: DurationMS(start)}
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				emit(Event{Kind: EventKindProgress, Message: string(buf[:n])})
			}
			if err != nil {
				return
			}
		}
	}()

	// Parse stream-json output line by line.
	dec := json.NewDecoder(stdout)
	var fullText string
	for dec.More() {
		if ctx.Err() != nil {
			_ = cmd.Process.Kill()
			emit(Event{Kind: EventKindLifecycle, Phase: "cancelled", Message: "run cancelled"})
			return Result{Status: "stopped", ElapsedMs: DurationMS(start)}
		}
		var line map[string]any
		if err := dec.Decode(&line); err != nil {
			break
		}
		typ, _ := line["type"].(string)
		switch typ {
		case "text":
			text, _ := line["text"].(string)
			fullText += text
			emit(Event{Kind: EventKindText, Message: text})
		case "tool_use":
			name, _ := line["name"].(string)
			emit(Event{Kind: EventKindToolStart, Phase: name, Message: "tool: " + name, Metadata: line})
		case "tool_result":
			emit(Event{Kind: EventKindToolEnd, Message: "tool done", Metadata: line})
		case "error":
			msg, _ := line["message"].(string)
			emit(Event{Kind: EventKindError, Message: msg})
		}
	}

	_ = cmd.Wait()
	emit(Event{Kind: EventKindLifecycle, Phase: "complete", Message: "claude-code run completed"})

	return Result{
		Status:        "completed",
		ResultSummary: truncate(fullText, 500),
		ElapsedMs:     DurationMS(start),
	}
}
