# Agent Context: OpsIntelligence Kanban Integration

> This file captures the architectural decisions, patterns, and current state of the Kanbots-style kanban board integration into OpsIntelligence. Read this before making further changes.

## Project Context

**OpsIntelligence** is a Go-based DevOps agent platform with a vanilla JS dashboard. We're building all Kanbots features natively — zero dependency on Kanbots code.

**Kanbots** (reference only): TypeScript/Electron app with React UI that runs Claude Code / Codex agents in parallel on kanban cards.

## Architecture Decisions

### Agent Runtime
- **Primary**: Go LLM providers (existing `internal/provider/` chain)
- **Secondary**: External CLI adapters as pluggable assignees (claude-code, codex, kimi-code, mcp)
- Cards can be assigned to any registered agent

### Frontend
- **Vanilla JS** (no build step) — consistent with existing dashboard
- Native HTML5 Drag-and-Drop for card moves
- Polling every 5s for live updates (same pattern as Tasks page)

### Data Model
- All kanban state is **durable** in SQLite/Postgres (not in-memory)
- `board_cards` table is source of truth for card state
- `card_runs` table tracks each agent execution
- `card_run_events` streams agent tool_use/tool_result/decision events
- `pending_decisions` captures human-in-the-loop prompts

## File Map

### New Files Created
```
internal/datastore/migrations/sqlite/0003_kanban.sql
internal/datastore/migrations/postgres/0003_kanban.sql

internal/datastore/sqlstore/boards.go
internal/datastore/sqlstore/board_columns.go
internal/datastore/sqlstore/board_cards.go
internal/datastore/sqlstore/card_runs.go
internal/datastore/sqlstore/card_run_events.go
internal/datastore/sqlstore/pending_decisions.go
internal/datastore/sqlstore/board_agents.go
internal/datastore/sqlstore/personas.go

internal/gateway/kanban_api.go

internal/config/seed/personas/
├── product-manager.md
├── senior-engineer.md
├── ux-designer.md
├── growth-lead.md
├── reliability-engineer.md
└── qa-engineer.md
```

### Modified Files
```
internal/datastore/types.go           — added Board, BoardColumn, BoardCard, CardRun, CardRunEvent, PendingDecision, BoardAgent, Persona
internal/datastore/repos.go           — added 8 new repo interfaces
internal/datastore/sqlstore/sqlstore.go — wired repo accessors
internal/datastore/sqlstore/users.go   — added nullableInt, sqlNullInt64 helpers

internal/rbac/permissions.go          — added boards.*, cards.*, runs.*, personas.* permissions
internal/rbac/roles.go                — granted to all built-in roles

internal/gateway/server.go            — registered kanban routes under /api/v1/
internal/gateway/authsvc.go           — added kanban methods to sessionStoreFacade

internal/webui/dashboard/assets/app.html — added Boards nav + view section
internal/webui/dashboard/assets/app.js   — added boards route, render functions, drag-drop
internal/webui/dashboard/assets/style.css — added kanban board + modal styles
```

### New Files (Week 4-6 — COMPLETED)
```
internal/kanban/
├── dispatch_service.go        — orchestrate agent runs on cards ✅
├── decisions.go               — decision prompt detection + resume ✅
├── github_sync.go             — bidirectional GitHub issue sync ✅
├── sentry_import.go           — Sentry error groups → cards ✅
├── mcp_server.go              — expose board over Model Context Protocol ✅
├── autopilot.go               — multi-persona round-robin loop ✅
├── worktree/
│   └── manager.go             — git worktree create/remove/promote ✅
├── cost/
│   └── calculator.go          — per-model pricing + cost rollup ✅
└── dispatcher/
    ├── types.go               — AgentDriver interface, Event types ✅
    └── go_driver.go           — uses internal/provider/ + agent/runner.go ✅
    └── claude_code_driver.go  — spawns claude -p, parses stream-json ⏳
    └── codex_driver.go        — spawns codex CLI ⏳

internal/gateway/kanban_dispatcher.go — KanbanDispatcher interface for gateway ✅
internal/devops/github/github.go      — added ListIssues() ✅

cmd/opsintelligence/gateway_auth.go   — attachKanbanToGateway() ✅
```

