package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/memory"
	"github.com/opsintelligence/opsintelligence/internal/subagents"
)

// Orchestrator routes incoming requests to the best-matching specialist agent.
// It wraps the existing TaskManager so specialists run as supervised async tasks
// that the master runner can inspect via the standard subagent_status / subagent_stream tools.
type Orchestrator struct {
	registry *Registry
	tasks    *subagents.TaskManager
	spawnFn  SpawnFn
	log      *zap.Logger
}

// SpawnFn creates a specialized Runner for the given AgentDef. The runner
// should have a filtered tool set and the agent's system prompt focus applied.
type SpawnFn func(ctx context.Context, def AgentDef) (*agent.Runner, error)

// NewOrchestrator creates an Orchestrator backed by the built-in agent registry.
func NewOrchestrator(tasks *subagents.TaskManager, spawn SpawnFn, log *zap.Logger) *Orchestrator {
	return &Orchestrator{
		registry: NewRegistry(),
		tasks:    tasks,
		spawnFn:  spawn,
		log:      log,
	}
}

// NewOrchestratorWithRegistry creates an Orchestrator using a custom agent registry.
func NewOrchestratorWithRegistry(reg *Registry, tasks *subagents.TaskManager, spawn SpawnFn, log *zap.Logger) *Orchestrator {
	return &Orchestrator{
		registry: reg,
		tasks:    tasks,
		spawnFn:  spawn,
		log:      log,
	}
}

// RouteResult describes the outcome of a routing decision.
type RouteResult struct {
	// Delegated is true when the request was handed off to a specialist.
	Delegated bool
	// AgentName is the name of the specialist that was selected.
	AgentName string
	// TaskID is the TaskManager task ID for the specialist run.
	TaskID string
}

// Route examines query and delegates to a specialist when one matches.
// Returns Delegated=false when no specialist has sufficient keyword coverage,
// signalling the master runner to handle the request inline.
func (o *Orchestrator) Route(ctx context.Context, query string) (RouteResult, error) {
	def, ok := o.registry.Best(query)
	if !ok {
		return RouteResult{Delegated: false}, nil
	}

	o.log.Info("routing to specialist",
		zap.String("agent", def.Name),
		zap.String("query_preview", truncate(query, 120)),
	)

	runner, err := o.spawnFn(ctx, def)
	if err != nil {
		return RouteResult{}, fmt.Errorf("orchestrator: spawn %s: %w", def.Name, err)
	}

	msg := memory.Message{
		ID:        uuid.New().String(),
		SessionID: def.Name + ":" + uuid.New().String()[:8],
		Role:      memory.RoleUser,
		Content:   query,
		CreatedAt: time.Now(),
	}

	taskID, err := o.tasks.SubmitDirect(ctx, def.Name, query, func(taskCtx context.Context) (string, error) {
		result, runErr := runner.Run(taskCtx, msg)
		if runErr != nil {
			return "", runErr
		}
		return result.Response, nil
	})
	if err != nil {
		return RouteResult{}, fmt.Errorf("orchestrator: submit task for %s: %w", def.Name, err)
	}

	return RouteResult{
		Delegated: true,
		AgentName: def.Name,
		TaskID:    taskID,
	}, nil
}

// RoutingHint returns a human-readable hint for the master runner's system prompt
// describing which specialists are available and their domains.
func (o *Orchestrator) RoutingHint() string {
	defs := o.registry.All()
	if len(defs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Specialist Agents\n")
	sb.WriteString("These domain specialists are available via the subagent tools. ")
	sb.WriteString("Delegate domain-specific work to them for focused, high-quality results.\n\n")
	for _, d := range defs {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", d.Name, d.Description))
	}
	return sb.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
