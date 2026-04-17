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

// This file adds async / parallel sub-agent orchestration tools on top of
// the synchronous subagent_run. The master agent uses these to dispatch
// multiple specialist sub-agents concurrently (e.g. "review PR 42 AND
// triage the failing pipeline AND chase down the Sonar regression"), then
// poll or wait on their results.
//
// All tools below are master-only: they are appended to subAgentOmit in
// subagent.go so child runners cannot spawn grand-children.

func (s *SubAgentSvc) requireTasks() (*subagents.TaskManager, error) {
	if s.Tasks != nil {
		return s.Tasks, nil
	}
	return nil, errors.New("subagent task manager not initialised; call SubAgentSvc.EnsureTaskManager on startup")
}

// ── subagent_run_async ───────────────────────────────────────────────────────

type SubAgentRunAsyncTool struct{ S *SubAgentSvc }

func (t SubAgentRunAsyncTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_run_async",
		Description: `Dispatch a task to a sub-agent in the background. Returns a task_id ` +
			`immediately without waiting for the sub-agent to finish. Use when you need to ` +
			`orchestrate multiple independent sub-agents concurrently (e.g. review 3 PRs at once, ` +
			`or review PR + check Sonar + check pipeline in parallel). Poll with subagent_status, ` +
			`wait on a batch with subagent_wait, or use subagent_run_parallel as a convenience wrapper.`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Sub-agent id (from subagent_create / subagent_list)",
				},
				"task": map[string]any{
					"type":        "string",
					"description": "Task prompt for this invocation",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Optional per-task timeout (default 1800s / 30min, max 3600s)",
				},
			},
			Required: []string{"id", "task"},
		},
	}
}

func (t SubAgentRunAsyncTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	mgr, err := t.S.requireTasks()
	if err != nil {
		return "", err
	}
	var args struct {
		ID             string `json:"id"`
		Task           string `json:"task"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	sa, err := t.S.Store.Get(args.ID)
	if err != nil {
		return "", err
	}
	if sa == nil {
		return "", fmt.Errorf("unknown sub-agent id %q (use subagent_list)", args.ID)
	}
	var timeout time.Duration
	if args.TimeoutSeconds > 0 {
		if args.TimeoutSeconds > 3600 {
			args.TimeoutSeconds = 3600
		}
		timeout = time.Duration(args.TimeoutSeconds) * time.Second
	}
	taskID, err := mgr.RunAsync(sa.ID, sa.Name, args.Task, timeout)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("task_id=%s status=pending sub_agent=%s (%q)\nPoll with subagent_status or wait with subagent_wait.",
		taskID, sa.ID, sa.Name), nil
}

// ── subagent_status ──────────────────────────────────────────────────────────

type SubAgentStatusTool struct{ S *SubAgentSvc }

func (t SubAgentStatusTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_status",
		Description: `Return the status (pending / running / completed / failed / cancelled) ` +
			`and, if terminal, the result or error of an async sub-agent task. Tasks are ` +
			`identified by the task_id returned from subagent_run_async.`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task id from subagent_run_async",
				},
			},
			Required: []string{"task_id"},
		},
	}
}

func (t SubAgentStatusTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
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
	return formatTask(task), nil
}

// ── subagent_wait ────────────────────────────────────────────────────────────

type SubAgentWaitTool struct{ S *SubAgentSvc }

func (t SubAgentWaitTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_wait",
		Description: `Block until every listed task_id reaches a terminal status (completed, ` +
			`failed, or cancelled), or the timeout elapses. Returns a summary for each task. ` +
			`Useful after fanning out work with subagent_run_async so the master can collect ` +
			`all results before synthesising a reply.`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"task_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Task ids to wait on",
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Optional wait timeout (default 300s, max 3600s)",
				},
			},
			Required: []string{"task_ids"},
		},
	}
}

func (t SubAgentWaitTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	mgr, err := t.S.requireTasks()
	if err != nil {
		return "", err
	}
	var args struct {
		TaskIDs        []string `json:"task_ids"`
		TimeoutSeconds int      `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	if len(args.TaskIDs) == 0 {
		return "", errors.New("task_ids is required")
	}
	if args.TimeoutSeconds > 3600 {
		args.TimeoutSeconds = 3600
	}
	timeout := time.Duration(args.TimeoutSeconds) * time.Second
	results, waitErr := mgr.Wait(ctx, args.TaskIDs, timeout)

	var b strings.Builder
	if waitErr != nil {
		b.WriteString("(wait ended: ")
		b.WriteString(waitErr.Error())
		b.WriteString(")\n\n")
	}
	for _, tk := range results {
		b.WriteString(formatTask(tk))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ── subagent_tasks ───────────────────────────────────────────────────────────

type SubAgentTasksTool struct{ S *SubAgentSvc }

func (t SubAgentTasksTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "subagent_tasks",
		Description: `List recent async sub-agent tasks (newest first) with status and elapsed time. Use to see what's running right now or inspect a recent run.`,
		InputSchema: provider.ToolParameter{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func (t SubAgentTasksTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	mgr, err := t.S.requireTasks()
	if err != nil {
		return "", err
	}
	tasks := mgr.List()
	if len(tasks) == 0 {
		return "No async sub-agent tasks yet. Dispatch one with subagent_run_async.", nil
	}
	var b strings.Builder
	for _, tk := range tasks {
		fmt.Fprintf(&b, "- task=%s status=%s sub_agent=%s (%q) elapsed=%s\n",
			tk.ID, tk.Status, tk.SubAgentID, tk.SubAgentNm, tk.Elapsed().Round(time.Millisecond))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ── subagent_cancel ──────────────────────────────────────────────────────────

type SubAgentCancelTool struct{ S *SubAgentSvc }

func (t SubAgentCancelTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "subagent_cancel",
		Description: `Cancel a pending or running async sub-agent task. No-op on terminal tasks. Use when a sub-agent is hung or when you realise the work is no longer needed.`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task id from subagent_run_async",
				},
			},
			Required: []string{"task_id"},
		},
	}
}

func (t SubAgentCancelTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
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
	if !mgr.Cancel(args.TaskID) {
		return "", fmt.Errorf("unknown task_id %q", args.TaskID)
	}
	return fmt.Sprintf("Cancellation requested for task %s.", args.TaskID), nil
}

// ── subagent_run_parallel ────────────────────────────────────────────────────

type SubAgentRunParallelTool struct{ S *SubAgentSvc }

func (t SubAgentRunParallelTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_run_parallel",
		Description: `Fan out multiple sub-agent tasks concurrently and wait for all of them to complete. ` +
			`Equivalent to subagent_run_async × N followed by subagent_wait, but one call. ` +
			`Use when the master already knows the full batch (e.g. "review all 5 open PRs on this repo").`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"tasks": map[string]any{
					"type":        "array",
					"description": "Array of {id, task, timeout_seconds?} objects",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"id":              map[string]any{"type": "string"},
							"task":            map[string]any{"type": "string"},
							"timeout_seconds": map[string]any{"type": "integer"},
						},
						"required": []string{"id", "task"},
					},
				},
				"wait_seconds": map[string]any{
					"type":        "integer",
					"description": "Optional wait-all timeout (default 600s, max 3600s)",
				},
			},
			Required: []string{"tasks"},
		},
	}
}

