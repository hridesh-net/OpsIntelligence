package dispatcher

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// CodexDriver spawns the OpenAI Codex CLI and parses its output.
type CodexDriver struct{}

// NewCodexDriver creates a new Codex CLI driver.
func NewCodexDriver() *CodexDriver {
	return &CodexDriver{}
}

// Type returns "codex".
func (d *CodexDriver) Type() string { return "codex" }

// SupportedModels returns Codex model identifiers.
func (d *CodexDriver) SupportedModels() []string {
	return []string{"gpt-5", "gpt-4o", "gpt-4o-mini"}
}

// Run spawns `codex` in the worktree.
func (d *CodexDriver) Run(ctx context.Context, opts RunOpts) (<-chan Event, context.CancelFunc, error) {
	events := make(chan Event, 64)
	ctx, cancel := context.WithCancel(ctx)

	// Codex CLI accepts prompts via stdin or as arguments.
	// We use the exec mode: `codex --approval-mode auto-quiet <prompt>`
	cmd := exec.CommandContext(ctx, "codex",
		"--approval-mode", "auto-quiet",
		opts.Prompt,
	)
	if opts.WorktreePath != "" {
		cmd.Dir = opts.WorktreePath
	}
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("codex: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("codex: start: %w", err)
	}

	go func() {
		defer close(events)

		go func() {
			defer cancel()
			scanner := bufio.NewScanner(stdout)
			const maxCapacity = 1024 * 1024 // 1MB buffer for large output lines
			buf := make([]byte, 64*1024)
			scanner.Buffer(buf, maxCapacity)
			for scanner.Scan() {
				line := scanner.Text()
				select {
				case events <- Event{Kind: "text", Message: line}:
				case <-ctx.Done():
					return
				}
			}
			_ = cmd.Wait()
			events <- Event{Kind: "done"}
		}()

		<-ctx.Done()
		_ = cmd.Process.Kill()
	}()

	return events, cancel, nil
}
