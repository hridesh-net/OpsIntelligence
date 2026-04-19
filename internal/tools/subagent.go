package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/graph"
	"github.com/opsintelligence/opsintelligence/internal/memory"
	"github.com/opsintelligence/opsintelligence/internal/provider"
	"github.com/opsintelligence/opsintelligence/internal/security"
	"github.com/opsintelligence/opsintelligence/internal/subagents"
	"github.com/opsintelligence/opsintelligence/internal/system"
)

// SubAgentSvc holds shared dependencies for sub-agent tools (delegation to specialist runners).
type SubAgentSvc struct {
	Store *subagents.Store
	Tasks *subagents.TaskManager // shared async task tracker (may be nil before init)

	Provider       provider.Provider
	ParentRegistry *agent.ToolRegistry
	ToolGraph      *graph.ToolGraph
	Mem            *memory.Manager
	Log            *zap.Logger

	Model                 string
	ActiveSkillsContext   string
	ProviderName          string
	GatewayPublicBaseURL  string
	ExtensionPromptAppend string
	DefaultToolsProfile   string // security profile for new sub-agents when not specified

	Guardrail *security.Guardrail
	AuditLog  *security.AuditLog
	Hardware  *system.HardwareReport

	// RunTracePath is the NDJSON path for child runs: agent.run_trace_subagent_file
	// when set, otherwise the same path as the master agent.run_trace_file.
	RunTracePath string
	// RunTraceMode is the resolved agent.run_trace_mode (copied for run_trace.task_start).
	RunTraceMode string
	// EnabledSkillNames is copied from the parent agent for run_trace.task_start.
	EnabledSkillNames []string
}

// EnsureTaskManager wires a TaskManager that reuses the same sync executor as
// the blocking subagent_run tool. Safe to call multiple times; subsequent
// calls are no-ops once a manager is present.
func (s *SubAgentSvc) EnsureTaskManager(maxConcurrent, retainLimit int, defaultTimeout time.Duration) *subagents.TaskManager {
	if s.Tasks != nil {
		return s.Tasks
	}
	exec := func(ctx context.Context, taskID, subAgentID, task string) (string, int, error) {
		return s.runSyncWithTask(ctx, taskID, subAgentID, task, 0)
	}
	s.Tasks = subagents.NewTaskManager(exec)
	if maxConcurrent > 0 {
		s.Tasks.MaxConcurrent = maxConcurrent
	}
	if retainLimit > 0 {
		s.Tasks.RetainLimit = retainLimit
	}
	if defaultTimeout > 0 {
		s.Tasks.DefaultTimeout = defaultTimeout
	}
	return s.Tasks
}

// runSync is the shared synchronous run used by the blocking subagent_run
// tool. It does not have an async task_id (there's no TaskManager tracking
// it), so supervisor features like progress reporting / intervention are
// unavailable for this path. Callers that want supervision should use
// subagent_run_async (goes through runSyncWithTask with a tracked task_id).
func (s *SubAgentSvc) runSync(ctx context.Context, subAgentID, task string, maxIterations int) (string, int, error) {
	return s.runSyncWithTask(ctx, "", subAgentID, task, maxIterations)
}

// runSyncWithTask is the single shared run path. When taskID != "" the
// child runner is wired with two supervisor hooks:
//
//  1. Its system prompt is augmented on every iteration with any
//     interventions the master queued via TaskManager.Intervene — they
//     are drained atomically and appear as SYSTEM: authoritative guidance.
//  2. It is allowed to call the supervisor_report tool (injected by main
//     when taskID != "") to post progress events back to the manager.
//
// The taskID/subAgentID distinction: taskID identifies this invocation;
// subAgentID identifies which named specialist to run.
func (s *SubAgentSvc) runSyncWithTask(ctx context.Context, taskID, subAgentID, task string, maxIterations int) (string, int, error) {
	sa, err := s.Store.Get(subAgentID)
	if err != nil {
		return "", 0, err
	}
	if sa == nil {
		return "", 0, fmt.Errorf("unknown sub-agent id %q (use subagent_list)", subAgentID)
	}
	maxIt := maxIterations
	if maxIt <= 0 {
		maxIt = 32
	}
	if maxIt > 64 {
		maxIt = 64
	}
	ws := s.Store.WorkspaceDir(sa.ID)
	run := s.buildChildRunner(maxIt, sa.ToolsProfile, ws, taskID)

	sid := "subagent:" + sa.ID + ":" + uuid.New().String()
	runner := run.WithSession(sid)

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 20*time.Minute)
		defer cancel()
	}

	res, err := runner.Run(ctx, memory.Message{
		ID:        uuid.New().String(),
		SessionID: sid,
		Role:      memory.RoleUser,
		Content:   strings.TrimSpace(task),
		CreatedAt: time.Now(),
	})
	if err != nil {
		return "", 0, err
	}
	return res.Response, res.Iterations, nil
}

