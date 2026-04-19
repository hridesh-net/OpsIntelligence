// Package graph implements the weighted directed graph for context-engineering.
// It provides BFS traversal with session inertia for tool selection,
// and wikilink-edge traversal for skill graph navigation.
package graph

import (
	"math"
	"sort"
	"strings"
	"sync"
)

// EdgeType classifies the relationship between two tool nodes.
type EdgeType int

const (
	EdgeCompanion    EdgeType = iota // Commonly used together (0.9)
	EdgePrerequisite                 // Almost always needed before (1.0)
	EdgeDomain                       // Same capability category (0.7)
	EdgeFallback                     // Alternative when primary fails (0.5)
)

var edgeBaseWeight = map[EdgeType]float64{
	EdgePrerequisite: 1.0,
	EdgeCompanion:    0.9,
	EdgeDomain:       0.7,
	EdgeFallback:     0.5,
}

// Intent groups a user message into a capability category.
type Intent int

const (
	IntentWebResearch Intent = iota
	IntentFileOps
	IntentCodeExec
	IntentBrowser
	IntentMemory
	IntentSchedule
	IntentCommunicate
	IntentSystem
	// DevOps-specific intents. OpsIntelligence is a DevOps-first agent, so
	// these carry more weight than generic capability buckets.
	IntentPRReview      // "review PR", "should we merge", "check diff"
	IntentSonar         // "sonar", "quality gate", "code smells"
	IntentCICD          // "pipeline failed", "ci broken", "regression on main"
	IntentIncident      // "incident", "outage", "on-call", "page fired"
	IntentDevOpsGeneric // any other devops.* signal (git, gh, runbook)
)

// intentKeywords maps each intent to trigger words.
var intentKeywords = map[Intent][]string{
	IntentWebResearch:   {"search", "find", "look up", "lookup", "research", "latest", "current", "online", "news", "today", "recent"},
	IntentFileOps:       {"create", "write", "edit", "modify", "read", "file", "save", "content", "update", "change", "open", "delete"},
	IntentCodeExec:      {"run", "execute", "test", "build", "compile", "install", "npm", "go run", "pip", "make", "start"},
	IntentBrowser:       {"screenshot", "navigate", "browser", "page", "click", "url", "website", "open browser", "visit"},
	IntentMemory:        {"remember", "history", "session", "past", "previous", "recall", "earlier", "last time"},
	IntentSchedule:      {"schedule", "recurring", "daily", "cron", "every", "repeat", "periodic", "automation", "job"},
	IntentCommunicate:   {"send", "message", "notify", "alert", "slack", "push"},
	IntentSystem:        {"process", "background", "daemon", "env", "environment", "variable", "pid", "kill", ".env", "subagent", "sub-agent", "delegate", "specialist", "spawn"},
	IntentPRReview:      {"pr ", "pull request", "review pr", "merge", "code review", "diff", "should we merge", "ship it", "lgtm", "approve", "request changes", "reviewers"},
	IntentSonar:         {"sonar", "sonarqube", "quality gate", "code smell", "code smells", "coverage drop", "security hotspot", "new-code issue"},
	IntentCICD:          {"pipeline", "workflow", "ci/cd", "ci failed", "ci broken", "regression", "failing build", "red build", "flaky test", "rerun", "workflow_run", "github actions", "jenkins", "gitlab pipeline"},
	IntentIncident:      {"incident", "outage", "prod is down", "on call", "on-call", "pager", "page fired", "rollback", "postmortem", "post-mortem", "sev1", "sev 1", "sev2"},
	IntentDevOpsGeneric: {"devops", "deploy", "deployment", "runbook", "health check", "healthcheck", "uptime", "slo", "sla", "rollback", "release", "gh ", "git "},
}

// intentSeeds maps each intent to the tool names that seed BFS traversal.
var intentSeeds = map[Intent][]string{
	IntentWebResearch:   {"web_search", "web_fetch"},
	IntentFileOps:       {"read_file", "write_file", "edit"},
	IntentCodeExec:      {"bash"},
	IntentBrowser:       {"browser_navigate", "browser_screenshot"},
	IntentMemory:        {"memory_search", "sessions_list"},
	IntentSchedule:      {"cron", "bash"},
	IntentCommunicate:   {"message"},
	IntentSystem:        {"env", "process", "bash", "subagent_run", "subagent_create", "subagent_list"},
	IntentPRReview:      {"chain_run", "devops.github.list_prs", "devops.github.pull_request", "devops.github.pr_diff", "devops.gitlab.list_mrs", "bash", "read_file"},
	IntentSonar:         {"chain_run", "devops.sonar.quality_gate", "devops.sonar.issues"},
	IntentCICD:          {"chain_run", "devops.github.workflow_runs", "devops.github.commit_status", "devops.gitlab.pipelines", "devops.jenkins.job_status", "bash"},
	IntentIncident:      {"chain_run", "devops.github.workflow_runs", "message", "memory_search", "bash"},
	IntentDevOpsGeneric: {"chain_run", "chain_list", "bash", "devops.github.list_prs"},
}