### Gateway Wiring Changes
```
internal/gateway/authsvc.go    — added Kanban field to AuthService ✅
internal/gateway/kanban_api.go — dispatch/stop/decision use real kanban.Service ✅
internal/gateway/server.go     — kanban routes unchanged ✅
cmd/opsintelligence/main.go    — attachKanbanToGateway() called after auth ✅
```

## API Endpoints

```
GET    /api/v1/boards
POST   /api/v1/boards
GET    /api/v1/boards/{id}
PUT    /api/v1/boards/{id}
DELETE /api/v1/boards/{id}

GET    /api/v1/boards/{id}/columns
POST   /api/v1/boards/{id}/columns
PUT    /api/v1/boards/{id}/columns/{cid}
DELETE /api/v1/boards/{id}/columns/{cid}

GET    /api/v1/boards/{id}/cards
POST   /api/v1/boards/{id}/cards
GET    /api/v1/boards/{id}/cards/{cid}
PUT    /api/v1/boards/{id}/cards/{cid}
DELETE /api/v1/boards/{id}/cards/{cid}
POST   /api/v1/boards/{id}/cards/{cid}/move
POST   /api/v1/boards/{id}/cards/{cid}/dispatch

GET    /api/v1/boards/{id}/cards/{cid}/runs
GET    /api/v1/runs/{rid}
POST   /api/v1/runs/{rid}/stop
POST   /api/v1/runs/{rid}/decisions/{did}

GET    /api/v1/boards/{id}/agents
POST   /api/v1/boards/{id}/agents

GET    /api/v1/personas
POST   /api/v1/personas
```

## Dashboard Routes

Hash-based SPA routing:
- `#/boards` → board list grid
- `#/boards` (with board open) → kanban board view (set programmatically via `openBoard()`)

## Patterns to Follow

### SQL Repos
- Embed `*Store`: `type xxxRepo struct{ s *Store }`
- Use `r.s.rebind()` for dialect portability
- Use `r.s.scanErr()` for error normalization
- Use `nullable()` / `nullableTime()` / `nullableInt()` for NULLables
- JSON columns: `json.Marshal` on write, `json.Unmarshal` on scan
- Use `uuid.NewString()` for IDs

### API Handlers
- Methods on `*AuthService` (has `Store` + auth middleware)
- Check RBAC: `rbac.Enforce(r.Context(), p, rbac.PermXXX)`
- Use `writeJSON()` / `writeJSONError()` for responses
- Use `getJSON()` on frontend, `csrfHeaders()` for mutating requests

### Frontend
- Route handler added in `route()` switch statement
- Poll with `setInterval`, clear with dedicated `clearXXXPoll()`
- DOM rendered into `#view-xxx` sections
- Use existing `escapeHtml()`, `getJSON()`, `showToast()` helpers

## Built-in Personas

| Icon | Name | Focus |
|------|------|-------|
| 🎯 | Product Manager | User value, prioritization, market fit |
| 🏗 | Senior Engineer | Architecture, dev experience, tech debt |
| 🎨 | UX Designer | Flows, polish, accessibility |
| 📈 | Growth Lead | Activation, retention, virality |
| 🛡 | Reliability Engineer | Robustness, observability, security |
| 🧪 | QA Engineer | Test coverage, edge cases, regression |

## Next Steps

1. **Worktree manager** (`internal/kanban/worktree/`) — git worktree isolation per run
2. **Cost calculator** (`internal/kanban/cost/`) — per-model pricing table
3. **Dispatcher drivers** (`internal/kanban/dispatcher/`) — Go provider + CLI adapters
4. **Dispatch service** — connect cards → agents → runs → events
5. **Decision prompts** — detect agent questions, pause, resume with answer
6. **Autopilot** — multi-persona round-robin loop
7. **GitHub mode** — bidirectional issue sync
8. **Sentry import** — error groups → cards
9. **MCP server** — expose board over Model Context Protocol
