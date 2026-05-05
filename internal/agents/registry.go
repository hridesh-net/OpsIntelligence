// Package agents defines specialized domain agents and the orchestrator that
// routes incoming requests to the right specialist.
//
// Architecture:
//
//	Orchestrator (master runner)
//	  ├─ DevOpsAgent   — PR reviews, CI/CD, GitHub/GitLab/Jenkins/Sonar
//	  ├─ SecurityAgent — CVE, audit, compliance, guardrail checks
//	  └─ RepoIntelAgent — repo indexing, semantic search, code analysis
//
// Each agent has a defined tool profile, system-prompt focus, and keyword
// set used by the orchestrator for intent classification. They run via the
// existing subagents.TaskManager so the master can supervise and intervene.
package agents

import (
	"strings"
)

// AgentDef describes a specialized domain agent.
type AgentDef struct {
	// Name is the canonical agent identifier used in TaskManager task names.
	Name string

	// Description is shown to the master runner so it understands when to delegate.
	Description string

	// SystemPromptFocus is appended to the base system prompt when this agent runs.
	SystemPromptFocus string

	// Keywords are matched against the user request (case-insensitive) to decide
	// whether to delegate. The orchestrator picks the agent with most keyword hits.
	Keywords []string

	// AllowedTools lists the tool slugs this agent may use. Empty means all tools.
	AllowedTools []string

	// BlockedTools lists tool slugs explicitly denied to this agent.
	// Useful for preventing recursive subagent spawning in specialists.
	BlockedTools []string

	// ContextLoader is called by spawnSpecialist at runner-build time to inject
	// dynamic context (repo memory, code scan results, user methodology) into the
	// specialist's system prompt. The returned string is prepended to
	// SystemPromptFocus. Nil disables dynamic context injection.
	ContextLoader func() string
}

// MatchScore returns the number of keyword hits against the given query.
func (a AgentDef) MatchScore(query string) int {
	q := strings.ToLower(query)
	score := 0
	for _, kw := range a.Keywords {
		if strings.Contains(q, strings.ToLower(kw)) {
			score++
		}
	}
	return score
}

// Registry holds all registered specialized agents.
type Registry struct {
	agents []AgentDef
}

// RegistryOpts carries optional context loaders for each built-in agent.
// All fields are optional; zero values disable dynamic context injection for
// the corresponding agent. Pass to NewRegistry to wire live context.
type RegistryOpts struct {
	// DevOpsWorkflowPath is the absolute path to a workflow policy markdown
	// file injected into the devops agent's system prompt (workflow.md).
	DevOpsWorkflowPath string

	// SecurityPolicyFn returns the concatenated active security policies
	// (POLICIES.md + teams/*.md). Called lazily at spawn time.
	SecurityPolicyFn func() string

	// RepoSummaryFn returns a compact repo intelligence summary in markdown.
	// Shared between the repointel and pr_review agents.
	RepoSummaryFn func() string

	// AgentsConfigDir is the root directory for per-agent flow files.
	// Each agent's pipeline is loaded from <AgentsConfigDir>/<name>/flow.yaml
	// (or flow.md) at spawn time and injected into the system prompt.
	AgentsConfigDir string

	// FlowEvalCtx carries runtime integration flags for stage condition evaluation
	// (e.g. whether GitHub/Jira/SonarQube are configured).
	FlowEvalCtx FlowEvalContext
}

// NewRegistry returns a registry pre-loaded with the built-in domain agents
// (devops, security, repointel). The pr_review specialist is NOT auto-registered
// here because it carries its own PRReviewOpts; call Register(NewPRReviewAgent(opts))
// on the returned registry before passing it to NewOrchestratorWithRegistry.
//
// Pass RegistryOpts to enable dynamic context injection per agent. Calling
// NewRegistry() with no args is safe and leaves all context loaders nil.
func NewRegistry(opts ...RegistryOpts) *Registry {
	var o RegistryOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	r := &Registry{}
	r.agents = append(r.agents,
		newDevOpsAgent(o),
		newSecurityAgent(o),
		newRepoIntelAgent(o),
	)
	return r
}

// Register adds a custom agent definition to the registry.
func (r *Registry) Register(a AgentDef) {
	r.agents = append(r.agents, a)
}

// Unregister removes the agent with the given name from the registry.
// Returns true if an agent was removed, false if no agent had that name.
func (r *Registry) Unregister(name string) bool {
	for i, a := range r.agents {
		if a.Name == name {
			r.agents = append(r.agents[:i], r.agents[i+1:]...)
			return true
		}
	}
	return false
}

// All returns a copy of all registered agent definitions.
func (r *Registry) All() []AgentDef {
	out := make([]AgentDef, len(r.agents))
	copy(out, r.agents)
	return out
}

// Best returns the agent definition that best matches the query, and whether
// it meets the minimum confidence threshold (at least 1 keyword hit).
func (r *Registry) Best(query string) (AgentDef, bool) {
	best := AgentDef{}
	bestScore := 0
	for _, a := range r.agents {
		if s := a.MatchScore(query); s > bestScore {
			bestScore = s
			best = a
		}
	}
	return best, bestScore > 0
}
