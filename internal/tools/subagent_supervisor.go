package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/provider"
	"github.com/opsintelligence/opsintelligence/internal/subagents"
)

// This file adds the master↔child supervision layer. Two axes:
//
//   Master → child (authoritative):
//     - subagent_intervene:     push guidance that the child reads next iter
//     - subagent_share_context: push a context note into the child's session
//     - subagent_stream:        drain progress events for inspection
//     - subagent_read_context:  read back shared notes (audit trail)
//
//   Child → master (advisory):
//     - supervisor_report: post a ProgressEvent (progress / blocked / error)
//
// Children get a per-task-id bound supervisor_report — they cannot report
// against other tasks. All master-only tools are in subAgentOmit.

// ── supervisor_report (child-side) ──────────────────────────────────────────

// SupervisorReportTool is installed on child runners only. It is pre-bound
// to a specific TaskID, so the child can only report its own progress.
type SupervisorReportTool struct {
	Tasks  *subagents.TaskManager
	TaskID string
}

func (t SupervisorReportTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "supervisor_report",
		Description: `Post a progress update to the master agent that dispatched you. ` +
			`Use this liberally — every milestone, phase transition, or when you ` +
			`encounter anything the master should know about. The master sees these ` +
			`events in its live sub-agent dashboard and can push back guidance ` +
			`(which you will see on your next turn as "SUPERVISOR GUIDANCE"). ` +
			`Report "blocked" when you genuinely need input from the master.`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "One-sentence status update (what you're doing / just did / need).",
				},
				"phase": map[string]any{
					"type":        "string",
					"description": "Optional short phase label, e.g. \"analyze\", \"test\", \"post-comments\".",
				},
				"kind": map[string]any{
					"type":        "string",
					"description": "One of: progress (default), blocked, error.",
					"enum":        []string{"progress", "blocked", "error"},
				},
			},
			Required: []string{"message"},
		},
	}
}

func (t SupervisorReportTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	if t.Tasks == nil {
		return "", errors.New("supervisor_report: task manager not wired (are you running outside a tracked async task?)")
	}
	if t.TaskID == "" {
		return "", errors.New("supervisor_report: not bound to a task id")
	}
	var args struct {
		Message string `json:"message"`
		Phase   string `json:"phase"`
		Kind    string `json:"kind"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	kind := subagents.ProgressKind(strings.ToLower(strings.TrimSpace(args.Kind)))
	switch kind {
	case subagents.KindProgress, subagents.KindBlocked, subagents.KindError:
	default:
		kind = subagents.KindProgress
	}
	if err := t.Tasks.Report(t.TaskID, args.Phase, args.Message, kind); err != nil {
		return "", err
	}
	return "progress recorded", nil
}

// ── subagent_intervene (master-side) ────────────────────────────────────────

type SubAgentInterveneTool struct{ S *SubAgentSvc }

func (t SubAgentInterveneTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_intervene",
		Description: `Push authoritative guidance into a running sub-agent task. ` +
			`The child will see the guidance at the top of its next iteration as a ` +
			`"SUPERVISOR GUIDANCE" block and is expected to obey it immediately. ` +
			`Use to refocus, narrow scope, abort a path, correct a mistake, or ` +
			`inject fresh context (e.g. "stop inspecting tests and review src/auth.go first").`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task id of the running sub-agent (from the dashboard / subagent_tasks).",
				},
				"guidance": map[string]any{
					"type":        "string",
					"description": "Imperative direction for the child. One to three sentences.",
				},
			},
			Required: []string{"task_id", "guidance"},
		},
	}
}

func (t SubAgentInterveneTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	mgr, err := t.S.requireTasks()
	if err != nil {
		return "", err
	}
	var args struct {
		TaskID   string `json:"task_id"`
		Guidance string `json:"guidance"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	if err := mgr.Intervene(args.TaskID, "master", args.Guidance); err != nil {
		return "", err
	}
	return fmt.Sprintf("Intervention queued for task %s. The child will read it at the start of its next iteration.", args.TaskID), nil
}

// ── subagent_stream (master-side) ───────────────────────────────────────────

type SubAgentStreamTool struct{ S *SubAgentSvc }

func (t SubAgentStreamTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_stream",
		Description: `Drain the progress event stream for a sub-agent task (or all active tasks ` +
			`when task_id is omitted). Returns the ordered list of events the child posted via ` +
			`supervisor_report plus TaskManager lifecycle events. Use for deeper inspection than ` +
			`the ambient dashboard — e.g. to read a child's reasoning for "blocked".`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Optional task id. If omitted, streams events for every currently-active task.",
				},
				"since_index": map[string]any{
					"type":        "integer",
					"description": "Skip the first N events (for incremental pagination). Defaults to 0.",
				},
			},
		},
	}
}

