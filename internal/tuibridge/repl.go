package tuibridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// TokenUsage mirrors agent.TokenUsageSnapshot fields needed for the REPL footer.
// Kept here so callers don't have to depend on the agent package.
type TokenUsage struct {
	PromptTokens     uint64 `json:"prompt_tokens"`
	CompletionTokens uint64 `json:"completion_tokens"`
	TotalTokens      uint64 `json:"total_tokens"`
}

// RunResult is reported to the Rust TUI when an agent turn completes.
type RunResult struct {
	Iterations uint32     `json:"iterations"`
	Usage      TokenUsage `json:"usage"`
}

// AgentStreamHandler matches the shape of tui.AgentStreamHandler so an existing
// runner can be re-targeted at this bridge with a trivial adapter.
type AgentStreamHandler interface {
	OnToken(token string)
	OnToolCall(name string, input json.RawMessage)
	OnToolResult(name, result string)
	OnDone(result *RunResult)
	OnError(err error)
}

// AgentRunner is the minimal contract the bridge needs to drive an agent turn.
type AgentRunner interface {
	SessionID() string
	RunStream(ctx context.Context, msg string, h AgentStreamHandler)
}

// ReplOptions configures the REPL launch.
type ReplOptions struct {
	Runner        AgentRunner
	Version       string
	ModelName     string
	ProviderCount uint32
	SkillCount    uint32
	Banner        string
	LogDir        string
}

// RunREPL launches the embedded Rust TUI in REPL mode, wires user input to the
// supplied AgentRunner, and streams events back to the TUI. Blocks until the
// user quits (Esc/Ctrl+C) or the Rust subprocess exits.
func RunREPL(ctx context.Context, opts ReplOptions) error {
	if opts.Runner == nil {
		return errors.New("tuibridge: ReplOptions.Runner is required")
	}

	type submitMsg struct {
		Text string `json:"text"`
	}

	state := struct {
		mu           sync.Mutex
		currentCxl   context.CancelFunc
		quitRequested bool
	}{}

	quit := make(chan struct{})
	requestQuit := func() {
		state.mu.Lock()
		if !state.quitRequested {
			state.quitRequested = true
			close(quit)
		}
		state.mu.Unlock()
	}

	var bridge *Bridge

	handler := func(msg Message) {
		switch msg.Method {
		case "command.submit":
			var p submitMsg
			if err := json.Unmarshal(msg.Params, &p); err != nil || p.Text == "" {
				return
			}
			turnCtx, cancel := context.WithCancel(ctx)
			state.mu.Lock()
			if state.currentCxl != nil {
				state.currentCxl() // cancel any prior in-flight turn (shouldn't happen)
			}
			state.currentCxl = cancel
			state.mu.Unlock()
			go opts.Runner.RunStream(turnCtx, p.Text, &streamForwarder{bridge: &bridge, done: func() {
				state.mu.Lock()
				state.currentCxl = nil
				state.mu.Unlock()
			}})
		case "command.cancel":
			state.mu.Lock()
			if state.currentCxl != nil {
				state.currentCxl()
				state.currentCxl = nil
			}
			state.mu.Unlock()
		case "view.exit":
			requestQuit()
		}
	}

	b, err := Spawn(ctx, Options{
		Handler: handler,
		LogDir:  opts.LogDir,
	})
	if err != nil {
		return err
	}
	bridge = b
	defer func() { _ = b.Close(2 * time.Second) }()

	// Push the REPL view spec; the Rust side queues stdin from spawn-time so
	// this is safe to send immediately.
	if err := b.Send("view.push", map[string]any{
		"view": "repl",
		"repl": map[string]any{
			"session_id":     opts.Runner.SessionID(),
			"version":        opts.Version,
			"model_name":     opts.ModelName,
			"provider_count": opts.ProviderCount,
			"skill_count":    opts.SkillCount,
			"banner":         StripANSI(opts.Banner),
		},
	}); err != nil {
		return err
	}

	select {
	case <-quit:
		return nil
	case <-b.Done():
		return b.CloseErr()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// streamForwarder converts AgentStreamHandler callbacks into JSON-RPC
// notifications on the bridge.
type streamForwarder struct {
	bridge **Bridge
	done   func()
}

func (s *streamForwarder) send(method string, params any) {
	if s.bridge == nil || *s.bridge == nil {
		return
	}
	_ = (*s.bridge).Send(method, params)
}

func (s *streamForwarder) OnToken(t string) {
	s.send("agent.delta", map[string]any{"kind": "token", "text": StripANSI(t)})
}

func (s *streamForwarder) OnToolCall(name string, input json.RawMessage) {
	s.send("agent.delta", map[string]any{
		"kind":  "tool_call",
		"name":  name,
		"input": snippetFromJSON(input),
	})
}

func (s *streamForwarder) OnToolResult(name, result string) {
	s.send("agent.delta", map[string]any{
		"kind":   "tool_result",
		"name":   name,
		"result": StripANSI(resultSnippet(result)),
	})
}

func (s *streamForwarder) OnDone(r *RunResult) {
	if r == nil {
		r = &RunResult{}
	}
	s.send("agent.end", r)
	if s.done != nil {
		s.done()
	}
}

func (s *streamForwarder) OnError(err error) {
	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	}
	s.send("agent.error", map[string]any{"message": msg})
	if s.done != nil {
		s.done()
	}
}

// snippetFromJSON extracts a compact param string from a tool input JSON object.
// Port of cmd/opsintelligence/tui/repl.go::snippetFromJSON.
func snippetFromJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	priority := []string{"path", "command", "query", "url", "file", "repo", "owner", "name", "id"}
	for _, k := range priority {
		if v, ok := m[k]; ok {
			return k + "=" + truncRunes(fmt.Sprintf("%v", v), 60)
		}
	}
	for k, v := range m {
		return k + "=" + truncRunes(fmt.Sprintf("%v", v), 60)
	}
	return ""
}

// resultSnippet returns a one-line summary of a tool result.
func resultSnippet(result string) string {
	result = trim(result)
	if len(result) > 0 && result[0] == '{' {
		var m map[string]json.RawMessage
		if json.Unmarshal([]byte(result), &m) == nil {
			for _, key := range []string{
				"verdict", "title", "message", "reason", "state",
				"conclusion", "status", "name", "error",
			} {
				if raw, ok := m[key]; ok {
					var s string
					if json.Unmarshal(raw, &s) == nil && s != "" {
						return truncRunes(s, 100)
					}
				}
			}
			return fmt.Sprintf("(%d fields)", len(m))
		}
	}
	return firstLine(result, 120)
}

func firstLine(s string, max int) string {
	for _, line := range splitLines(s) {
		line = trim(line)
		if line == "" {
			continue
		}
		return truncRunes(line, max)
	}
	return ""
}

func truncRunes(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max]) + "…"
}

func splitLines(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end {
		c := s[start]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		start++
	}
	for end > start {
		c := s[end-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		end--
	}
	return s[start:end]
}