// toolEdge is a directed weighted edge between two tools.
type toolEdge struct {
	from, to string
	typ      EdgeType
	weight   float64
}

// ToolGraph is a weighted directed graph of tool relationships.
type ToolGraph struct {
	mu    sync.RWMutex
	edges []toolEdge

	// Session inertia: tool name → cumulative boost (decays each turn).
	inertia      map[string]float64
	inertiaDecay float64 // multiplied each turn (e.g. 0.7)
}

// NewToolGraph builds the static tool relationship graph.
func NewToolGraph() *ToolGraph {
	g := &ToolGraph{
		inertia:      make(map[string]float64),
		inertiaDecay: 0.7,
	}
	g.edges = []toolEdge{
		// Web research cluster
		{from: "web_search", to: "web_fetch", typ: EdgeCompanion},
		{from: "web_fetch", to: "browser_navigate", typ: EdgeCompanion},
		{from: "web_fetch", to: "bash", typ: EdgeFallback},

		// Browser cluster
		{from: "browser_navigate", to: "browser_screenshot", typ: EdgeCompanion},
		{from: "browser_screenshot", to: "image_understand", typ: EdgeCompanion},

		// File ops cluster
		{from: "write_file", to: "edit", typ: EdgeCompanion},
		{from: "edit", to: "apply_patch", typ: EdgeFallback},
		{from: "read_file", to: "grep", typ: EdgeDomain},
		{from: "read_file", to: "list_dir", typ: EdgeDomain},

		// Code execution cluster
		{from: "bash", to: "process", typ: EdgePrerequisite},
		{from: "bash", to: "write_file", typ: EdgeCompanion},
		{from: "process", to: "env", typ: EdgeDomain},

		// Memory cluster
		{from: "sessions_list", to: "sessions_history", typ: EdgeCompanion},
		{from: "memory_search", to: "sessions_list", typ: EdgeDomain},

		// Communication / automation
		{from: "message", to: "cron", typ: EdgeDomain},
		{from: "cron", to: "bash", typ: EdgeCompanion},
		{from: "env", to: "bash", typ: EdgeDomain},

		// Sub-agent delegation (orchestration)
		{from: "subagent_create", to: "subagent_run", typ: EdgeCompanion},
		{from: "subagent_run", to: "subagent_list", typ: EdgeDomain},
		{from: "subagent_list", to: "subagent_remove", typ: EdgeDomain},
		{from: "bash", to: "subagent_run", typ: EdgeDomain},

		// Smart-prompt chains (pr-review, sonar-triage, cicd-regression, incident-scribe)
		{from: "chain_run", to: "chain_list", typ: EdgeCompanion},
		{from: "chain_list", to: "chain_run", typ: EdgeCompanion},

		// PR review cluster: chains + GitHub/GitLab evidence + bash for local
		// checkout/tests + read_file for spot inspection + message for posting.
		{from: "chain_run", to: "devops.github.list_prs", typ: EdgeCompanion},
		{from: "chain_run", to: "devops.github.pull_request", typ: EdgeCompanion},
		{from: "chain_run", to: "devops.github.pr_diff", typ: EdgeCompanion},
		{from: "chain_run", to: "devops.gitlab.list_mrs", typ: EdgeCompanion},
		{from: "devops.github.list_prs", to: "devops.github.pr_diff", typ: EdgeCompanion},
		{from: "devops.github.pr_diff", to: "read_file", typ: EdgeCompanion},
		{from: "devops.github.pr_diff", to: "bash", typ: EdgeCompanion},
		{from: "devops.github.list_prs", to: "bash", typ: EdgeDomain},

		// SonarQube cluster
		{from: "devops.sonar.quality_gate", to: "devops.sonar.issues", typ: EdgeCompanion},
		{from: "chain_run", to: "devops.sonar.quality_gate", typ: EdgeCompanion},

		// CI/CD cluster: workflows + commit status + Jenkins + pipelines
		{from: "devops.github.workflow_runs", to: "devops.github.commit_status", typ: EdgeCompanion},
		{from: "devops.github.workflow_runs", to: "bash", typ: EdgeDomain},
		{from: "devops.gitlab.pipelines", to: "devops.gitlab.list_mrs", typ: EdgeDomain},
		{from: "devops.jenkins.job_status", to: "bash", typ: EdgeDomain},
		{from: "chain_run", to: "devops.github.workflow_runs", typ: EdgeCompanion},
		{from: "chain_run", to: "devops.gitlab.pipelines", typ: EdgeCompanion},
		{from: "chain_run", to: "devops.jenkins.job_status", typ: EdgeCompanion},

		// Incident cluster: chain_run + message + memory_search + workflow_runs
		{from: "chain_run", to: "message", typ: EdgeCompanion},
		{from: "message", to: "memory_search", typ: EdgeDomain},
	}

	// Set base weights from edge type
	for i := range g.edges {
		g.edges[i].weight = edgeBaseWeight[g.edges[i].typ]
	}
	return g
}