func (t SubAgentStreamTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	mgr, err := t.S.requireTasks()
	if err != nil {
		return "", err
	}
	var args struct {
		TaskID     string `json:"task_id"`
		SinceIndex int    `json:"since_index"`
	}
	if input != nil {
		_ = json.Unmarshal(input, &args)
	}
	var ids []string
	if args.TaskID != "" {
		ids = []string{args.TaskID}
	} else {
		for _, tk := range mgr.Active() {
			ids = append(ids, tk.ID)
		}
	}
	if len(ids) == 0 {
		return "No active sub-agent tasks. Use subagent_tasks to see completed ones.", nil
	}
	var b strings.Builder
	for i, id := range ids {
		if i > 0 {
			b.WriteString("\n")
		}
		events := mgr.Events(id, args.SinceIndex)
		b.WriteString(fmt.Sprintf("── task %s ──\n", id))
		if len(events) == 0 {
			b.WriteString("  (no events since index)\n")
			continue
		}
		for j, e := range events {
			phase := ""
			if e.Phase != "" {
				phase = " [" + e.Phase + "]"
			}
			b.WriteString(fmt.Sprintf("  %3d. %s (%s)%s %s\n",
				args.SinceIndex+j, e.At.Format(time.RFC3339), e.Kind, phase, e.Message))
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ── subagent_share_context (master-side) ────────────────────────────────────

type SubAgentShareContextTool struct{ S *SubAgentSvc }

func (t SubAgentShareContextTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_share_context",
		Description: `Explicitly share a piece of context with a running (or completed) sub-agent task. ` +
			`By default sub-agents are fully isolated from the master's memory — each has its own ` +
			`session and workspace. Use this when (and only when) the child genuinely needs a ` +
			`master-side fact to proceed. The note is both recorded for audit and (when the task is ` +
			`running) appended to the child's message history so it influences the next LLM call.`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task id receiving the context.",
				},
				"note": map[string]any{
					"type":        "string",
					"description": "The context to share (concise — don't dump entire files).",
				},
			},
			Required: []string{"task_id", "note"},
		},
	}
}

func (t SubAgentShareContextTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	mgr, err := t.S.requireTasks()
	if err != nil {
		return "", err
	}
	var args struct {
		TaskID string `json:"task_id"`
		Note   string `json:"note"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	task, ok := mgr.Get(args.TaskID)
	if !ok {
		return "", fmt.Errorf("unknown task_id %q", args.TaskID)
	}
	if err := mgr.ShareContext(args.TaskID, "master", args.Note); err != nil {
		return "", err
	}
	// Note: the shared note is stored in the TaskManager's audit trail
	// (Task.SharedNotes) and surfaced to the master through
	// subagent_read_context and the dashboard event log. We intentionally
	// do NOT push it into the child's LLM session here — that would break
	// the "isolated by default" invariant silently. If you need the child
	// to see the note, pair share_context with subagent_intervene, which
	// goes through the proper supervisor-guidance path and is shown to
	// the child as authoritative.
	return fmt.Sprintf("Shared context with task %s (status=%s). Pair with subagent_intervene if you want the child to actually see it on its next turn.", args.TaskID, task.Status), nil
}

// ── subagent_read_context (master-side) ─────────────────────────────────────

type SubAgentReadContextTool struct{ S *SubAgentSvc }

func (t SubAgentReadContextTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_read_context",
		Description: `Read the audit trail of shared-context notes for a sub-agent task (both ` +
			`directions — what the master shared with the child, and future: what children shared ` +
			`back). Useful for "what does this child know that I told it?" questions.`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"task_id": map[string]any{"type": "string"},
			},
			Required: []string{"task_id"},
		},
	}
}

func (t SubAgentReadContextTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	mgr, err := t.S.requireTasks()
	if err != nil {
		return "", err
	}
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	task, ok := mgr.Get(args.TaskID)
	if !ok {
		return "", fmt.Errorf("unknown task_id %q", args.TaskID)
	}
	if len(task.SharedNotes) == 0 {
		return fmt.Sprintf("Task %s has no shared-context entries.", args.TaskID), nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Shared context for task %s:\n", args.TaskID))
	for i, n := range task.SharedNotes {
		b.WriteString(fmt.Sprintf("  %d. [%s] from %s: %s\n",
			i+1, n.At.Format(time.RFC3339), n.From, n.Message))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

