# Agents & graphs

## Master runner

Single **`agent.Runner`** per logical session (`WithSession`). Holds working messages, `ToolCatalog`, provider, memory handles, optional security (`Guardrail`, `AuditLog`).

## Specialists and sub-agents

- **Sub-agents** — separate runner instances inside `tools.SubAgentSvc` with narrowed registries (often `CloneWithout` recursion-prone tools). Session ids like `subagent:…` or task ids with per-task trace dirs under `logs/subagents/<task-id>/` when used.
- **Orchestrator** — [`internal/agents/orchestrator.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/agents/orchestrator.go) routes queries to registered specialists (spawn/sync/async patterns).

Delegation tools: `subagent_run`, `subagent_run_async`, `subagent_run_parallel` ([`internal/tools/subagent.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/tools/subagent.go), [`subagent_async.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/tools/subagent_async.go)).

## RunStream loop

[`runner.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/agent/runner.go): persist user message → optional planning → per-iteration `buildRequestV3` → stream → collect tool calls → execute tools → append results → until finish or `MaxIterations` (default 64 in `applyDefaults`). `Run` shares the same core path with `CollectStream`.

## Guardrails and audit

`Runner.WithSecurity`: tool/skill events can be checked and logged via [`internal/security`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/security). Audit path wiring lives in `cmd/opsintelligence/main.go`.

## `tool_graph` vs `skill_graph`

| Piece | Role |
| ----- | ---- |
| **`internal/graph.ToolGraph`** ([`tool_graph.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/graph/tool_graph.go)) | Static weighted edges between **tool names**, keyword intent buckets, **BFS** with session inertia (`Traverse`, `RecordUsage`, `DecayInertia`). Narrows tools sent to the LLM. |
| **`internal/graph.SkillGraph`** ([`skill_graph.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/graph/skill_graph.go)) | Graph over **skill Markdown nodes**: ENTRY, EXTENDS (`[[wikilink]]`), REQUIRES/EXAMPLE from frontmatter, TOOL edges. Built when skills load; separate from provider tool payload shrink. |
| **`tools.Catalog`** ([`catalog.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/tools/catalog.go)) | Joins `ToolGraph` with `agent.ToolRegistry`: always includes a **core** slug set (`bash`, `read_file`, `find_tools`, `read_skill_node`, …) plus graph-selected tools (`SelectForRequest`, default traverse top 8). Providers with `RequiresAllTools` skip narrowing. |

Lazy skill discovery uses **`skill_graph_index`** ([`graph_index.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/skills/graph_index.go)) and **`read_skill_node`** so the full skill tree is not inlined every request.
