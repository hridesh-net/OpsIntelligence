// Package dispatcher provides the Codex CLI driver.
package dispatcher

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.uber.org/zap"
)

// CodexDriver spawns the OpenAI Codex CLI.
type CodexDriver struct {
	log *zap.Logger
}

// NewCodexDriver creates a driver that spawns the codex CLI.
func NewCodexDriver(log *zap.Logger) *CodexDriver {
	if log == nil {
		log = zap.NewNop()
	}
	return &CodexDriver{log: log}
}

// Name returns "codex".
func (d *CodexDriver) Name() string { return "codex" }

// Run executes codex in the worktree directory.
func (d *CodexDriver) Run(ctx context.Context, req RunRequest, events chan<- Event) Result {
	start := time.Now()
	defer close(events)

	emit := func(e Event) {
		select {
		case events <- e:
		case <-ctx.Done():
		}
	}

	emit(Event{Kind: EventKindLifecycle, Phase: "start", Message: "codex run started"})

	prompt := fmt.Sprintf(
		"You are working on a kanban card.\n\nTitle: %s\nDescription: %s\n\n%s",
		req.CardTitle, req.CardDescription, req.SystemPrompt,
	)

	promptFile := filepath.Join(req.WorktreePath, ".kanban_prompt.md")
	if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
		emit(Event{Kind: EventKindError, Message: err.Error()})
		return Result{Status: "failed", Error: err.Error(), ElapsedMs: DurationMS(start)}
	}
	defer os.Remove(promptFile)

	cmd := exec.CommandContext(ctx, "codex", "-q", "-f", promptFile)
	cmd.Dir = req.WorktreePath

	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		emit(Event{Kind: EventKindLifecycle, Phase: "cancelled", Message: "run cancelled"})
		return Result{Status: "stopped", ElapsedMs: DurationMS(start)}
	}
	if err != nil {
		emit(Event{Kind: EventKindError, Message: err.Error()})
		return Result{Status: "failed", Error: err.Error(), ElapsedMs: DurationMS(start)}
	}

	text := string(out)
	emit(Event{Kind: EventKindText, Message: text})
	emit(Event{Kind: EventKindLifecycle, Phase: "complete", Message: "codex run completed"})

	return Result{
		Status:        "completed",
		ResultSummary: truncate(text, 500),
		ElapsedMs:     DurationMS(start),
	}
}
