# Architectural Patterns

## 1. Orchestrator-workers

A central **orchestrator** agent delegates sub-tasks to specialized **worker** agents.

```
Orchestrator: "Review this PR"
  → Worker: "Fetch diff" → result
  → Worker: "Analyze code" → result
  → Worker: "Post review" → result
Orchestrator: "Done"
```

**Best for:** Complex workflows with clear sequential or parallel steps.

## 2. Router

A lightweight **router** agent inspects the input and forwards it to the right specialist.

```
Input: "High CPU alert on prod-web-3"
  → Router: classify as "incident" → IncidentResponseAgent
Input: "Review PR #123"
  → Router: classify as "pr-review" → PRReviewAgent
```

**Best for:** Multi-tenant or multi-domain systems where one entrypoint serves many use cases.

## 3. Swarm / P2P

Agents communicate peer-to-peer via a message bus. No central coordinator.

```
Agent-A: "I need the deploy log"
  → (broadcast) → Agent-B: "I have it" → reply
```

**Best for:** Decentralized systems, self-organizing teams of agents.

## 4. Supervisor-review

A **supervisor** monitors worker agents and can approve, reject, or retry their outputs.

```
Worker: "I'll delete the production database"
  → Supervisor: "REJECT — that's destructive"
  → Worker: "OK, I'll create a backup first"
  → Supervisor: "APPROVE"
```

**Best for:** Safety-critical operations, high-stakes automation.

## Choosing a pattern

| Pattern | Latency | Complexity | Safety | Scalability |
|---------|---------|-----------|--------|-------------|
| Orchestrator-workers | Medium | Low | Medium | Medium |
| Router | Low | Low | Medium | High |
| Swarm | Variable | High | Low | High |
| Supervisor-review | High | Medium | High | Low |