// subAgentOmit lists tools that must not be available inside a sub-agent.
// These are either recursion hazards (sub-agents spawning grand-children)
// or master-only supervisor controls (a child should not be able to
// intervene in its own task or another child's).
var subAgentOmit = []string{
	// sub-agent management is master-only
	"subagent_create",
	"subagent_list",
	"subagent_run",
	"subagent_remove",
	"subagent_run_async",
	"subagent_run_parallel",
	"subagent_status",
	"subagent_wait",
	"subagent_tasks",
	"subagent_cancel",
	"subagent_intervene",
	"subagent_stream",
	"subagent_share_context",
	"subagent_read_context",
}

// buildChildRunner constructs a runner for one sub-agent invocation:
// same tool stack as the parent minus recursive sub-agent tools + master-
// only supervisor controls, isolated workspace, no planning/reflection.
//
// When taskID != "" the runner is also wired with two supervisor hooks:
//
//   - A supervisor_report tool, scoped to this exact task_id, so the
//     child can post progress events back to the TaskManager.
//   - A per-iteration system-prompt augmentor that drains any pending
//     interventions from the TaskManager and surfaces them as an
//     authoritative SUPERVISOR block so the child obeys them on its
//     next turn.
//
// When taskID == "" (legacy synchronous subagent_run) neither hook is
// wired — supervision is only available on the async/tracked path.
func (s *SubAgentSvc) buildChildRunner(maxIterations int, toolsProfile, workspace, taskID string) *agent.Runner {
	childReg := s.ParentRegistry.CloneWithout(subAgentOmit...)

	// Inject supervisor_report scoped to this taskID when tracked.
	if taskID != "" && s.Tasks != nil {
		childReg.Register(SupervisorReportTool{Tasks: s.Tasks, TaskID: taskID})
	}

	catalog := NewCatalog(childReg, s.ToolGraph)
	prof := toolsProfile
	if prof != "coding" {
		prof = "full"
	}
	run := agent.NewRunner(agent.Config{
		MaxIterations:         maxIterations,
		Model:                 s.Model,
		ActiveSkillsContext:   s.ActiveSkillsContext,
		EnabledSkillNames:     s.EnabledSkillNames,
		RunTracePath:          s.RunTracePath,
		RunnerRole:            "subagent",
		RunTraceMode:          s.RunTraceMode,
		ProviderName:          s.ProviderName,
		ToolsProfile:          prof,
		GatewayPublicBaseURL:  s.GatewayPublicBaseURL,
		ExtensionPromptAppend: s.ExtensionPromptAppend,
		EnablePlanning:        false,
		EnableReflection:      false,
		StateDir:              s.Store.StateDir(),
	}, s.Provider, childReg, s.Mem, s.Log, workspace)

	run = run.WithCatalog(catalog).WithHardware(s.Hardware).WithSecurity(s.Guardrail, s.AuditLog)

	// Per-turn intervention augmentor (tracked tasks only).
	if taskID != "" && s.Tasks != nil {
		tasks := s.Tasks
		run = run.WithSystemPromptAugmentor(func(ctx context.Context) string {
			drained := tasks.DrainInterventions(taskID)
			if len(drained) == 0 {
				return ""
			}
			var b strings.Builder
			b.WriteString("## SUPERVISOR GUIDANCE\n")
			b.WriteString("Your master (parent agent) has pushed the following authoritative guidance while you were running. Treat it as top-priority and adjust your plan immediately:\n\n")
			for _, iv := range drained {
				b.WriteString(fmt.Sprintf("- [%s] %s: %s\n",
					iv.At.Format(time.RFC3339), iv.From, iv.Message))
			}
			b.WriteString("\nAcknowledge the guidance by calling supervisor_report(kind=\"progress\", message=\"acknowledged intervention: …\") before resuming tool use.")
			return b.String()
		})
	}

	return run
}

// ── subagent_create ──────────────────────────────────────────────────────────

type SubAgentCreateTool struct{ S *SubAgentSvc }

