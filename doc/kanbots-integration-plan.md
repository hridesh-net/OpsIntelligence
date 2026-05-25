# Kanbots → OpsIntelligence Integration Plan

## Executive Summary

**Kanbots** is an open-source Electron desktop app (TypeScript/React) that provides a **kanban board UI** for running Claude Code / Codex agents in parallel on cards. **OpsIntelligence** is a Go-based DevOps agent platform with a vanilla JS dashboard, ReAct agent loop, async sub-agent TaskManager, and robust DevOps integrations.

This plan maps every kanbots feature to how it can be incorporated into OpsIntelligence, leveraging existing infrastructure where possible and building new components where needed.

---

## 1. Feature Matrix: Kanbots vs. OpsIntelligence

| # | Kanbots Feature | OpsIntelligence Equivalent | Gap |
|---|----------------|---------------------------|-----|
| 1 | **Kanban Board UI** (5 columns: Inbox/Backlog/Todo/In Progress/Review/Done) | Tasks page (table view) | **Major** — no board view |
| 2 | **Parallel Agent Dispatch** (multiple cards, each in git worktree) | TaskManager (max 4 concurrent sub-agents) | **Medium** — no worktree isolation, no card-based dispatch |
| 3 | **Autopilot** (multi-persona round-robin, up to 4 parallel slots) | Orchestrator + specialists | **Major** — no persona system, no autopilot loop |
| 4 | **Decision Prompts** (agent pauses, numbered options, slash commands) | ProgressEvent `KindBlocked` + `PendingInterventions` | **Minor** — infrastructure exists, needs UI polish |
| 5 | **Personas** (built-in: PM, Engineer, Designer, etc.; custom prompts) | Team Markdown policies + specialist agents | **Medium** — needs persona abstraction layer |
| 6 | **Cost Analytics** (per-run, per-card, per-project, live meter) | No cost tracking | **Major** — new feature |
| 7 | **Git Worktrees** (per-run isolation, branch `kanbots/issue-N`) | No worktree support | **Major** — new feature |
| 8 | **GitHub Mode** (drive real GitHub issues, draft PRs, PAT) | GitHub REST client + GitHub App | **Minor** — most exists, needs board sync |
| 9 | **Sentry Import** (auto-pull error groups onto board) | No Sentry integration | **Medium** — new integration |
| 10 | **MCP Server** (`kanbots-mcp-server` exposes board over MCP) | MCP client exists | **Medium** — need MCP *server* exposing board |
| 11 | **Local-first SQLite** (`.kanbots/db.sqlite`) | SQLite/Postgres datastore | **None** — already exists |
| 12 | **Branch Preview** (start dev server from worktree) | No equivalent | **Medium** — new feature |
| 13 | **Promote / Draft PR** (land worktree as commit or PR) | PR review tool (read-only by default) | **Medium** — needs write path |
| 14 | **QA Mode** (typecheck/test/lint/build/e2e + auto-fix) | No equivalent | **Major** — new feature |
| 15 | **Chat** (general-purpose repo-aware agent) | Chat UI at `/` + RAG chat | **Minor** — exists, needs board integration |

---

## 2. Architecture Overview

### Kanbots Architecture (TypeScript/Electron)
```
packages/
├── core/          — Domain types, GitHub client, IssueSource contract
├── local-store/   — SQLite schema, migrations, repos (cards, issues, agent-runs)
├── dispatcher/    — Agent runtime: spawns claude/codex, parses stream-json, worktrees
├── llm/           — CLI adapters (claude-code, codex-cli), provider catalogue
├── api/           — Pure handler library + agent supervisor (no HTTP server)
├── mcp/           — MCP server exposing board state
├── web/           — React + Vite UI (Board, Chat, Modals, Run detail)
└── desktop/       — Electron shell, IPC bridge, workspace picker
```

