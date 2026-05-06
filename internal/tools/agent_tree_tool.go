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

// AgentTreeTool renders the current agent hierarchy as readable text.
// The master agent can call this to understand what specialists are running,
// what they are doing, and when they finished — without needing to poll
// individual subagent_status calls.
type AgentTreeTool struct {
	Tasks *subagents.TaskManager
}

func (t AgentTreeTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "agent.tree",
		Description: "Show a live hierarchy tree of all active and recently completed specialist agents: " +
			"name, status, elapsed time, current tool being called, and last progress event. " +
			"Use this to understand what sub-agents are doing before deciding to intervene or wait.",
		InputSchema: provider.ToolParameter{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func (t AgentTreeTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	if t.Tasks == nil {
		return "Agent task manager not available.", nil
	}
	tasks := t.Tasks.List()
	if len(tasks) == 0 {
		return "No specialist agents have been spawned in this session.", nil
	}

	var sb strings.Builder
	active := 0
	for _, tk := range tasks {
		if tk.Status == subagents.StatusRunning || tk.Status == subagents.StatusPending {
			active++
		}
	}
	fmt.Fprintf(&sb, "Agent tree (%d active, %d total)\n\n", active, len(tasks))

	// Master row
	sb.WriteString("■ master  running\n")

	// Running / pending children first
	for _, tk := range tasks {
		if tk.Status != subagents.StatusRunning && tk.Status != subagents.StatusPending {
			continue
		}
		icon := "■"
		if tk.Status == subagents.StatusPending {
			icon = "◷"
		}
		elapsed := tk.Elapsed().Round(time.Second)
		sb.WriteString(fmt.Sprintf("  └─ %s %-12s %-10s %s\n",
			icon, tk.SubAgentNm, tk.Status, elapsed))
		last := tk.LastEvent()
		if last.Message != "" {
			sb.WriteString(fmt.Sprintf("       last [%s]: %s\n", last.Phase, truncateAgentStr(last.Message, 80)))
		}
	}

	// Completed / failed
	var done []subagents.Task
	for _, tk := range tasks {
		if tk.Status == subagents.StatusCompleted || tk.Status == subagents.StatusFailed || tk.Status == subagents.StatusCancelled {
			done = append(done, tk)
		}
	}
	if len(done) > 0 {
		sb.WriteString("\n── Completed ──────────────────────────────────\n")
		for _, tk := range done {
			icon := "✓"
			if tk.Status == subagents.StatusFailed {
				icon = "✗"
			} else if tk.Status == subagents.StatusCancelled {
				icon = "⊘"
			}
			elapsed := tk.Elapsed().Round(time.Second)
			errStr := ""
			if tk.Error != "" {
				errStr = "  error: " + truncateAgentStr(tk.Error, 60)
			}
			sb.WriteString(fmt.Sprintf("%s %-12s %-10s %s%s\n",
				icon, tk.SubAgentNm, tk.Status, elapsed, errStr))
		}
	}

	return strings.TrimRight(sb.String(), "\n"), nil
}

func truncateAgentStr(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n]) + "…"
}