func (t SubAgentCreateTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_create",
		Description: `Create a named specialist sub-agent with its own workspace under subagents/<id>/ (SOUL.md + instructions). ` +
			`Use for delegating complex parallel concerns (research, IT monitor, writing). The main agent keeps orchestration; ` +
			`invoke work with subagent_run.`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Short display name, e.g. NetworkMonitor",
				},
				"instructions": map[string]any{
					"type":        "string",
					"description": "Role, constraints, and expertise for this sub-agent (written to SOUL.md)",
				},
				"tools_profile": map[string]any{
					"type":        "string",
					"description": "Optional: 'full' (default) or 'coding' (no web/browser/cron/message tools)",
					"enum":        []string{"full", "coding"},
				},
			},
			Required: []string{"name", "instructions"},
		},
	}
}

func (t SubAgentCreateTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var args struct {
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
		ToolsProfile string `json:"tools_profile"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	prof := args.ToolsProfile
	if prof == "" {
		prof = t.S.DefaultToolsProfile
		if prof == "" {
			prof = "full"
		}
	}
	a, err := t.S.Store.Create(args.Name, args.Instructions, prof)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Created sub-agent %q id=%s tools_profile=%s workspace=%s\nUse subagent_run with id=%s to assign tasks.",
		a.Name, a.ID, a.ToolsProfile, t.S.Store.WorkspaceDir(a.ID), a.ID), nil
}

// ── subagent_list ───────────────────────────────────────────────────────────

type SubAgentListTool struct{ S *SubAgentSvc }

func (t SubAgentListTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name:        "subagent_list",
		Description: "List registered sub-agents (id, name, tools_profile, created_at).",
		InputSchema: provider.ToolParameter{
			Type:       "object",
			Properties: map[string]any{},
		},
	}
}

func (t SubAgentListTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	agents, err := t.S.Store.Load()
	if err != nil {
		return "", err
	}
	if len(agents) == 0 {
		return "No sub-agents registered. Use subagent_create first.", nil
	}
	var b strings.Builder
	for _, a := range agents {
		b.WriteString(fmt.Sprintf("- id=%s name=%q profile=%s created=%s\n",
			a.ID, a.Name, a.ToolsProfile, a.CreatedAt.Format(time.RFC3339)))
	}
	return b.String(), nil
}

// ── subagent_run ─────────────────────────────────────────────────────────────

type SubAgentRunTool struct{ S *SubAgentSvc }

func (t SubAgentRunTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_run",
		Description: `Run a one-shot task with a sub-agent: isolated session, same tools as main (except sub-agent recursion). ` +
			`Returns the sub-agent final text response. Use for heavy research, scripted checks, or drafts.`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Sub-agent id from subagent_list / subagent_create",
				},
				"task": map[string]any{
					"type":        "string",
					"description": "Clear task prompt for this invocation",
				},
				"max_iterations": map[string]any{
					"type":        "integer",
					"description": "Optional cap (default 32, max 64)",
				},
			},
			Required: []string{"id", "task"},
		},
	}
}

func (t SubAgentRunTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var args struct {
		ID            string `json:"id"`
		Task          string `json:"task"`
		MaxIterations int    `json:"max_iterations"`
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
	resp, iters, err := t.S.runSync(ctx, args.ID, args.Task, args.MaxIterations)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("[sub-agent %s %q]\n%s\n---\n(iterations=%d)",
		sa.ID, sa.Name, resp, iters), nil
}

// ── subagent_remove ──────────────────────────────────────────────────────────

type SubAgentRemoveTool struct{ S *SubAgentSvc }

func (t SubAgentRemoveTool) Definition() provider.ToolDef {
	return provider.ToolDef{
		Name: "subagent_remove",
		Description: `Remove a sub-agent from the registry. By default deletes its workspace files (SOUL.md, etc.). ` +
			`Use when a specialist is no longer needed.`,
		InputSchema: provider.ToolParameter{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Sub-agent id",
				},
				"keep_files": map[string]any{
					"type":        "boolean",
					"description": "If true, unregister only but leave workspace on disk (default false = delete files)",
				},
			},
			Required: []string{"id"},
		},
	}
}

func (t SubAgentRemoveTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var args struct {
		ID        string `json:"id"`
		KeepFiles bool   `json:"keep_files"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", err
	}
	deleteFiles := !args.KeepFiles
	if err := t.S.Store.Remove(args.ID, deleteFiles); err != nil {
		return "", err
	}
	if deleteFiles {
		return fmt.Sprintf("Removed sub-agent %q and deleted its workspace.", args.ID), nil
	}
	return fmt.Sprintf("Removed sub-agent %q from registry (files kept on disk).", args.ID), nil
}