### OpsIntelligence Architecture (Go)
```
internal/
├── agent/         — ReAct runner loop, tool execution, memory tiers
├── agents/        — Orchestrator, specialists, flows
├── subagents/     — TaskManager (in-memory async task scheduler)
├── gateway/       — HTTP server, REST API, WebSocket hub, auth
├── webui/         — Dashboard SPA (vanilla JS), chat UI
├── datastore/     — SQLite/Postgres migrations, repos
├── memory/        — Working/episodic/semantic memory (Palace), FTS5 RAG
├── devops/        — GitHub, GitLab, Jenkins, Sonar clients
├── githubapp/     — Multi-tenant GitHub App
├── mcp/           — MCP client (connects to external MCP servers)
├── provider/      — LLM providers (OpenAI, Anthropic, Ollama, etc.)
├── tools/         — Tool registry (devops.*, file, shell, etc.)
└── config/        — YAML config, team policy seeding
```

---

## 3. Integration Strategy: Phased Implementation

### Phase 1: Foundation — Kanban Board + Durable Tasks
**Goal:** Replace the in-memory TaskManager table view with a durable kanban board.

#### 3.1 Data Model (New Tables)
```sql
-- boards: a kanban board per workspace/project
CREATE TABLE boards (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    team_id     TEXT REFERENCES teams(id),
    repo_url    TEXT,
    mode        TEXT NOT NULL DEFAULT 'local', -- local | github
    config_json TEXT NOT NULL DEFAULT '{}',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- board_columns: customizable columns (Backlog, Todo, In Progress, Review, Done)
CREATE TABLE board_columns (
    id          TEXT PRIMARY KEY,
    board_id    TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    position    INTEGER NOT NULL DEFAULT 0,
    color       TEXT,
    config_json TEXT NOT NULL DEFAULT '{}'
);

-- board_cards: tasks/issues on the board
CREATE TABLE board_cards (
    id              TEXT PRIMARY KEY,
    board_id        TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    column_id       TEXT NOT NULL REFERENCES board_columns(id),
    issue_number    INTEGER,              -- for GitHub mode
    title           TEXT NOT NULL,
    description     TEXT,
    card_type       TEXT NOT NULL DEFAULT 'feature', -- bug | feature | refactor | review | spike
    priority        TEXT NOT NULL DEFAULT 'p2',       -- p0 | p1 | p2 | p3
    effort          TEXT,                 -- small | medium | large
    status          TEXT NOT NULL DEFAULT 'queued',   -- queued | running | awaiting | completed | failed
    assignee        TEXT,                 -- agent/persona name
    branch          TEXT,                 -- git branch name
    worktree_path   TEXT,                 -- path to git worktree
    cost_usd        REAL DEFAULT 0,
    token_in        INTEGER DEFAULT 0,
    token_out       INTEGER DEFAULT 0,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME,
    completed_at    DATETIME
);

-- card_runs: each agent execution on a card
CREATE TABLE card_runs (
    id              TEXT PRIMARY KEY,
    card_id         TEXT NOT NULL REFERENCES board_cards(id) ON DELETE CASCADE,
    run_number      INTEGER NOT NULL DEFAULT 1,
    agent_name      TEXT NOT NULL,        -- e.g., "claude", "codex"
    model           TEXT,
    persona_id      TEXT,                 -- references personas
    status          TEXT NOT NULL DEFAULT 'running', -- running | awaiting | completed | failed | stopped
    cost_usd        REAL DEFAULT 0,
    token_in        INTEGER DEFAULT 0,
    token_out       INTEGER DEFAULT 0,
    elapsed_ms      INTEGER DEFAULT 0,
    worktree_path   TEXT,
    branch          TEXT,
    base_branch     TEXT,
    result_summary  TEXT,
    error           TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at    DATETIME
);

-- card_run_events: stream of agent events (tool_use, tool_result, decision)
CREATE TABLE card_run_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id      TEXT NOT NULL REFERENCES card_runs(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,            -- tool_start | tool_end | text | decision | error
    phase       TEXT,
    message     TEXT,
    metadata_json TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- personas: system prompt lenses
CREATE TABLE personas (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    icon        TEXT,                     -- emoji
    description TEXT,
    system_prompt TEXT NOT NULL,
    is_builtin  INTEGER NOT NULL DEFAULT 0,
    created_by  TEXT REFERENCES users(id),
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- autopilot_sessions: orchestrator sessions
CREATE TABLE autopilot_sessions (
    id              TEXT PRIMARY KEY,
    board_id        TEXT NOT NULL REFERENCES boards(id),
    mode            TEXT NOT NULL,        -- feature-dev | qa
    status          TEXT NOT NULL DEFAULT 'running', -- running | paused | completed | stopped
    session_budget_usd REAL,
    spent_usd       REAL DEFAULT 0,
    parallelism     INTEGER NOT NULL DEFAULT 2,
    persona_ids     TEXT NOT NULL DEFAULT '[]', -- JSON array
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    stopped_at      DATETIME
);
```

