package dispatcher

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// ClaudeCodeDriver spawns the Claude Code CLI (`claude -p`) and parses stream-json output.
type ClaudeCodeDriver struct{}

// NewClaudeCodeDriver creates a new Claude Code CLI driver.
func NewClaudeCodeDriver() *ClaudeCodeDriver {
	return &ClaudeCodeDriver{}
}

// Type returns "claude-code".
func (d *ClaudeCodeDriver) Type() string { return "claude-code" }

// SupportedModels returns Claude model identifiers.
func (d *ClaudeCodeDriver) SupportedModels() []string {
	return []string{
		"claude-opus-4", "claude-sonnet-4", "claude-haiku-4",
		"claude-3-opus", "claude-3-sonnet", "claude-3-haiku",
	}
}

// Run spawns `claude -p --output-format stream-json` in the worktree.
func (d *ClaudeCodeDriver) Run(ctx context.Context, opts RunOpts) (<-chan Event, context.CancelFunc, error) {
	events := make(chan Event, 64)
	ctx, cancel := context.WithCancel(ctx)

	cmd := exec.CommandContext(ctx, "claude",
		"-p",
		"--output-format", "stream-json",
		"--dangerously-skip-permissions",
		opts.Prompt,
	)
	if opts.WorktreePath != "" {
		cmd.Dir = opts.WorktreePath
	}
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("claude-code: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("claude-code: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("claude-code: start: %w", err)
	}

	go func() {
		defer close(events)

		// Stream parser goroutine
		go func() {
			defer cancel() // ensure cancel is called when parser finishes
			scanner := bufio.NewScanner(stdout)
			const maxCapacity = 1024 * 1024 // 1MB buffer for large JSON lines
			buf := make([]byte, 64*1024)
			scanner.Buffer(buf, maxCapacity)
			for scanner.Scan() {
				line := scanner.Text()
				if line == "" {
					continue
				}
				ev, ok := parseClaudeStreamJSON(line)
				if ok {
					select {
					case events <- ev:
					case <-ctx.Done():
						return
					}
				}
			}
			// Drain stderr for errors
			_ = stderr
			_ = cmd.Wait()
			events <- Event{Kind: "done"}
		}()

		<-ctx.Done()
		_ = cmd.Process.Kill()
	}()

	return events, cancel, nil
}

// parseClaudeStreamJSON parses a single line of Claude Code stream-json output.
func parseClaudeStreamJSON(line string) (Event, bool) {
	var raw map[string]any
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return Event{Kind: "text", Message: line}, true
	}

	typeVal, _ := raw["type"].(string)
	switch typeVal {
	case "thinking":
		content, _ := raw["content"].(string)
		return Event{Kind: "text", Phase: "thinking", Message: content}, true
	case "tool_use":
		name, _ := raw["name"].(string)
		input, _ := json.Marshal(raw["input"])
		return Event{Kind: "tool_start", Message: name, Metadata: map[string]any{"tool": name, "input": string(input)}}, true
	case "tool_result":
		name, _ := raw["name"].(string)
		content, _ := raw["content"].(string)
		return Event{Kind: "tool_end", Message: name, Metadata: map[string]any{"tool": name, "result": content}}, true
	case "error":
		msg, _ := raw["error"].(string)
		return Event{Kind: "error", Message: msg}, true
	default:
		// Fallback: treat as text
		content, _ := raw["content"].(string)
		if content == "" {
			b, _ := json.Marshal(raw)
			content = string(b)
		}
		return Event{Kind: "text", Message: content}, true
	}
}