func (t SubAgentRunParallelTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	mgr, err := t.S.requireTasks()
	if err != nil {
		return "", err
	}
	var args struct {
		Tasks []struct {
			ID             string `json:"id"`
			Task           string `json:"task"`
			TimeoutSeconds int    `json:"timeout_seconds"`
		} `json:"tasks"`
		WaitSeconds int `json:"wait_seconds"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	if len(args.Tasks) == 0 {
		return "", errors.New("tasks is required")
	}
	ids := make([]string, 0, len(args.Tasks))
	for i, spec := range args.Tasks {
		sa, err := t.S.Store.Get(spec.ID)
		if err != nil {
			return "", fmt.Errorf("tasks[%d]: %w", i, err)
		}
		if sa == nil {
			return "", fmt.Errorf("tasks[%d]: unknown sub-agent id %q", i, spec.ID)
		}
		var to time.Duration
		if spec.TimeoutSeconds > 0 {
			if spec.TimeoutSeconds > 3600 {
				spec.TimeoutSeconds = 3600
			}
			to = time.Duration(spec.TimeoutSeconds) * time.Second
		}
		tid, err := mgr.RunAsync(sa.ID, sa.Name, spec.Task, to)
		if err != nil {
			return "", fmt.Errorf("tasks[%d]: %w", i, err)
		}
		ids = append(ids, tid)
	}

	waitSec := args.WaitSeconds
	if waitSec <= 0 {
		waitSec = 600
	}
	if waitSec > 3600 {
		waitSec = 3600
	}
	results, waitErr := mgr.Wait(ctx, ids, time.Duration(waitSec)*time.Second)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Dispatched %d task(s): %s\n\n", len(ids), strings.Join(ids, ", ")))
	if waitErr != nil {
		b.WriteString("(wait ended: " + waitErr.Error() + ")\n\n")
	}
	for _, tk := range results {
		b.WriteString(formatTask(tk))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ── shared formatter ─────────────────────────────────────────────────────────

func formatTask(t subagents.Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "task=%s status=%s sub_agent=%s (%q) elapsed=%s iterations=%d",
		t.ID, t.Status, t.SubAgentID, t.SubAgentNm, t.Elapsed().Round(time.Millisecond), t.Iterations)
	if t.Error != "" {
		fmt.Fprintf(&b, "\nerror: %s", t.Error)
	}
	if strings.TrimSpace(t.Result) != "" {
		fmt.Fprintf(&b, "\nresult:\n%s", strings.TrimRight(t.Result, "\n"))
	}
	return b.String()
}
