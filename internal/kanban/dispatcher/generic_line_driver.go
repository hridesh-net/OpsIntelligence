package dispatcher

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// GenericLineDriver spawns an external agent CLI that emits plain text
// (one line at a time) to stdout and turns each line into a `text` event.
// It's the lowest-common-denominator driver: works for every CLI that
// reads a prompt from argv/stdin and writes free-form output. Specific
// drivers (claude-code's stream-json, ACP's framed protocol) extend or
// replace this.
//
// Concrete drivers below (Gemini, Cursor, Copilot, ...) just configure
// the spawn spec instead of duplicating I/O plumbing.
type GenericLineDriver struct {
	// TypeID is what the dispatch service matches on (agent_type column).
	TypeID string
	// Models is the list of model identifiers this CLI advertises. Used
	// by the UI for the model picker dropdown.
	Models []string
	// Binary is the executable to spawn (e.g. "gemini").
	Binary string
	// ArgsFn builds the argv vector for a single run. Receives the run
	// options so subclasses can splice in the model, worktree, etc.
	// Must return at least the equivalent of `{prompt}` — we don't write
	// to the child's stdin by default.
	ArgsFn func(opts RunOpts) []string
	// EnvFn returns the environment the child should inherit. Defaults
	// to os.Environ() when nil.
	EnvFn func(opts RunOpts) []string
	// StdinFn, when non-nil, is called with the child's stdin pipe so a
	// driver can write the prompt there instead of via argv. Useful for
	// CLIs (like some chat-mode tools) that reject large prompts on the
	// command line.
	StdinFn func(w io.WriteCloser, opts RunOpts) error
}

// Type returns the driver's stable identifier (matches BoardAgent.AgentType).
func (d *GenericLineDriver) Type() string { return d.TypeID }

// SupportedModels returns the model list the UI shows for this driver.
func (d *GenericLineDriver) SupportedModels() []string { return d.Models }

// Run spawns the CLI inside the run's worktree (if any), tails stdout, and
// emits each line as a `text` event. Returns a `done` event on exit.
func (d *GenericLineDriver) Run(ctx context.Context, opts RunOpts) (<-chan Event, context.CancelFunc, error) {
	events := make(chan Event, 64)
	ctx, cancel := context.WithCancel(ctx)

	args := []string{}
	if d.ArgsFn != nil {
		args = d.ArgsFn(opts)
	}
	cmd := exec.CommandContext(ctx, d.Binary, args...)
	if opts.WorktreePath != "" {
		cmd.Dir = opts.WorktreePath
	}
	if d.EnvFn != nil {
		cmd.Env = d.EnvFn(opts)
	} else {
		cmd.Env = os.Environ()
	}

	var stdin io.WriteCloser
	if d.StdinFn != nil {
		p, err := cmd.StdinPipe()
		if err != nil {
			cancel()
			return nil, nil, fmt.Errorf("%s: stdin pipe: %w", d.TypeID, err)
		}
		stdin = p
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("%s: stdout pipe: %w", d.TypeID, err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("%s: start: %w", d.TypeID, err)
	}

	// Hand the prompt to the child via stdin if the driver opted in.
	if stdin != nil && d.StdinFn != nil {
		go func() {
			defer stdin.Close()
			if err := d.StdinFn(stdin, opts); err != nil {
				select {
				case events <- Event{Kind: "error", Message: err.Error()}:
				case <-ctx.Done():
				}
			}
		}()
	}

	go func() {
		defer close(events)

		go func() {
			defer cancel()
			scanner := bufio.NewScanner(stdout)
			// 1 MB line buffer: long enough for typical agent output without
			// risking pathological mid-line memory blowup.
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			for scanner.Scan() {
				select {
				case events <- Event{Kind: "text", Message: scanner.Text()}:
				case <-ctx.Done():
					return
				}
			}
			if err := cmd.Wait(); err != nil {
				select {
				case events <- Event{Kind: "error", Message: err.Error()}:
				case <-ctx.Done():
				}
			}
			select {
			case events <- Event{Kind: "done"}:
			case <-ctx.Done():
			}
		}()

		<-ctx.Done()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	return events, cancel, nil
}

// promptArgs is the trivial `{prompt}` argv used by most CLIs.
func promptArgs(opts RunOpts) []string { return []string{opts.Prompt} }
