package agents

import (
	"context"
	"fmt"
	"strconv"
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
	// reviewFn, when set, posts a PR review deterministically. Used so a routed
	// pr_review request with a concrete PR URL always posts, rather than relying
	// on the model to call devops.github.review_pr (weaker models narrate a
	// review without ever posting it).
	reviewFn func(ctx context.Context, owner, repo string, number int) (string, error)
}

// SetReviewFn installs the deterministic PR-review poster (the same ReviewFn the
// devops tools use). When set, a routed pr_review request containing a GitHub PR
// URL is posted directly instead of going through the specialist's LLM loop.
func (o *Orchestrator) SetReviewFn(fn func(ctx context.Context, owner, repo string, number int) (string, error)) {
	o.reviewFn = fn
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

	// Deterministic PR review: when routing to pr_review with a concrete PR URL,
	// post the review directly via the configured ReviewFn. This guarantees the
	// review is posted instead of depending on the model to call review_pr —
	// weaker models (e.g. gemini-flash) tend to narrate a review without posting.
	// Skipped when the user explicitly asked not to post (dry-run / summarize).
	if def.Name == "pr_review" && o.reviewFn != nil && !mentionsNoPost(query) {
		if owner, repo, number, ok := parseGitHubPRURL(query); ok {
			fn := o.reviewFn
			o.log.Info("pr_review: posting deterministically via ReviewFn",
				zap.String("owner", owner), zap.String("repo", repo), zap.Int("number", number))
			taskID, err := o.tasks.SubmitDirect(ctx, def.Name, query, func(taskCtx context.Context) (string, error) {
				return fn(taskCtx, owner, repo, number)
			})
			if err != nil {
				return RouteResult{}, fmt.Errorf("orchestrator: submit pr_review post: %w", err)
			}
			return RouteResult{Delegated: true, AgentName: def.Name, TaskID: taskID}, nil
		}
	}

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
		// Free working + episodic memory for this specialist session.
		runner.Cleanup(context.Background())
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

// mentionsNoPost reports whether the query explicitly asks for a read-only /
// preview review (so the deterministic poster should stand down).
func mentionsNoPost(query string) bool {
	q := strings.ToLower(query)
	for _, phrase := range []string{"dry run", "dry-run", "don't post", "do not post", "without posting", "just summarize", "just summarise", "read-only", "read only", "no comment"} {
		if strings.Contains(q, phrase) {
			return true
		}
	}
	return false
}

// parseGitHubPRURL extracts owner/repo/number from a GitHub PR URL found
// anywhere in the text (e.g. https://github.com/owner/repo/pull/42[#...]).
func parseGitHubPRURL(text string) (owner, repo string, number int, ok bool) {
	for _, tok := range strings.Fields(text) {
		s := strings.TrimSpace(tok)
		for _, p := range []string{"https://", "http://"} {
			s = strings.TrimPrefix(s, p)
		}
		if !strings.HasPrefix(s, "github.com/") {
			continue
		}
		s = strings.TrimPrefix(s, "github.com/")
		if i := strings.IndexAny(s, "#?"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimRight(s, "/")
		parts := strings.Split(s, "/")
		if len(parts) < 4 || parts[2] != "pull" {
			continue
		}
		n, err := strconv.Atoi(parts[3])
		if err != nil || n < 1 {
			continue
		}
		return parts[0], parts[1], n, true
	}
	return "", "", 0, false
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