// RecordUsage adds session inertia for a tool that was just called.
// Neighbours gain a boost on the next traversal.
func (g *ToolGraph) RecordUsage(toolName string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, e := range g.edges {
		if e.from == toolName {
			g.inertia[e.to] += 0.3
		}
	}
}

// DecayInertia should be called once per turn to decay all boosts.
func (g *ToolGraph) DecayInertia() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, v := range g.inertia {
		g.inertia[k] = math.Max(0, v*g.inertiaDecay)
	}
}

// Traverse performs BFS from intent-matched seed nodes and returns the top-N
// most relevant tool names. Always includes core tools. No embeddings needed.
func (g *ToolGraph) Traverse(userMessage string, topN int) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// 1. Classify intents from the user message (keyword match)
	lower := strings.ToLower(userMessage)
	seeds := map[string]float64{}
	for intent, keywords := range intentKeywords {
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				for _, seed := range intentSeeds[intent] {
					seeds[seed] = math.Max(seeds[seed], 1.0)
				}
				break // one keyword match per intent is enough
			}
		}
	}

	// 2. BFS — accumulate scores by traversing edges from seeds
	scores := make(map[string]float64)
	for seed, score := range seeds {
		scores[seed] = score
	}

	// BFS depth 2
	for depth := 0; depth < 2; depth++ {
		next := make(map[string]float64)
		for from, fromScore := range scores {
			for _, e := range g.edges {
				if e.from == from {
					bonus := fromScore * e.weight
					if existing, ok := next[e.to]; !ok || bonus > existing {
						next[e.to] = bonus
					}
				}
			}
		}
		for k, v := range next {
			scores[k] += v
		}
	}

	// 3. Apply session inertia boost
	for tool, boost := range g.inertia {
		scores[tool] += boost
	}

	// 4. Sort by score, return top-N names
	type scored struct {
		name  string
		score float64
	}
	var ranked []scored
	for name, score := range scores {
		ranked = append(ranked, scored{name, score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	out := make([]string, 0, topN)
	for _, s := range ranked {
		if len(out) >= topN {
			break
		}
		out = append(out, s.name)
	}
	return out
}

// ClassifyIntents returns sorted, de-duplicated routing labels for intents
// whose keywords appear in the user message (same signals as tool graph BFS seeds).
func (g *ToolGraph) ClassifyIntents(msg string) []string {
	lower := strings.ToLower(msg)
	seen := make(map[string]struct{})
	for intent, keywords := range intentKeywords {
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				var label string
				switch intent {
				case IntentWebResearch:
					label = "WEB_RESEARCH"
				case IntentFileOps:
					label = "FILE_OPS"
				case IntentCodeExec:
					label = "CODE_EXEC"
				case IntentBrowser:
					label = "BROWSER"
				case IntentMemory:
					label = "MEMORY"
				case IntentSchedule:
					label = "SCHEDULE"
				case IntentCommunicate:
					label = "COMMUNICATE"
				case IntentSystem:
					label = "SYSTEM"
				case IntentPRReview:
					label = "PR_REVIEW"
				case IntentSonar:
					label = "SONAR"
				case IntentCICD:
					label = "CICD"
				case IntentIncident:
					label = "INCIDENT"
				case IntentDevOpsGeneric:
					label = "DEVOPS"
				}
				if label != "" {
					seen[label] = struct{}{}
				}
				break
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
