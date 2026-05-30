package dispatcher

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// ACPDriver speaks the Agent Client Protocol over stdio. ACP is a thin
// JSON-RPC-ish framing on top of which an agent CLI emits one event per
// line. Lines are objects of the form:
//
//	{"kind":"text"|"tool_start"|"tool_end"|"decision"|"error"|"done",
//	 "phase":"thinking"|"running"|...,
//	 "message":"...",
//	 "metadata":{...}}
//
// This driver lets the OpsIntelligence kanban target any ACP-compliant
// CLI without writing a per-tool adapter — kanbots.dev calls this
// "any ACP-compatible CLI."
//
// Configuration is just the binary name + arg template, mirroring
// GenericLineDriver's shape. The frame parser is the only addition.
type ACPDriver struct {
	TypeID string
	Models []string
	Binary string
	ArgsFn func(opts RunOpts) []string
	EnvFn  func(opts RunOpts) []string
}

// NewACPDriver constructs an ACPDriver bound to the given executable.
// The default ArgsFn passes `--prompt <prompt>` which is the ACP
// convention; override it via the returned struct if your CLI uses
// different flags.
func NewACPDriver(binary string) *ACPDriver {
	return &ACPDriver{
		TypeID: "acp",
		Binary: binary,
		ArgsFn: func(opts RunOpts) []string {
			a := []string{"--protocol", "acp", "--prompt", opts.Prompt}
			if opts.Model != "" {
				a = append(a, "--model", opts.Model)
			}
			return a
		},
	}
}

func (d *ACPDriver) Type() string             { return d.TypeID }
func (d *ACPDriver) SupportedModels() []string { return d.Models }

// Run starts the child, reads framed JSON events, and translates them
// into the dispatcher's Event channel. Unknown `kind` values fall
// through as plain `text` so we never silently swallow output.
func (d *ACPDriver) Run(ctx context.Context, opts RunOpts) (<-chan Event, context.CancelFunc, error) {
	events := make(chan Event, 64)
	ctx, cancel := context.WithCancel(ctx)

	var args []string
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
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("acp: stdout pipe: %w", err)
	}
	stderr, _ := cmd.StderrPipe() // best-effort; we still proceed if nil

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, fmt.Errorf("acp: start: %w", err)
	}

	go func() {
		defer close(events)
		// Drain stderr into `text` events so panic dumps don't disappear.
		if stderr != nil {
			go drainStderr(ctx, stderr, events)
		}
		go func() {
			defer cancel()
			scanner := bufio.NewScanner(stdout)
			scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
			for scanner.Scan() {
				line := scanner.Bytes()
				if len(line) == 0 {
					continue
				}
				ev := parseACPFrame(line)
				select {
				case events <- ev:
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

// parseACPFrame turns a single JSON line into an Event. Anything that
// doesn't validate as ACP gets passed through as a `text` event so the
// run timeline still records the raw output for forensic review.
func parseACPFrame(line []byte) Event {
	var frame struct {
		Kind     string         `json:"kind"`
		Phase    string         `json:"phase"`
		Message  string         `json:"message"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(line, &frame); err != nil || frame.Kind == "" {
		return Event{Kind: "text", Message: string(line)}
	}
	return Event{
		Kind:     frame.Kind,
		Phase:    frame.Phase,
		Message:  frame.Message,
		Metadata: frame.Metadata,
	}
}

func drainStderr(ctx context.Context, r io.Reader, events chan<- Event) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case events <- Event{Kind: "text", Phase: "stderr", Message: scanner.Text()}:
		case <-ctx.Done():
			return
		}
	}
}