**Files to modify:**
- `internal/datastore/migrations/sqlite/0002_kanban.sql`
- `internal/datastore/migrations/postgres/0002_kanban.sql`
- `internal/datastore/types.go` — add Go structs
- `internal/datastore/repos.go` — add repo interfaces + implementations

#### 3.2 Backend API (New Handlers)
Following the pattern in `internal/gateway/agent_tasks_api.go` and `internal/gateway/server.go`:

```go
// internal/gateway/kanban_api.go
mux.Handle("/api/v1/boards", s.Protect(http.HandlerFunc(s.handleBoards)))
mux.Handle("/api/v1/boards/", s.Protect(http.HandlerFunc(s.handleBoardDetail)))
mux.Handle("/api/v1/boards/{board_id}/cards", s.Protect(http.HandlerFunc(s.handleCards)))
mux.Handle("/api/v1/boards/{board_id}/cards/{card_id}/runs", s.Protect(http.HandlerFunc(s.handleCardRuns)))
mux.Handle("/api/v1/boards/{board_id}/cards/{card_id}/dispatch", s.Protect(http.HandlerFunc(s.handleDispatch)))
mux.Handle("/api/v1/boards/{board_id}/autopilot", s.Protect(http.HandlerFunc(s.handleAutopilot)))
mux.Handle("/api/v1/personas", s.Protect(http.HandlerFunc(s.handlePersonas)))
mux.Handle("/api/v1/decisions/{decision_id}", s.Protect(http.HandlerFunc(s.handleDecision)))
```

**Files to create:**
- `internal/gateway/kanban_api.go`
- `internal/kanban/` — new package: board service, card service, dispatcher integration

#### 3.3 Frontend — Kanban Board View
The dashboard is vanilla JS (`internal/webui/dashboard/assets/app.js` + `app.html`).

**Add to `app.html`:**
```html
<nav class="nav" id="primary-nav">
  <a class="nav-item" data-route="overview" href="#/overview">Overview</a>
  <a class="nav-item" data-route="boards" href="#/boards">Boards</a>   <!-- NEW -->
  <a class="nav-item" data-route="tasks" href="#/tasks">Tasks</a>
  ...
</nav>

<!-- New view section -->
<section class="view hidden" id="view-boards">
  <div id="boards-container"></div>
</section>
```

**Add to `app.js`:**
```javascript
case "boards":
  titleEl.textContent = "Kanban Boards";
  subEl.textContent = "Agent-driven kanban boards.";
  renderBoardsView(actionsEl);
  break;
```

