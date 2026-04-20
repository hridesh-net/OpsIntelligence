package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/provider"
	"github.com/opsintelligence/opsintelligence/internal/subagents"
)

// ── pr_review_tasks ───────────────────────────────────────────────────────────

// PRReviewTasksTool lists all tracked PR review tasks with status, elapsed time, and last event.
type PRReviewTasksTool struct{ H *PRReviewCmdHandler }

func (t PRReviewTasksTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "pr_review_tasks",
		Description: "List all /pr-review tasks (pending, running, completed, failed). Shows task IDs, status, elapsed time, and last progress event. Use pr_review_events for full event history on a specific task.",
		InputSchema: provider.ToolParameter{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func (t PRReviewTasksTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	tasks := t.H.Manager().List()
	if len(tasks) == 0 {
		return "No PR review tasks yet. Use /pr-review: <url> or the pr_review command to start one.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "PR Review Tasks (%d total, max_concurrent=%d)\n\n", len(tasks), t.H.mgr.MaxConcurrent)
	for _, tk := range tasks {
		elapsed := tk.Elapsed().Round(time.Millisecond)
		fmt.Fprintf(&b, "task=%-14s  status=%-10s  elapsed=%-10s  %s\n",
			tk.ID, tk.Status, elapsed, tk.SubAgentNm)
		if last := tk.LastEvent(); last.Message != "" {
			fmt.Fprintf(&b, "  └ last [%s/%s]: %s\n", last.Phase, last.Kind, last.Message)
		}
		if tk.Error != "" {
			fmt.Fprintf(&b, "  └ error: %s\n", tk.Error)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ── pr_review_cancel ──────────────────────────────────────────────────────────

// PRReviewCancelTool cancels a pending or running PR review task by task ID.
type PRReviewCancelTool struct{ H *PRReviewCmdHandler }

func (t PRReviewCancelTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "pr_review_cancel",
		Description: "Cancel a pending or running PR review task. Use pr_review_tasks to find the task_id. No-op on already-terminal tasks.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task ID from pr_review_tasks",
				},
			},
			Required: []string{"task_id"},
		},
	}
}

func (t PRReviewCancelTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	if args.TaskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	if !t.H.Manager().Cancel(args.TaskID) {
		return fmt.Sprintf("Unknown task_id %q. Use pr_review_tasks to list known tasks.", args.TaskID), nil
	}
	return fmt.Sprintf("Cancellation requested for PR review task %s.", args.TaskID), nil
}

// ── pr_review_events ──────────────────────────────────────────────────────────

// PRReviewEventsTool streams the full event log for a specific PR review task.
type PRReviewEventsTool struct{ H *PRReviewCmdHandler }

func (t PRReviewEventsTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "pr_review_events",
		Description: "Fetch the full progress event log for a specific PR review task. Useful for diagnosing slow or failed reviews. Use pr_review_tasks to find task IDs.",
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "Task ID from pr_review_tasks",
				},
				"since": map[string]any{
					"type":        "integer",
					"description": "Skip the first N events (for incremental polling). Default 0.",
				},
			},
			Required: []string{"task_id"},
		},
	}
}

func (t PRReviewEventsTool) Execute(_ context.Context, input json.RawMessage) (string, error) {
	var args struct {
		TaskID string `json:"task_id"`
		Since  int    `json:"since"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	if args.TaskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	task, ok := t.H.Manager().Get(args.TaskID)
	if !ok {
		return fmt.Sprintf("Unknown task_id %q.", args.TaskID), nil
	}
	events := t.H.Manager().Events(args.TaskID, args.Since)
	var b strings.Builder
	fmt.Fprintf(&b, "PR Review task=%s  %s  status=%s  elapsed=%s\n\n",
		task.ID, task.SubAgentNm, task.Status, task.Elapsed().Round(time.Millisecond))
	if len(events) == 0 {
		fmt.Fprintf(&b, "(no new events since index %d)", args.Since)
	}
	for i, ev := range events {
		phase := ev.Phase
		if phase == "" {
			phase = "-"
		}
		fmt.Fprintf(&b, "[%3d] %s  kind=%-10s  phase=%-12s  %s\n",
			args.Since+i, ev.At.Format("15:04:05"), ev.Kind, phase, ev.Message)
	}
	if task.Error != "" {
		fmt.Fprintf(&b, "\nerror: %s", task.Error)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// ensure subagents import is used
func init() { _ = subagents.KindProgress }
