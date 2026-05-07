# Agents & tools

This page summarizes **operator-facing** behavior. For package-level internals, see [Architecture — Agents & graphs](architecture/agents-and-graphs.md).

## Runner loop

The **`agent.Runner`** executes the core loop: build the provider request, stream completion, run tool calls, persist episodic memory, repeat until finished or iteration limits. Ingress paths (CLI, gateway SSE/WebSocket, Slack, webhooks, cron) all converge on `Run` / `RunStream` with a session id.

Sub-agents run as separate runner instances with a cloned, narrowed tool registry (`subagent_run`, async and parallel variants).

Specialist routing uses [`internal/agents.Orchestrator`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/agents/orchestrator.go): keyword scoring selects registered specialists for spawn/sync/async delegation patterns.

## Skills

Lazy-loaded skill graphs live under configurable `agent.skills_dir`. Shipped DevOps nodes are under [`skills/devops/`](https://github.com/hridesh-net/OpsIntelligence/tree/main/skills/devops/) — map in [`SKILL.md`](https://github.com/hridesh-net/OpsIntelligence/blob/main/skills/devops/SKILL.md).

Agents discover nodes via **`skill_graph_index`** and pull details with **`read_skill_node`** so the entire tree is not inlined every turn.

## Smart prompts & chains

Complex DevOps flows use **named chains** (gather → analyze → critique → render) exposed through the **`chain_run`** tool.

```bash
opsintelligence prompts ls
opsintelligence prompts show pr-review
opsintelligence prompts run pr-review --input pr_url=https://…
```

Shipped chains include `pr-review`, `sonar-triage`, `cicd-regression`, `incident-scribe`. Override prompt files under `~/.opsintelligence/prompts/<id>.md`.

Design and file-format detail: [`doc/smart-prompts.md`](https://github.com/hridesh-net/OpsIntelligence/blob/main/doc/smart-prompts.md).

## Tool catalog & graph

**`tools.Catalog`** combines **`internal/graph.ToolGraph`** with the **`agent.ToolRegistry`**: a core tool slug set is always available; optional graph narrowing selects additional tools per request unless the provider requires the full tool list.

Distinction from skills: **ToolGraph** connects tool names and intent keywords; **SkillGraph** connects Markdown skill nodes and wikilinks. See [Agents & graphs](architecture/agents-and-graphs.md).

## Built-in DevOps surface

`opsintelligence tools ls` includes **`devops.*`** tools for GitHub, GitLab, Jenkins, Sonar, and related workflows. MCP extends coverage to additional systems when configured.

## Commands

```bash
opsintelligence run "…"        # one-shot turn
opsintelligence skills ls
opsintelligence tools ls
```

Run `opsintelligence <cmd> --help` for flags.