The kanban board UI needs:
- Horizontal columns with drag-and-drop (native HTML5 DnD or SortableJS)
- Cards showing: title, type badge, priority, assignee, cost, status
- "+ New task" button per column
- Card detail modal (like kanbots' task detail)
- Live agent thread inside card detail
- Decision prompt UI with numbered options

**Files to modify:**
- `internal/webui/dashboard/assets/app.html` — add nav + view section
- `internal/webui/dashboard/assets/app.js` — add route handler, board render functions
- `internal/webui/dashboard/assets/style.css` — add kanban styles

---

### Phase 2: Agent Runtime — Worktrees + Parallel Dispatch

#### 2.1 Git Worktree Isolation
Kanbots creates `.kanbots/worktrees/issue-<n>-<runId>/` for each agent run. We need similar isolation.

**New package: `internal/kanban/worktree/`**
```go
package worktree

type Manager struct {
    BaseDir string  // e.g., ~/.opsintelligence/worktrees/
}

func (m *Manager) Create(cardID, runID, baseBranch string) (path, branch string, error)
func (m *Manager) Remove(path string) error
func (m *Manager) StartDevServer(path string, port int) (*exec.Cmd, error)
func (m *Manager) PromoteToCommit(path, branch, message string) error
```

Each worktree:
- Branched from repo default branch
- Pre-push hook installed (prevents agents from pushing)
- Agent runs inside worktree with cwd set

**Files to create:**
- `internal/kanban/worktree/manager.go`

#### 2.2 Agent CLI Adapter
Kanbots' `packages/dispatcher/src/adapters/claude-code.ts` spawns `claude -p` with stream-JSON output.

OpsIntelligence currently uses Go provider packages (`internal/provider/`). We need a bridge to spawn external CLI agents.

**New package: `internal/kanban/dispatcher/`**
```go
package dispatcher

type AgentCLI interface {
    Run(ctx context.Context, worktreePath, prompt string, opts RunOpts) (<-chan Event, error)
    Stop(runID string) error
}

type ClaudeCodeAdapter struct{}
type CodexCLIAdapter struct{}
```

The adapter:
1. Spawns `claude -p --output-format stream-json` or `codex`
2. Parses stream-json events (`tool_use`, `tool_result`, `thinking`, `error`)
3. Forwards to `card_run_events` table + WebSocket/SSE
4. Detects decision prompts (agent asks a question) → pauses run → emits decision event

**Files to create:**
- `internal/kanban/dispatcher/claude_adapter.go`
- `internal/kanban/dispatcher/codex_adapter.go`
- `internal/kanban/dispatcher/stream_parser.go`
- `internal/kanban/dispatcher/types.go`

#### 2.3 Cost Tracking
Track per-run costs using provider pricing tables.

**New package: `internal/kanban/cost/`**
```go
package cost

var pricing = map[string]modelPricing{
    "claude-opus-4.7":  {inputPer1M: 15.0, outputPer1M: 75.0},
    "claude-sonnet-4.7": {inputPer1M: 3.0,  outputPer1M: 15.0},
    "gpt-5":            {inputPer1M: 1.5,  outputPer1M: 6.0},
}

func Calculate(model string, tokensIn, tokensOut int64) float64
```

Update `card_runs` table with costs after each run. Roll up to `board_cards` and `autopilot_sessions`.

---

### Phase 3: Autopilot + Personas

#### 3.1 Persona System
Personas are named system prompt snippets that lens the agent's behavior.

**Built-in personas** (seeded at boot):
| Icon | Name | Description |
|------|------|-------------|
| 🎯 | Product Manager | User value, prioritization, market fit |
| 🏗 | Senior Engineer | Architecture, dev experience, tech debt |
| 🎨 | UX Designer | Flows, polish, accessibility |
| 📈 | Growth Lead | Activation, retention, virality |
| 🛡 | Reliability Engineer | Robustness, observability, security |
| 🧪 | QA Engineer | Test coverage, edge cases, regression |

**Custom personas:** Users create their own via UI (stored in `personas` table).

When dispatching on a card:
1. User picks persona
2. System prompt = base system prompt + persona snippet + task context
3. Agent runs with that lens

**Files to modify:**
- `internal/config/seed/` — add persona seed files
- `internal/datastore/` — add `personas` table + repo
- Dashboard UI — persona picker modal

#### 3.2 Autopilot Orchestrator
Kanbots' autopilot is a loop that:
1. Claims the next persona from a roster (round-robin)
2. Dispatches an agent on a card
3. Agent may split parent issue into subtasks (new cards)
4. Next cycle picks up new cards
5. Stops on completion, stop button, or cost budget

**New file: `internal/kanban/autopilot/orchestrator.go`**
```go
package autopilot

type Orchestrator struct {
    SessionID   string
    BoardID     string
    Personas    []string
    Parallelism int        // 1-4
    BudgetUSD   float64
    SpentUSD    float64
    // ...
}

func (o *Orchestrator) Start(ctx context.Context) error
func (o *Orchestrator) Stop() error
func (o *Orchestrator) nextSlot() (*Slot, error)
```

Each slot atomically claims the next persona, picks a card from the backlog, and dispatches. Sub-agent runs that create new cards write them to the board.

**QA Mode:**
- Runs `pnpm typecheck`, `pnpm test`, `pnpm lint`, `pnpm build`, `pnpm e2e`
- For each failure, creates a fix card and dispatches
- Repeats until green or budget exhausted

---

### Phase 4: GitHub + Sentry Integration

#### 4.1 GitHub Mode for Boards
Two modes per board:
- **Local mode:** Cards stored in `board_cards` table (SQLite)
- **GitHub mode:** Cards mirror real GitHub issues (bidirectional sync)

**Local mode** (default):
- All cards in local SQLite
- Agent branches: `opsintel/issue-<n>`
- Manual promote to commit / draft PR

**GitHub mode**:
- Board ↔ GitHub repo issues synced via REST API
- Card moves update `status:*` labels on GitHub
- "Open draft PR" creates real PR via GitHub API
- Sentry import pulls error groups as cards

**Files to modify:**
- `internal/devops/github/github.go` — add issue CRUD, PR draft, label sync
- `internal/kanban/` — add `IssueSource` interface (like kanbots' `packages/core/src/issue-source.ts`)

#### 4.2 Sentry Import
New integration: connect Sentry project → poll error groups → create cards on board.

**New file: `internal/kanban/sentry/importer.go`**
```go
package sentry

type Importer struct {
    Client     *sentry.Client
    BoardID    string
    ProjectDSN string
}

func (i *Importer) PollAndCreateCards(ctx context.Context) error
```

---

### Phase 5: MCP Server + Advanced Features

#### 5.1 MCP Server
Expose the kanban board over Model Context Protocol so Cursor, Claude Desktop, etc. can drive it.

**New package: `internal/kanban/mcpserver/`**
```go
package mcpserver

// Exposes tools:
//   - list_boards
//   - list_cards
//   - move_card
//   - dispatch_agent
//   - create_card
//   - get_run_status
```

This is an MCP *server* (OpsIntelligence currently only has an MCP *client* in `internal/mcp/`).

#### 5.2 Branch Preview
From a card's worktree, start the dev server and proxy it:
```go
func (m *WorktreeManager) Preview(cardID string, port int) (url string, error)
```
UI shows "Open preview ↗" link.

#### 5.3 Decision Prompts
When the agent asks a question, the run pauses:
```
Claude: I've drafted three approaches for the password reset token. Which one?
1. Single-use JWT signed with HS256, 15 min expiry
2. DB-stored opaque token, 1 hour expiry, revocable
3. Magic link only — no token, email-verified login
4. I'll explain the tradeoffs first
```

UI shows numbered buttons. Reply box accepts slash commands:
- `/spec` — refine acceptance criteria
- `/review` — spawn reviewer persona
- `/split` — fan out into subtasks

**Implementation:**
- Stream parser detects decision patterns in agent output
- Sets `card_runs.status = 'awaiting'`
- Creates `pending_decisions` row
- UI polls or WebSocket pushes decision event
- User responds → appends to agent context → resumes run

---

## 4. File-Level Implementation Map

### New Files (~25)
```
internal/kanban/
├── board_service.go           — CRUD for boards, columns, cards
├── card_service.go            — Card lifecycle, moves, status
├── run_service.go             — Agent run orchestration
├── autopilot/
│   ├── orchestrator.go        — Autopilot loop
│   ├── feature_dev.go         — Feature-dev autopilot mode
│   └── qa.go                  — QA autopilot mode
├── dispatcher/
│   ├── types.go               — Event types, interfaces
│   ├── claude_adapter.go      — Claude Code CLI spawn + parse
│   ├── codex_adapter.go       — Codex CLI spawn + parse
│   └── stream_parser.go       — stream-json → Events
├── worktree/
│   └── manager.go             — Git worktree create/remove/promote
├── cost/
│   └── calculator.go          — Per-model pricing + cost rollup
├── sentry/
│   └── importer.go            — Sentry error → card import
├── mcpserver/
│   └── server.go              — MCP server exposing board tools
└── decisions/
    └── handler.go             — Decision prompt detection + resume

internal/datastore/migrations/sqlite/0002_kanban.sql
internal/datastore/migrations/postgres/0002_kanban.sql

doc/kanbots-integration-plan.md   (this file)
```

### Modified Files (~12)
```
internal/datastore/types.go          — add Board, BoardColumn, BoardCard, CardRun, Persona, AutopilotSession structs
internal/datastore/repos.go          — add repo interfaces + SQLite implementations
internal/gateway/server.go           — register new kanban API routes
internal/gateway/kanban_api.go       — new REST handlers (boards, cards, runs, dispatch, autopilot, personas, decisions)
internal/gateway/authsvc.go          — add kanban permissions to RBAC
internal/webui/dashboard/assets/app.html   — add Boards nav + view section
internal/webui/dashboard/assets/app.js     — add boards route renderer, kanban DOM functions
internal/webui/dashboard/assets/style.css  — add kanban board CSS (columns, cards, drag-drop)
internal/config/seed/                — seed built-in personas
skills/kanban/SKILL.md               — new skill for kanban operations
```

---

## 5. UI Mockup: Kanban Board View

```
┌─────────────────────────────────────────────────────────────────────────┐
│  OpsIntelligence ◆  Overview | Boards | Tasks | Users | Repo Intel ...  │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  my-app workspace · main                                    + New task  │
│                                                                         │
│  ┌────────────┐  ┌────────────┐  ┌────────────┐  ┌────────────┐        │
│  │ Inbox   1  │  │ Backlog  2 │  │ Todo    2  │  │ In Prog  3 │  ...   │
│  ├────────────┤  ├────────────┤  ├────────────┤  ├────────────┤        │
│  │ SENTRY#42  │  │ FEAT#37    │  │ FEAT#31    │  │ FEAT#24 ⚡ │        │
│  │ TypeError… │  │ Stripe…    │  │ Dark mode  │  │ Sign in…   │        │
│  │ 4h         │  │ 9d         │  │ 4d         │  │ running    │        │
│  │            │  │            │  │            │  │ $0.13 · 18h│        │
│  │            │  │ FEAT#34    │  │ FEAT#28    │  │ FEAT#22 ⚡ │        │
│  │            │  │ Weekly…    │  │ Forgot…    │  │ Email…     │        │
│  │            │  │ 12d        │  │ 6d         │  │ running    │        │
│  │            │  │            │  │            │  │ $0.31 · 14h│        │
│  │            │  │            │  │            │  │ FIX#19 ⏸  │        │
│  │            │  │            │  │            │  │ Profile…   │        │
│  │            │  │            │  │            │  │ awaiting   │        │
│  │            │  │            │  │            │  │ 1h         │        │
│  └────────────┘  └────────────┘  └────────────┘  └────────────┘        │
│                                                                         │
│  .opsintelligence/db.sqlite · local-first · 0 telemetry · claude-ready │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### Card Detail Modal (Kanbots-style)
```
┌────────────────────────────────────────────────────────────┐
│  #28 · Forgot password email flow                    [×]   │
│  FEAT · P2 · feat/forgot-password · off main               │
├────────────────────────────────────────────────────────────┤
│  Overview | Thread | Diff | Preview | Runs                 │
│                                                            │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ 🤖 claude · 3m ago                                  │   │
│  │ I've drafted three approaches for the password      │   │
│  │ reset token. Which one fits your security reqs?     │   │
│  │                                                     │   │
│  │ 1️⃣ Single-use JWT signed with HS256, 15 min expiry │   │
│  │ 2️⃣ DB-stored opaque token, 1 hour expiry, revocable│   │
│  │ 3️⃣ Magic link only — no token, email-verified login│   │
│  │ 4️⃣ I'll explain the tradeoffs first                 │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                            │
│  Reply to agent · /spec to refine · /review to spawn …    │
│  [______________________________________________] [Send]   │
│                                                            │
│  Live run        model: claude-opus-4.7                    │
│  elapsed: 3m18s  tokens: 7.7k in   cost: $0.10             │
│  [Run checks]  [Open worktree ↗]  [Stop · Fork]            │
└────────────────────────────────────────────────────────────┘
```

---

## 6. Reuse Analysis: What Already Exists

### ✅ Can Reuse As-Is
| Component | Location | How |
|-----------|----------|-----|
| **SQLite/Postgres datastore** | `internal/datastore/` | Add new tables + repos |
| **Auth + RBAC** | `internal/gateway/authsvc.go`, `internal/rbac/` | Add `kanban.*` permissions |
| **Agent runner loop** | `internal/agent/runner.go` | Use as base for CLI adapters |
| **TaskManager** | `internal/subagents/tasks.go` | Pattern for async dispatch |
| **ProgressEvent stream** | `internal/subagents/tasks.go` | Extend with `KindDecision` |
| **Intervention system** | `PendingInterventions` | Use for decision responses |
| **GitHub REST client** | `internal/devops/github/github.go` | Extend for issue sync + PRs |
| **GitHub App** | `internal/githubapp/` | Webhook → card creation |
| **WebSocket hub** | `internal/gateway/hub.go` | Push board updates in real-time |
| **Config API pattern** | `internal/gateway/config_api.go` | Follow for board settings |
| **Dashboard SPA shell** | `internal/webui/dashboard/` | Add route + view + CSS |
| **Memory / RAG** | `internal/memory/` | Ground agent context in repo |
| **Provider catalogue** | `internal/provider/catalogs/` | Pick models for agent runs |
| **MCP client** | `internal/mcp/` | Pattern for MCP server impl |

### ⚠️ Needs Adaptation
| Component | Kanbots | OpsIntelligence | Work Required |
|-----------|---------|-----------------|---------------|
| **Issue storage** | `local-store` SQLite (`.kanbots/db.sqlite`) | `internal/datastore` SQLite/Postgres | Merge schema into existing migrations |
| **Agent dispatch** | `dispatcher` spawns `claude -p` | `subagents` runs Go functions | Build CLI adapter layer |
| **Stream parsing** | `dispatcher/src/stream-parser.ts` | SSE tokens in `gateway/server.go` | Port stream-json parser to Go |
| **Board UI** | React + Vite (`packages/web/`) | Vanilla JS SPA | Build kanban DOM in vanilla JS |
| **Cost tracking** | `dispatcher/src/pricing.ts` | None | New Go package |
| **Worktrees** | `dispatcher/src/worktree.ts` | None | New Go package using `git worktree` |

### ❌ Needs to Be Built
| Feature | Why New |
|---------|---------|
| **Kanban board UI** (columns, DnD, cards) | No equivalent exists |
| **Persona system** | Team policies are static Markdown; personas are dynamic runtime lenses |
| **Autopilot orchestrator** | No loop-based multi-persona dispatch |
| **Decision prompt UI** | No structured decision/intervention UI |
| **Cost analytics** | No cost tracking at all |
| **Git worktree manager** | No worktree support |
| **Sentry importer** | No Sentry integration |
| **MCP server** | Only MCP client exists |
| **Branch preview** | No dev server proxy |
| **QA mode** | No automated check → fix loop |

---

## 7. Risk Assessment & Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| **Vanilla JS kanban UI is complex** | High | Medium | Consider embedding a lightweight React build for the board view only, or use a library like SortableJS + custom CSS |
| **External CLI dependency** (claude, codex) | Medium | High | Make optional; fallback to existing Go provider chain |
| **Git worktree bloat** | Medium | Medium | Auto-reap old worktrees; configurable retention |
| **Cost overruns** | Medium | High | Per-run and per-session caps; hard stop in orchestrator |
| **GitHub API rate limits** | Medium | Low | ETag caching; backoff; local mode as fallback |
| **Schema migration conflicts** | Low | High | Follow existing migration numbering; test both SQLite + Postgres |

---

## 8. Recommended Starting Point

### Week 1-2: Data Model + Basic Board API
1. Create `0002_kanban.sql` migrations (SQLite + Postgres)
2. Add Go types to `internal/datastore/types.go`
3. Implement repos in `internal/datastore/repos.go`
4. Create `internal/gateway/kanban_api.go` with CRUD for boards/columns/cards
5. Wire routes in `internal/gateway/server.go`

### Week 3-4: Board UI (Vanilla JS)
1. Add "Boards" nav to `app.html`
2. Implement `renderBoardsView()` in `app.js`
3. Build kanban CSS (columns, cards, drag-drop)
4. Connect to REST API

### Week 5-6: Agent Dispatch (No Worktrees Yet)
1. Build `internal/kanban/dispatcher/` with Claude/Codex adapters
2. Add `POST /api/v1/boards/{id}/cards/{id}/dispatch`
3. Stream events to `card_run_events` table
4. Show live agent thread in card detail modal

### Week 7-8: Personas + Decision Prompts
1. Add `personas` table + built-in seeds
2. Persona picker in dispatch UI
3. Decision detection in stream parser
4. Decision UI with numbered options + slash commands

### Week 9-10: Autopilot v1
1. Build `internal/kanban/autopilot/orchestrator.go`
2. Feature-dev mode (round-robin personas)
3. Session budget + cost tracking
4. Autopilot launch modal

### Week 11+: Polish + Advanced Features
- Git worktree isolation
- GitHub mode sync
- Sentry import
- MCP server
- Branch preview
- QA mode

---

## 9. Key Design Decisions

### 9.1 Vanilla JS vs. React for Board UI
**Current state:** Dashboard is vanilla JS (no build step). Kanbots uses React.
**Options:**
1. **Keep vanilla JS** — consistent with existing dashboard, no build complexity. Kanban DOM is manageable with modern JS.
2. **Embed React build** — ship a separate `board.js` bundle built from React/TSX, loaded only on `#/boards`. More powerful but adds build toolchain.

**Recommendation:** Start with vanilla JS + SortableJS for DnD. If complexity exceeds ~800 lines of DOM manipulation, migrate to a lightweight embedded React build.

### 9.2 In-Memory vs. Durable TaskManager
**Current state:** `TaskManager` is in-memory (evicts after 256 tasks).
**Kanban requirement:** Cards must survive restarts.
**Decision:** New `card_runs` table is the durable source of truth. In-memory `TaskManager` can be used as a runtime cache for active runs only.

### 9.3 Go Provider vs. External CLI
**Current state:** OpsIntelligence uses Go provider packages (OpenAI, Anthropic SDKs).
**Kanbots:** Spawns `claude` or `codex` CLI.
**Decision:** Support both. Default to existing Go providers for simplicity. Add external CLI as an advanced option for users who want the exact Kanbots experience (Claude Code's agentic mode, Codex CLI).

### 9.4 One Board vs. Multiple Boards
**Kanbots:** One board per workspace (repo folder).
**OpsIntelligence:** Multi-team, multi-repo.
**Decision:** Support multiple boards. `boards` table has `team_id` foreign key. Each team can have multiple boards (e.g., one per repo).

---

## 10. Conclusion

OpsIntelligence already has ~60% of the infrastructure needed for a Kanbots-style kanban board:
- ✅ Datastore (SQLite/Postgres)
- ✅ Auth + RBAC
- ✅ Agent loop + async tasks
- ✅ GitHub integration
- ✅ Dashboard SPA shell
- ✅ WebSocket / SSE streaming
- ✅ Config + team policy system

The remaining ~40% is:
- 🆕 Kanban board UI (columns, cards, DnD)
- 🆕 Durable card/run storage (extend datastore)
- 🆕 External CLI agent adapters (Claude Code, Codex)
- 🆕 Persona system
- 🆕 Autopilot orchestrator
- 🆕 Cost tracking
- 🆕 Git worktree isolation
- 🆕 Decision prompt UI
- 🆕 Sentry importer
- 🆕 MCP server

This is a **3-4 month project** for 1-2 engineers working full-time, deliverable in the phased approach above. The foundation (data model + basic board) can be demo-ready in **2-3 weeks**.
