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

// NewRegistry returns a registry pre-loaded with all built-in domain agents.
func NewRegistry() *Registry {
	r := &Registry{}
	r.agents = append(r.agents,
		devOpsAgent(),
		securityAgent(),
		repoIntelAgent(),
	)
	return r
}

// Register adds a custom agent definition to the registry.
func (r *Registry) Register(a AgentDef) {
	r.agents = append(r.agents, a)
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
