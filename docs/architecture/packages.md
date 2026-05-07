# Packages & layout

| Area | Path | Role |
| ---- | ---- | ---- |
| CLI / wiring | [`cmd/opsintelligence`](https://github.com/hridesh-net/OpsIntelligence/tree/main/cmd/opsintelligence) | Cobra commands, loads config, constructs provider, memory manager, `graph.NewToolGraph()`, `tools.NewCatalog`, `agent.Runner`, gateway server, optional `repointel.Manager`, cron daemon. |
| Agent loop | [`internal/agent`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/agent) | `Runner` (`Run`, `RunStream`, autonomous variant), prompts, tool execution loop, planning/reflection hooks, security hooks, local intel integration, runtrace emission. |
| Tool graph | [`internal/graph`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/graph) | `ToolGraph` ([`tool_graph.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/graph/tool_graph.go)): intent keywords, seeded BFS, session inertia. `SkillGraph` ([`skill_graph.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/graph/skill_graph.go)): wikilinks, skill nodes, tool edges. |
| Tools | [`internal/tools`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/tools) | Built-in tools (`Catalog`, `find_tools`, `devops.*`, `chain_run`, subagent tools, cron tool, …). |
| Skills | [`internal/skills`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/skills) | Skill loader, registry, `skill_graph_index` / `read_skill_node` ([`graph_index.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/skills/graph_index.go), [`node_tool.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/skills/node_tool.go)). |
| Repo intelligence | [`internal/repointel`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/repointel) | `Registry`, `Indexer`, `Scanner`, `Manager`, `HybridStore`, call graph builder, optional semantic mirroring into agent memory. |
| Gateway | [`internal/gateway`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/gateway) | HTTP server, SSE chat, auth, dashboard API, repo REST ([`repos_api.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/gateway/repos_api.go)). |
| Memory | [`internal/memory`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/memory) | Episodic + semantic stores, mining/RAG helpers. |
| Sub-agents | [`internal/subagents`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/subagents) | Per-subagent store, async task manager, orchestration helpers. |
| Specialists | [`internal/agents`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/agents) | Built-in specialist definitions and [`Orchestrator`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/agents/orchestrator.go) routing. |
| Observability | [`internal/observability`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/observability) | Runtrace NDJSON (`runtrace`), tracing/metrics helpers. |

See also [Resources](resources.md) for how these packages interact with disk and network.
