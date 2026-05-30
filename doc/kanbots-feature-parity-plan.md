# Kanbots Feature Parity Plan for OpsIntelligence

## Executive Summary

This plan maps every capability of [Kanbots](https://www.kanbots.dev/) (kanban board + parallel agent dispatch system) onto the existing OpsIntelligence kanban foundation. The goal is **feature parity** — a native, zero-dependency implementation that lives entirely within the OpsIntelligence codebase.

**Current state:** Data model, REST API, worktree manager, cost calculator, 3 agent drivers (Go/Claude Code/Codex), decision detection, and basic dashboard UI are implemented and tested.

**Gap:** Autopilot orchestration, GitHub bidirectional sync, Sentry import, MCP server, branch preview, decision resume loop, live event streaming UI, task templates, spec-first flow, and team/collaboration features are missing or stubbed.

---

## Feature Areas & Acceptance Criteria

### AREA 1: Core Board & Card Lifecycle

#### F1.1 — Kanban Board with Drag-and-Drop
**Current:** ✅ Implemented. Columns, cards, drag-and-drop, DOM-diffed polling refresh.

**AC:**
- [x] Board list view renders all boards with mode/repo metadata.
- [x] Board detail view renders columns horizontally with cards.
- [x] Cards show type, priority, status icon, cost.
- [x] Native HTML5 drag-and-drop moves cards between columns.
- [x] WIP limit enforced on move (HTTP 409 if exceeded).
- [x] 5s polling with Page Visibility API + request deduplication.

#### F1.2 — Column Management UI
**Current:** ❌ API exists (`POST/PUT/DELETE /boards/{id}/columns`), but no dashboard UI.

**AC:**
- [ ] Board view shows an "Edit columns" button in toolbar.
- [ ] Modal lists existing columns with name, position, color, WIP limit.
- [ ] User can add a new column (name, position, optional color + WIP limit).
- [ ] User can edit column name, position, color, WIP limit.
- [ ] User can delete a column (cards must be moved first or deleted).
- [ ] Changes reflect immediately via DOM update without full refresh.

#### F1.3 — Inline Card Editing
**Current:** ❌ Card detail modal is read-only except for dispatch/delete.

**AC:**
- [ ] Card detail modal allows editing title, description, priority, effort, assignee.
- [ ] Description supports markdown rendering.
- [ ] Priority changes reflect immediately on the board (color border update).
- [ ] Assignee can be set to a board agent or left unassigned.
- [ ] Changes persist via `PUT /boards/{id}/cards/{cid}`.

#### F1.4 — Card Reordering Within Columns
**Current:** ❌ No `position` field on cards; cards sort by `created_at DESC` only.

**AC:**
- [ ] `board_cards` table gains an `position INTEGER` column.
- [ ] Cards within a column sort by `position ASC`.
- [ ] Drag-and-drop within the same column reorders cards.
- [ ] `POST /boards/{id}/cards/{cid}/move` accepts optional `position` parameter.
- [ ] Backend renumbers positions in batches to avoid gaps.

---

### AREA 2: Agent Dispatch & Runtime

#### F2.1 — Multi-Driver Agent Runtime
**Current:** ✅ Go, Claude Code, Codex drivers implemented. Go driver wired to `srv.Runner`.

**AC:**
- [x] `go` driver wraps `agent.Runner` with `StreamHandler` adapter.
- [x] `claude-code` driver spawns `claude -p --output-format stream-json`.
- [x] `codex` driver spawns `codex --approval-mode auto-quiet`.
- [x] All drivers run in per-card git worktrees with pre-push hooks.
- [x] Non-blocking event channel (drop on overflow, never deadlock).
- [x] `AgentDriver` interface is stable and extensible.

#### F2.2 — Task Templates & Start Modes
**Current:** ❌ No templates. Cards are created bare; dispatch always spawns immediately.

**AC:**
- [ ] Create-card modal offers templates: Bug fix, Feature, Refactor, Review, Spike.
- [ ] Each template pre-fills description with a structured prompt scaffold.
- [ ] Three start modes available on create:
  - **Spec first** — runs `/spec` in worktree, pauses for user approval before implementation.
  - **Create & dispatch** — spawns agent immediately (current behavior).
  - **Queue for later** — card lands in Backlog; manual dispatch required.
- [ ] Spec-first mode stores the spec output on the card and creates a pending decision.
- [ ] Template definitions stored in `personas` or a new `recipes` table.

#### F2.3 — Run Number Auto-Increment
**Current:** ❌ `run_number` column exists but is never populated.

**AC:**
- [ ] On `Dispatch`, query `MAX(run_number) WHERE card_id = ?` and increment.
- [ ] Run number displayed in UI as `#N` next to run status.
- [ ] Run number used in worktree branch name: `opsintel/issue-<n>-<run_number>`.

---

### AREA 3: Decision Prompts & Resume Loop

#### F3.1 — Decision Detection & Pause
**Current:** ⚠️ `DecisionDetector` exists but is **not wired into any driver**.

**AC:**
- [ ] Go driver pipes agent output through `DecisionDetector` before emitting `text` events.
- [ ] Claude Code driver detects decision patterns in stream-json output (or uses `decision` type if CLI supports it).
- [ ] When a decision is detected, driver emits `Event{Kind: "decision", Message: question, Metadata: options}`.
- [ ] `DispatchService.runAgent` receives `decision` event, flushes batch, calls `pauseForDecision()`.
- [ ] Run status becomes `awaiting`, card status becomes `awaiting`.
- [ ] Pending decision created with UUID, question, options JSON.

#### F3.2 — Decision Answering UI
**Current:** ❌ API exists (`POST /runs/{rid}/decisions/{did}`), but dashboard never renders decisions.

**AC:**
- [ ] Card detail modal queries pending decisions for the card's active run.
- [ ] If a pending decision exists, modal renders question + numbered option buttons.
- [ ] User clicks an option → `POST /runs/{rid}/decisions/{did}` with answer.
- [ ] UI shows "awaiting input" state with reply box.
- [ ] Reply box supports slash commands: `/spec`, `/review`, `/split`.

#### F3.3 — Decision Resume (Inject Answer Back into Agent)
**Current:** ❌ `AnswerDecision` only flips DB status to `running`. Agent context is lost.

**AC:**
- [ ] `DispatchService` maintains an in-memory map of active `runID → driver session`.
- [ ] When a decision is answered, the answer is injected back into the agent as a user message.
  - **Go driver:** append answer to `memory.Manager` conversation, resume `runner.Run()`.
  - **Claude Code:** not resumable (stateless CLI) — spawn new `claude` process with context in prompt.
  - **Codex:** same as Claude Code — respawn with context.
- [ ] If the driver does not support resume, spawn a new run with the decision context prepended.
- [ ] Decision + answer recorded as `CardRunEvent` entries for audit trail.

#### F3.4 — Fork Run from Decision
**Current:** ❌ No fork capability.

**AC:**
- [ ] "Fork run" button appears on cards with pending decisions.
- [ ] Forking creates a new `CardRun` with the same card, copying the worktree.
- [ ] Original run can be stopped or continued independently.
- [ ] Forked run starts from the decision point with the chosen answer baked in.

---

### AREA 4: Live Event Streaming & Dashboard

#### F4.1 — Run Event Stream in Card Detail
**Current:** ❌ Modal shows run list but not individual events.

**AC:**
- [ ] Card detail modal has tabs: Overview | Thread | Diff | Preview | Runs.
- [ ] **Thread tab** shows live agent events: text tokens, tool_use, tool_result, errors.
- [ ] Events fetched via `GET /runs/{rid}` (includes up to 500 events + decisions).
- [ ] Events render with icons: 📝 text, 🔧 tool start, ✅ tool end, ⚠️ error, ⏸ decision.
- [ ] Tool events are collapsible (show/hide input + result).

#### F4.2 — Real-Time Event Polling
**Current:** ⚠️ Board polls every 5s, but run events are not polled.

**AC:**
- [ ] When card detail modal is open on a running/awaiting run, poll events every 2s.
- [ ] Use `SinceID` parameter to fetch only new events since last poll.
- [ ] Append new events to the Thread tab without re-rendering the entire list.
- [ ] Stop polling when run reaches terminal state (`completed`, `failed`, `stopped`).

#### F4.3 — Server-Sent Events (SSE) Alternative
**Current:** ❌ Pure polling. No SSE/WebSocket.

**AC (optional, Phase 3+):**
- [ ] `GET /api/v1/runs/{rid}/events/stream` returns SSE stream.
- [ ] Server pushes new `CardRunEvent` rows as they are inserted.
- [ ] Dashboard subscribes to SSE when modal is open; falls back to polling if SSE unavailable.

---

### AREA 5: Cost Analytics & Budget Caps

#### F5.1 — Live Cost Tracking
**Current:** ✅ Cost calculator with prefix matching. Atomic `AddCost` on card.

**AC:**
- [x] Per-run cost computed from token usage × model pricing.
- [x] Per-card cost aggregated via atomic SQL updates.
- [x] Cost displayed on card ($X.XX) and in modal run list.
- [ ] Board header shows today's total cost across all cards.

#### F5.2 — Per-Run Budget Cap
**Current:** ❌ No budget enforcement.

**AC:**
- [ ] Board config supports `max_cost_per_run_usd` (default: unlimited).
- [ ] `DispatchService` checks card cost + projected run cost against cap before starting.
- [ ] If cap exceeded, run is rejected with error message.
- [ ] During run, if accumulated cost exceeds cap, driver is cancelled and run marked `stopped`.

#### F5.3 — Per-Session Budget Cap
**Current:** ❌ No session-level tracking.

**AC:**
- [ ] Board config supports `max_cost_session_usd`.
- [ ] Session cost = sum of all run costs since board creation (or reset).
- [ ] Autopilot checks session budget before each cycle; stops if exceeded.
- [ ] Dashboard shows session budget meter: `$4.27 of $25.00`.

---

### AREA 6: Autopilot — Multi-Persona Orchestration

#### F6.1 — Persona Roster & Round-Robin
**Current:** ⚠️ `personas` table + CRUD API exists. 6 built-in personas seeded. No orchestration loop.

**AC:**
- [ ] Autopilot config: select 1+ personas, set parallelism (1–4), set model, set effort.
- [ ] Autopilot service maintains a round-robin counter across selected personas.
- [ ] Each slot atomically claims the next persona from the roster.
- [ ] Slots run concurrently; each gets its own worktree.

#### F6.2 — Backlog Scan & Card Claiming
**Current:** ❌ No autopilot service exists.

**AC:**
- [ ] Autopilot scans board columns for eligible cards (Backlog/Todo, status `queued`).
- [ ] Claims a card by setting `status = running`, `assignee = autopilot`.
- [ ] Dispatches the claimed card with the slot's assigned persona.
- [ ] When slot finishes, immediately claims the next eligible card.

#### F6.3 — Self-Evolving Backlog (Personas Spawn Cards)
**Current:** ❌ No card creation from agents.

**AC:**
- [ ] Agent can emit a special `spawn_card` event (or detect it in output).
- [ ] `DispatchService` creates a new card on the same board when `spawn_card` detected.
- [ ] New card lands in Backlog with title/description from agent output.
- [ ] Autopilot picks up spawned cards in subsequent cycles.
- [ ] Child cards linked to parent via `parent_id` column (new).

#### F6.4 — QA Mode
**Current:** ❌ Not implemented.

**AC:**
- [ ] QA mode runs typecheck, tests, lint, build, e2e in the worktree.
- [ ] User selects which checks to run from a checklist UI.
- [ ] For each failing check, autopilot dispatches a fix run on a derived child issue.
- [ ] Repeats until all checks pass or budget exhausted.
- [ ] Results summarized in a QA report card.

#### F6.5 — Autopilot Dashboard UI
**Current:** ❌ No UI.

**AC:**
- [ ] "Start Autopilot" button on board toolbar.
- [ ] Modal for configuring personas, parallelism, model, effort, session budget.
- [ ] Live autopilot status panel showing:
  - Current cycle number
  - Active slots (persona, card, status)
  - Session cost vs budget
  - Cards completed this session
- [ ] Stop button kills all active slots and prevents new claims.

---

### AREA 7: GitHub Integration

#### F7.1 — GitHub Mode (Bidirectional Issue Sync)
**Current:** ❌ `github_sync.go` does not exist. Board has `mode` field (`local`/`github`).

**AC:**
- [ ] Board can be created in `github` mode with `repo_url` + personal access token.
- [ ] `GitHubSyncService` polls GitHub Issues API every 5 minutes.
- [ ] New GitHub issues → created as cards in Inbox column.
- [ ] Card title/description updates sync back to GitHub issue.
- [ ] Card moves to Done → closes GitHub issue.
- [ ] GitHub issue closes → card moves to Done.
- [ ] `issue_number` field on card populated from GitHub issue number.

#### F7.2 — Draft PR from Worktree
**Current:** ✅ `WorktreeManager.OpenDraftPR()` implemented via `gh` CLI.

**AC:**
- [x] `PromoteToCommit` stages changes and creates commit in worktree.
- [x] `OpenDraftPR` pushes branch and opens draft PR via `gh pr create`.
- [ ] Dashboard shows "Open draft PR" button on completed cards (GitHub mode only).
- [ ] PR URL stored on card and displayed as link.

#### F7.3 — GitHub App Mode (Team/Org)
**Current:** ⚠️ GitHub App handler exists (`internal/githubapp`) but not wired to kanban.

**AC (Phase 3+):**
- [ ] GitHub App installations can be linked to boards.
- [ ] Org-level webhook events create cards automatically.
- [ ] Managed GitHub App (Cloud tier) — no PAT required.

---

### AREA 8: Sentry Import

#### F8.1 — Sentry Error Group → Card
**Current:** ❌ `sentry_import.go` does not exist.

**AC:**
- [ ] Board settings support Sentry DSN / auth token.
- [ ] `SentryImportService` fetches error groups from Sentry API.
- [ ] Each error group creates a card in Inbox with:
  - Title: error message
  - Description: stack trace + sentry link
  - Type: `bug`
  - Priority: auto-derived from event count (P0 if >100 events)
- [ ] Manual "Import from Sentry" button on board.
- [ ] Optional auto-poll every hour.
- [ ] Deduplication: don't create card if sentry issue ID already exists on board.

---

### AREA 9: MCP Server

#### F9.1 — Model Context Protocol Server
**Current:** ❌ `mcp_server.go` does not exist.

**AC:**
- [ ] Implement `kanbots-mcp-server` binary using `github.com/mark3labs/mcp-go`.
- [ ] Exposes board as MCP resources + tools:
  - `list_boards` — list all boards
  - `get_board` — get board + columns + cards
  - `create_card` — create a new card
  - `move_card` — move card to column
  - `dispatch_card` — start agent run on card
  - `get_run_events` — get run event stream
  - `answer_decision` — answer pending decision
- [ ] MCP server runs as a stdio or HTTP(SSE) transport.
- [ ] Works with Cursor, Claude Desktop, and any MCP-aware client.
- [ ] Auth via API key or PAT.

---

### AREA 10: Branch Preview & Dev Server

#### F10.1 — Start Dev Server from Worktree
**Current:** ✅ `WorktreeManager.StartDevServer()` exists but not exposed in UI.

**AC:**
- [ ] Completed card shows "Branch preview" button.
- [ ] Button starts dev server in worktree (`npm run dev` or configurable command).
- [ ] Server port auto-detected or configured per-board.
- [ ] UI opens the local URL in a new tab / iframe preview.
- [ ] Stop preview button kills the dev server process.

#### F10.2 — Diff View
**Current:** ❌ No diff view in dashboard.

**AC:**
- [ ] Card detail modal has "Diff" tab.
- [ ] Diff fetched via `git diff baseBranch..worktreeBranch`.
- [ ] Files listed with +/− stats; expandable per file.
- [ ] Syntax-highlighted diff rendering (or plain text with color classes).

---

### AREA 11: General Agent Chat

#### F11.1 — Workspace Chat
**Current:** ❌ Not implemented.

**AC:**
- [ ] "Chat" nav item or floating chat panel.
- [ ] General-purpose agent that knows the repo, tests, and git state.
- [ ] No worktree created; agent runs in-place (read-only or safe-write mode).
- [ ] Conversation history stored in new `chat_sessions` + `chat_messages` tables.
- [ ] Each message is a card-run-like event stream but without the kanban lifecycle.

---

### AREA 12: Recipes & Reusability

#### F12.1 — Recipe Library
**Current:** ❌ Not implemented.

**AC:**
- [ ] New `recipes` table: id, name, description, template_json, is_built_in, created_by.
- [ ] Recipe defines: card title template, description template, default persona, default model, start mode.
- [ ] Create-card modal shows recipe picker above templates.
- [ ] Built-in recipes: Bug fix, Feature, Refactor, Review, Spike, Security audit, Dependency update.
- [ ] Users can save a card as a custom recipe.

---

### AREA 13: Team & Collaboration (Cloud Tier)

#### F13.1 — Real-Time Presence
**Current:** ❌ Not implemented.

**AC:**
- [ ] WebSocket or SSE channel for board-level events.
- [ ] Show avatar cursors / names of users viewing the same board.
- [ ] "User X is editing this card" indicator.

#### F13.2 — Assignment & Notifications
**Current:** ❌ Cards have `assignee` field but no notification system.

**AC:**
- [ ] Cards can be assigned to users (not just agents).
- [ ] Assignment triggers notification via:
  - In-app toast
  - Slack webhook (configurable per board)
- [ ] Notifications table: id, user_id, type, message, read_at, created_at.

#### F13.3 — Audit Log
**Current:** ❌ Existing `audit` table but not used for kanban events.

**AC:**
- [ ] Every kanban mutation logged: card create/move/update, run dispatch/stop, decision answer, promotion.
- [ ] Audit log view in board settings.
- [ ] Filterable by user, action type, date range.

---

## Implementation Phases

### Phase 1: Decision Loop & Dashboard Polish (Foundation)
**Goal:** Make the core loop actually usable end-to-end.

1. **Wire DecisionDetector into Go driver** — emit `decision` events.
2. **Implement decision answering UI** — render options, POST answer, show awaiting state.
3. **Implement decision resume** — inject answer back into agent context (Go driver first).
4. **Run event stream in card detail** — Thread tab with live polling.
5. **Column management UI** — add/edit/delete columns from dashboard.
6. **Inline card editing** — title, description, priority, effort, assignee.
7. **Run number auto-increment** — populate `run_number` on dispatch.

### Phase 2: Autopilot & Cost Caps (Automation)
**Goal:** Let the board run itself.

1. **Autopilot service** — round-robin persona slots, backlog scan, card claiming.
2. **Autopilot dashboard UI** — config modal, live status panel, stop button.
3. **Self-evolving backlog** — agents spawn child cards via `spawn_card` event.
4. **Per-run & per-session budget caps** — enforce in `DispatchService`.
5. **Cost analytics dashboard** — today's total, session meter, per-card breakdown.
6. **QA mode** — run checks, dispatch fixes, convergence loop.

### Phase 3: GitHub, Sentry, MCP (Integrations)
**Goal:** Connect to external systems.

1. **GitHub bidirectional sync** — poll issues, sync status, populate `issue_number`.
2. **Draft PR flow** — UI button, store PR URL, link to card.
3. **Sentry import** — manual + auto-poll, deduplication, stack trace cards.
4. **MCP server** — stdio + SSE transport, all board tools exposed.
5. **Branch preview** — dev server start/stop, local URL open.
6. **Diff view** — git diff in card detail modal.

### Phase 4: Chat, Recipes, Team (Scale)
**Goal:** Collaboration and reusability.

1. **General agent chat** — floating panel, conversation history, no worktree.
2. **Recipe library** — built-in + custom recipes, recipe picker on create.
3. **Task templates + spec-first flow** — `/spec` run, approval gate, implementation.
4. **Real-time presence** — WebSocket, user cursors, editing indicators.
5. **Slack notifications** — webhook per board, assignment alerts.
6. **Audit log view** — board settings page with filterable history.

---

## Data Model Additions Needed

| Table / Column | Phase | Purpose |
|----------------|-------|---------|
| `board_cards.position` | 1 | Card reordering within columns |
| `board_cards.parent_id` | 2 | Parent-child card linking (autopilot spawns) |
| `boards.config_json` → `max_cost_per_run_usd`, `max_cost_session_usd` | 2 | Budget caps |
| `recipes` (new table) | 4 | Reusable task templates |
| `chat_sessions` + `chat_messages` (new tables) | 4 | General agent chat history |
| `notifications` (new table) | 4 | In-app + Slack notifications |
| `card_runs.run_number` | 1 | Already exists; populate it |

---

## Architecture Decisions

### Decision Resume Strategy
**Problem:** Claude Code and Codex are stateless CLI processes. You cannot "resume" them mid-conversation.

**Recommended approach:**
- **Go driver:** True resume — maintains `memory.Manager` session, injects answer, continues loop.
- **CLI drivers (claude-code, codex):** "Replay resume" — on decision answer, spawn a NEW process with the full conversation history + decision context prepended to the prompt. Mark the original run as `completed` (decision) and the new run as the continuation.

This is consistent with Kanbots' behavior and avoids trying to hack state into stateless CLIs.

### Event Streaming: Polling vs SSE
**Recommended:** Keep polling for Phase 1–2 (simple, works through proxies, no connection management). Add SSE as an optional optimization in Phase 3. The existing `SinceID` parameter already supports efficient incremental polling.

### Autopilot Slot Management
**Recommended:** Use a `sync.Map` or in-memory struct guarded by a mutex in `AutopilotService`. Slots claim cards atomically via `UPDATE board_cards SET status = 'running' WHERE status = 'queued' AND column_id IN (...) LIMIT 1 RETURNING id`. This prevents race conditions without complex distributed locking.

### GitHub Sync Polling
**Recommended:** Add a background goroutine in `cmd/opsintelligence` (similar to `StartGitHubAppConnector`) that polls GitHub Issues API on a ticker. Store `last_sync_at` on the board row to fetch only changed issues since last poll. Use ETag for efficient polling.

---

## Risk Register

| Risk | Impact | Mitigation |
|------|--------|------------|
| Claude Code / Codex CLI format changes | High | Parser falls back to plain text; monitor for breaking changes |
| Git worktree disk exhaustion | High | `ReapOldWorktrees` cron + per-board max worktree count |
| Decision resume for CLI drivers is complex | Medium | Accept "replay resume" pattern; document limitation |
| Autopilot cost overruns | High | Hard budget caps + pre-dispatch cost check |
| MCP server security | Medium | API key auth + rate limiting on MCP endpoints |
| Real-time presence scales poorly | Low | Defer to Phase 4; use simple polling first |
