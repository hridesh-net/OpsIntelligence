# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.60] — 2026-06-02

### Added — Repo Intelligence Wave 1: live CVE feed (OSV.dev) + filterable findings endpoint

First wave of the Repo Intel deep-scan enhancement. The scanner used to ask the LLM to recall CVEs from training data, which is unreliable and stale. Now it pre-queries the [OSV.dev](https://osv.dev) public vulnerability feed for every parsed dependency before going to the LLM. The LLM gets the real hits as ground truth and is asked to *explain* and *augment* them rather than rediscover them.

**New package** — `internal/repointel/cveclient/`
- `osv.go` — minimal client for the OSV `POST /v1/query` endpoint. No API key required. Maps `RepoMemory.PrimaryLang` to OSV ecosystem (Go, PyPI, npm, crates.io, RubyGems, Maven, NuGet, Packagist, Hex, Hackage, Pub). Normalises OSV's heterogenous severity (CVSS-v3 scores + GHSA `database_specific.severity` strings + free-text) into our four-tier `critical|high|medium|low` scale.

**Scanner integration** — `internal/repointel/scanner.go`
- `Scanner.preScanOSV` runs before the LLM call; failures are logged and swallowed so an OSV outage never blocks a scan (LLM-only path stays usable).
- LLM prompt now includes a `### Known vulnerabilities from OSV.dev (ground truth — already detected)` block listing every OSV hit, with explicit instructions to not duplicate them.
- `mergeCVEFindings` deduplicates: when the LLM rediscovers an OSV record we keep the OSV version since it carries verified metadata (references, fixed versions, CVE alias chain).

**Model changes** — `internal/repointel/model.go`
- `CVEFinding` gained `Source` (`osv`|`llm`), `References` (advisory URLs), `FixedVersions` (versions containing the patch), and `Ecosystem` fields. All optional; old persisted scans still load.

**New API** — `internal/gateway/repos_api.go`
- `GET /api/v1/repos/{id}/findings?severity=high&source=osv&type=cve` — flat, filterable view of the merged finding list (each query param is repeatable, any-match). Returns `{repo_id, scanned_at, findings[], total}`.

### Tradeoff notes
- OSV adds one HTTPS round-trip per dependency (12s timeout, 10 deps ≈ <2s typical). On flaky networks the scan degrades gracefully to LLM-only; you'll see `osv_cves=0` in the scan-complete log line.
- The next wave (v1.0.61) will add the LLM-harness call graph + transitive dependency tree.

## [1.0.59] — 2026-06-01

### Fixed — Scrun board scrolls again (vertical column scroll + horizontal board scroll)

User report on /boards: "I'm not able to scroll any of the page on any of the tab, neither the workflows nor the kanban board." Root cause was subtle flex-layout: every CSS Flexbox item defaults to `min-height: auto` (intrinsic content height). The Scrun shell stacks seven nested `flex: 1` containers — `.app → .main → .screen-wrap → .screen → .board-host → .board → .col` — and the column-flex links among them were free to expand to fit the tallest card pile, blowing past `.app { height: 100vh }`. Once `.app` grew taller than the viewport, `.col-body`'s `overflow-y: auto` no longer had a definite parent height and stopped scrolling; the body became scrollable instead but had no actual children below the fold, so wheel events went nowhere visible.

`internal/webui/dashboard/assets/scrun/scrun-bridge.css` now:

- Pins `body.scrun-active` to `overflow: hidden; height: 100vh` (with `!important` to defeat dashboard's `min-height: 100vh` on `.app-page`).
- Resets `min-height: 0` on every flex link from `.app` down to `.col-body`, so the column-flex chain stays inside its 100vh ceiling instead of stretching to fit content.

After this, the column card lists scroll vertically when content overflows and the board scrolls horizontally to reveal the off-screen DONE column.

## [1.0.58] — 2026-06-01

### Fixed — Repo scan no longer fails with "unexpected end of JSON input" on Gemini

User report from the TUI Repos view: two repos showed `scan error: scanner: LLM call: parseScanJSON: unexpected end of JSON input (raw: { "risk_…`. Root cause: `internal/repointel/scanner.go` and `internal/repointel/indexer.go` both set `MaxTokens: 2048`, but the scan schema covers `risk_level + summary + cves[] + bottlenecks[] + suggestions[]`. On a real repo with several CVEs and bottlenecks Gemini hits 2048 tokens mid-output, the JSON ends partway through a string, and `json.Unmarshal` rightly refuses to parse it.

Two fixes in this release:

- Both scan and index requests bumped to `MaxTokens: 8192`. That's still well under any model's context window and covers real-world JSON payloads with margin to spare.
- `scanWithLLM` now checks `resp.FinishReason == provider.FinishReasonLength` before parsing and returns a clear error (`LLM response truncated at token limit (finish_reason=length); raise MaxTokens or shrink prompt`) instead of a confusing JSON-parse trail-off. So if a future schema grows past 8192 too, the operator sees a self-explanatory message instead of having to grep `parseScanJSON`.

## [1.0.57] — 2026-06-01

### Fixed — Scrun topbar wraps cleanly, board scroll is reachable

User report: "I'm not able to scroll sideways or up/down to the tickets and top navbar components are congested." Both symptoms shared a root cause — the topbar's `.topbar { display: flex; gap: 14px }` had no `flex-wrap`, so at typical viewport widths the eight controls (board picker, search, three filter buttons, layout switcher, activity-rail toggle, theme switch, +New Task) overflowed horizontally. The overflow pushed `.main` wider than the viewport, which (a) made the topbar look cramped and (b) hijacked the page's horizontal scroll axis so the board's own `.board { overflow-x: auto }` could not be reached.

`internal/webui/dashboard/assets/scrun/scrun-bridge.css` now:

- Allows the topbar to wrap (`flex-wrap: wrap`, `height: auto`, `row-gap: 8px`) and lets every child shrink (`flex-shrink: 1; min-width: 0`).
- Pins the search input to `flex: 1 1 220px; min-width: 180px` so it stays usable but yields width to the filter buttons when there isn't enough room.
- Collapses the empty `#screenTitleWrap` on the board screen so it doesn't reserve dead width.
- Adds a visible 10px horizontal scrollbar styling on `.board` so the side-scroll affordance is obvious.
- Reinforces `min-height: 0` on `.col` and `.col-body` so vertical column scroll triggers reliably when cards overflow (some browsers stop propagating flex height through nested `flex: 1` containers without the explicit `min-height: 0`).

Net effect: the topbar reflows to two lines on narrow displays instead of crowding into one; the board horizontal scrollbar is reachable; each column's card list scrolls vertically when it has more cards than fit.

## [1.0.56] — 2026-06-01

### Fixed — Scrun board fills full viewport width

`internal/webui/dashboard/assets/style.css` caps `.content` at `max-width: 1160px` for normal dashboard pages, which is the right call there but left a ~460px blank strip on the right side of the Scrun board on wide displays — the user reported this as a "side blank cutout shrinking the UI." The v1.0.55 bridge CSS already reset padding and the grid track but did not touch max-width.

`scrun-bridge.css` now also forces `max-width: none` and `width: 100%` on `body.scrun-active .content`, so the board occupies the full remaining viewport. All six columns now fit (or scroll gracefully) without any dead space on the right.

## [1.0.55] — 2026-06-01

### Fixed — Scrun board fits the viewport; activity rail collapsed by default

User report on /boards: the leftmost column was clipped behind the rail and the rightmost column (DONE) was cut off the right edge, with a strip of dead space beyond it. Three causes, three fixes (all in `internal/webui/dashboard/assets/scrun/`):

- `app.js::STATE.showRail` flipped from `true` to `false`. The Agent Activity rail at 286px was open on first visit, leaving only ~1500px for six 306px columns — guaranteed to scroll-clip on a typical 14"–16" display. Default is now collapsed; the existing toggle (the activity icon in the topbar) opens it on demand and the choice persists in localStorage as before.
- `scrun-bridge.css` collapses the dashboard's `256px 1fr` grid track via both `body.app-page.scrun-active` and `body.scrun-active.app-page` (selector order doesn't matter) and adds two responsive column-width tiers — 248px under 1600px viewport and 220px under 1280px — so six columns actually fit at the widths users have, rather than spilling beyond the right edge.

The horizontal-scroll behavior on the board is unchanged; this just makes the default layout fit before users have to scroll.

## [1.0.54] — 2026-06-01

### Fixed — Scrun board opens in a new tab, defaults to light, theme toggle works

Three Scrun integration bugs from the v1.0.47 port:

- **Boards link replaced the dashboard chrome.** The sidebar "Boards" anchor (`assets/app.html`) now carries `target="_blank" rel="noopener"`, so opening a board pops a fresh tab and the dashboard nav stays intact in the original window.
- **Theme defaulted to dark and the switch was a no-op.** `assets/scrun/app.js::loadState` now defaults to `"light"` when nothing is in localStorage, and mirrors `data-theme` onto `document.body` (Scrun previously only set it on `<html>`). The CSS-scope rewrite in `assets/app.js::ensureScrunStyles` was upgraded from `:root\s*\{` to `:root(\[[^\]]+\])?\s*\{`, so the `:root[data-theme="light"]` override is rewritten to `body.scrun-active[data-theme="light"]`. Without those two changes the light tokens lived on `<html>` while the dark tokens lived on `<body>` and body always won the cascade — explaining why the toggle did nothing visible.
- **applyTheme keeps both attributes in sync** so clicking the sun/moon icon swaps both `<html>` and `<body>` `data-theme` in lock-step.

Column horizontal-scroll behavior is unchanged in code, but now reads correctly because the board no longer has to share viewport real estate with the original dashboard sidebar (boards open in their own tab).

## [1.0.53] — 2026-06-01

### Fixed — Installer no longer spams 404 retries when gzipped asset is missing

`install.sh` previously hit the `.gz` URL through `curl_get`, which has `--retry-all-errors --retry 3 --retry-delay 2`. On older releases (or any release predating the gzip workflow step), the asset 404s — and the retry loop turned a known-missing file into ~8 seconds of red `Warning: Problem (retrying all errors)` output before falling back to the raw binary. Now the installer probes the `.gz` URL with a single `curl -I` HEAD (no retries, 30s cap); only on HTTP 200 does it attempt the full download, otherwise it logs one line (`Gzipped asset unavailable (HTTP 404); using raw binary.`) and proceeds straight to the raw asset.

## [1.0.52] — 2026-05-31

### Fixed — Rust TUI builds with zero warnings

`crates/opsintel-tui/src/views/repos.rs::ReposView::state` was written in `new()` but never read, tripping the default `dead_code` lint during release builds. Marked it `#[allow(dead_code)]` to match the existing pattern on the sibling `last_error` field — the field is reserved for an upcoming state-driven refresh path, so removing it would just churn the struct.

## [1.0.51] — 2026-05-31

### Added — Scrun reads real boards/cards from `/api/v1` (read-only live mode)

The Scrun shell mounted in v1.0.47 was rendering bundled fixtures. v1.0.51 swaps that for a live API adapter so the board now reflects real backend state.

- **`scrun/api.js`** — thin client over `/api/v1/boards`, `/boards/{id}`, `/boards/{id}/agents`. Maps backend shapes (`BoardCard`, `BoardColumn`, `BoardAgent`) onto the structure the rest of the Scrun shell expects (`DB.CARDS`, `DB.WORKFLOW`, `DB.AGENTS`).
  - `card_type: feature|bug|infrastructure|security` → Scrun chips `feat|fix|infra|sec`
  - `priority: p0|p1|p2|p3` → Scrun chips `H|M|L`
  - `status: completed` → Scrun `done`
  - Per-column `gate` / `automation` lifted from `board.config.column_overrides`
  - Card metadata (`acceptance_criteria`, `labels`, `confidence`, `eta`, `hitl`, `branch`, `progress`, etc.) read from `BoardCard.metadata`
  - Agent colour/initials auto-derived when not present in `BoardAgent.config`
- **Board selection** — `loadFirstBoard()` lists boards, picks the last one the user opened (remembered via `localStorage.scrun.lastBoard`), or the first board if there's no remembered pick. The board name is mirrored into every `[data-boardname]` slot in the topbar / strip.
- **Mount flow** — `scrun/app.js`'s `init()` is now async; the API load happens before `bootApp()` so the first paint shows real data. The dashboard router `await`s `scrunMount()` before flipping `body.scrun-ready` so there's still no FOUC.
- **Graceful fallback** — if no boards exist, the API errors out, or the user toggled Demo mode (`localStorage.scrunDemoMode === "1"`), the shell keeps the bundled fixtures so it still renders something.

### Still missing vs target (next wave, v1.0.52)

- Write-back: drag / create / edit / dispatch still mutate `DB` in place; they don't hit `POST /cards/{id}/move`, `POST /cards`, `PUT /cards/{id}`, or `POST /cards/{id}/dispatch` yet.
- Run-event stream (SSE) for the Conversation tab.
- Analytics aggregation endpoint for the Analytics tab.
- Deletion of the legacy `renderBoardsView` / card-detail modal functions from `assets/app.js`.

## [1.0.50] — 2026-05-31

### Changed — install.sh prefers gzipped release asset (~70% smaller downloads)

The raw `opsintelligence-darwin-arm64` release asset weighs ~66 MB. On slow / flaky links curl was timing out at ~25% and burning the retry budget. v1.0.50 makes the release flow ship a `.gz` next to each raw binary and teaches `install.sh` to prefer it.

- **release.yml**: after each `go build`, a new step runs `gzip -9 -k -f` against every binary in `dist/`. Both the raw binary and the `.gz` are uploaded so older install scripts keep working.
- **install.sh**: tries `opsintelligence-<platform>.gz` first, decompresses, then chmod+install. Falls back to the raw binary transparently when the `.gz` isn't published (older tags). Reuses the existing curl retry / resume logic so partial `.gz` downloads still resume.
- Expected wire size: ~22 MB instead of ~66 MB for the macOS arm64 binary.

## [1.0.49] — 2026-05-31

### Fixed — install.sh pnpm step no longer needs sudo

The pnpm install step was running `npm install -g pnpm@10`, which on macOS targets `/usr/local/lib/node_modules` — a root-owned path on default Node installs. The install aborted with `EACCES: permission denied`.

- Prefer `corepack enable pnpm && corepack prepare pnpm@10 --activate` when corepack is present (default on Node 22). No sudo, no `/usr/local` writes.
- Fall back to `npm install -g --prefix=$HOME/.local pnpm@10` so the install stays user-scoped if corepack isn't available.
- If both fail, warn instead of aborting and skip the TypeScript layer build (the Go binary works on its own). User is told to `corepack enable pnpm` or `brew install pnpm` manually.

## [1.0.48] — 2026-05-30

### Fixed — Scrun shell layout polish

Follow-up to v1.0.47 closing the visible UX gaps in the embedded Scrun board:

- **Dashboard sidebar hidden on /boards.** Two nav rails side-by-side (dashboard's + Scrun's) was confusing. `body.scrun-active` now collapses the dashboard sidebar and re-grids `<body class="app-page">` to a single column, giving Scrun full bleed across the viewport.
- **Header padding removed.** Dashboard's `<main class="content">` keeps a top header + side padding by default; `body.scrun-active` zeroes both so Scrun's topbar lands flush against the viewport edge.
- **Flash-of-unstyled eliminated.** Scrun stylesheets fetch + parse asynchronously on first /boards visit. The mount + class flip now `await`s `ensureScrunStyles()` before showing the shell, and `#appRoot` stays `visibility: hidden` until `body.scrun-ready` is set.
- **Clean teardown.** Navigating away from /boards drops both `scrun-active` and `scrun-ready` so the dashboard returns to its normal sidebar + header layout.

### Still missing vs target (next wave)

- Live API adapter wiring scrun/data.js to `/api/v1/boards/{id}` (Demo mode still the only source of truth).
- `/api/v1/boards/{id}/analytics` aggregation endpoint.
- SSE `/api/v1/runs/{rid}/stream`.
- Removal of legacy `renderBoardsView` / card-detail modal code from `assets/app.js`.

## [1.0.47] — 2026-05-30

### Added — Scrun board UI mounted at `#/boards`

The standalone Scrun mockup (`/Scrun/`) is now embedded in the dashboard. Visiting **Boards** loads the full Scrun shell — nav rail, layout segmented (columns / compact / swimlanes), live activity rail, theme toggle, card detail panel with HITL gate, task create / agent config / workflow / analytics screens — instead of the legacy kanban list. Branding stays "Scrun" inside the boards section; the rest of the dashboard remains OpsIntelligence.

- Scrun JS/CSS copied to `internal/webui/dashboard/assets/scrun/` and shipped via the existing `go:embed` bundle. No build step.
- `app.html` inlines the Scrun shell HTML inside `#view-boards`. Scrun's setup wizard is hidden in embedded mode; the board exists on the server already.
- `app.js`'s `#/boards` route now calls `window.scrunMount()` (defined in `scrun/app.js`) instead of the legacy `renderBoardsView`. Legacy kanban code stays defined-but-unreachable; planned removal in v1.0.48.
- CSS isolation: Scrun stylesheets are fetched on first /boards visit, with their `:root { … }` token blocks rewritten to `body.scrun-active { … }` so the design tokens don't leak into Overview / Tasks / Settings.
- Demo mode by default. Cards are seeded from Scrun's bundled fixture (`data.js`). A live API adapter that reads `/api/v1/boards/{id}` and writes back via the existing endpoints lands in v1.0.48.

### Added — Backend extensions enabling the Scrun feature surface

- `GET /api/v1/workflow-presets` returns five preset templates (default / dev / research / support / ops) for the setup wizard.
- `POST /api/v1/boards` now accepts an atomic seed body: `preset` (slug) or explicit `columns[]` (each with optional `gate` and `automation`), plus `agents[]` to register the board's agent workforce in one call. Per-column gate/automation overrides are persisted under `board.config.column_overrides[<column_id>]` so no schema migration is required.
- `POST /api/v1/boards/{id}/columns` and `PUT /api/v1/boards/{id}/columns/{cid}` accept the same `gate` + `automation` fields, written through `setColumnOverride()`.
- `POST /api/v1/boards/{id}/cards` accepts `column_id`, `assignee`, and an arbitrary `metadata` map — the Scrun forms persist `acceptance_criteria`, `labels`, `confidence`, `eta_minutes`, `hitl` etc. on the existing `metadata_json` column.
- `PUT /api/v1/boards/{id}/cards/{cid}` merges incoming `metadata` into the card's existing metadata (set a key to `null` to remove it).

### Still missing vs target (next wave)

- Live API adapter wiring the Scrun UI to real boards / cards / runs (currently Demo-mode fixtures).
- `GET /api/v1/boards/{id}/analytics` aggregation endpoint for the Analytics screen.
- SSE `GET /api/v1/runs/{rid}/stream` for the conversation tab (Scrun panel currently polls).
- Removal of the legacy `renderBoardsView` / `refreshBoardView` / `openBoard` / card-detail modal functions from `assets/app.js`.

## [1.0.46] — 2026-05-30

### Added — Kanban / Agent Orchestration (kanbots.dev parity, wave 5 — closes UI)

Complete UI surface for every backend feature shipped in waves 1-4. The web dashboard now matches kanbots.dev pixel-by-pixel intent:

**Card detail modal — full rewrite (~350 lines)**:
- **Decision prompt banner** at the top of the card modal when any of its runs is awaiting an answer. Shows the question, every provided option as a button, plus a custom-answer input. Clicking ships `POST /runs/{rid}/decisions/{did}` and the agent resumes via the continuation dispatch path.
- **Per-run events viewer**: each run row gets an "events" button that loads `GET /runs/{rid}` and renders the last 200 events (text / tool_start / tool_end / decision / error) with phase tags and a refresh control. Tool calls and decisions get distinct visual treatment.
- **Stop-run button** inline on every running run.
- **Dispatch panel**:
  - Agent picker (populated from `GET /boards/{id}/agents`)
  - Persona picker
  - Slash command picker (`/spec`, `/review`, `/split`)
  - Custom model override field
- **Branch preview controls**: when a preview is running, shows status + clickable URL + stop button. When not running, shows a "dev-server cmd" input + start button.
- **Attachments pane**: list with download links + per-row delete + file upload widget.
- **Autopilot launch button** opens a dedicated modal (see below).

**Autopilot launch modal**:
- Tabbed UI for `feature-dev` and `qa` modes.
- feature-dev: multi-select persona list, parallelism (1-4), max cycles, session budget cap.
- qa: textarea for one-command-per-line check list, max fix attempts, budget cap.
- POSTs to `/api/v1/autopilot` and reports the session id.

**Autopilot sessions list modal** (board toolbar → "Autopilots"):
- Lists every running and completed session with mode, card id, cycle count, total cost.
- Inline stop button for running sessions.

**Sentry import modal** (board toolbar → "↻ Sentry"):
- Form for org slug, project slug, and Sentry search query. POSTs `/boards/{id}/sentry/import` and reports added/updated counts.

**Board toolbar enhancements**:
- "↻ GitHub" — triggers `POST /boards/{id}/github/sync`, reports added/updated.
- "↻ Sentry" — opens the Sentry import modal.
- "Autopilots" — opens the sessions list.
- **Per-board cost summary chip**: `N cards · M running · $X.XX spent`, updates on every refresh.

**Stylesheet additions** (~110 lines): modal-wide layout, decision-banner styling, run-event row styling per kind, autopilot tab UI, attachment / preview row layouts, board cost summary chip, badge for cost on the card meta line.

### Parity matrix vs kanbots.dev — final

| Capability | Status |
|---|---|
| All 19 capabilities from v1.0.45 | ✓ (backend) |
| Decision modal (UI) | ✓ |
| Run events stream (UI) | ✓ |
| Autopilot launch + sessions list (UI) | ✓ |
| Attachments upload/list/delete (UI) | ✓ |
| Branch preview start/stop (UI) | ✓ |
| GitHub sync button (UI) | ✓ |
| Sentry import (UI) | ✓ |
| Slash command picker (UI) | ✓ |
| Agent + persona + model picker (UI) | ✓ |
| Per-board cost summary (UI) | ✓ |

Kanbots.dev parity ✓ — backend + UI complete.

### Changed

- Crate version bumped to `0.2.19`.

## [1.0.45] — 2026-05-30

### Added — Kanban / Agent Orchestration (kanbots.dev parity, wave 4 — closes parity)

**Branch preview dev-server** — kanbots.dev's "branch preview: start worktree's dev server, open live URL":

- New `internal/kanban/preview/manager.go` — runs a dev-server process per card with an auto-picked free port; tracks status (`running`, `stopped`, `failed`) and surfaces the local URL.
- When the gateway is configured with Tailscale Funnel (`gateway.tailscale.mode=funnel`), the preview manager also runs `tailscale funnel <port>` and reports the public `*.ts.net` HTTPS URL on the preview record.
- One preview per card (must Stop before Start again); auto-reaped when the dev server crashes / exits.
- HTTP API:
  - `GET /api/v1/boards/{bid}/cards/{cid}/preview` — current preview (404 if none)
  - `POST /api/v1/boards/{bid}/cards/{cid}/preview` — `{cmd, port?}` to start
  - `DELETE /api/v1/boards/{bid}/cards/{cid}/preview` — stop
- CLI: `opsintelligence kanban preview start|get|stop`.

### kanbots.dev parity matrix — final status

| kanbots.dev feature | OpsIntelligence | Notes |
|---|---|---|
| Drag-to-move kanban (Backlog → Done + Inbox) | ✓ (API) | UI: drag-handler still pending |
| Per-card agent dispatch | ✓ | `POST /cards/{cid}/dispatch` |
| 11 supported CLIs | ✓ | claude-code, codex, gemini, cursor-agent, gh-copilot, opencode, amp, qwen, droid, ccr, acp |
| Git worktree isolation per run | ✓ | `<state_dir>/workspace/kanban/{cardID}-{runID}` |
| Pre-push hook blocking remote push | ✓ | Shell script returning exit 1 |
| Stream-JSON parsing | ✓ | ClaudeCodeDriver |
| Decision prompts (agent pauses, asks user) | ✓ | `PendingDecision` + resumption |
| Autopilot `feature-dev` mode | ✓ | Up to 4 parallel personas, round-robin |
| Autopilot `qa` mode | ✓ | Configurable checks + auto-fix dispatch |
| Sentry import → board | ✓ | Idempotent via `metadata.sentry_id` |
| Branch preview (dev server, live URL) | ✓ | Tailscale Funnel front when enabled |
| Promote to commit | ✓ | `worktree.PromoteToCommit()` |
| Draft PR | ✓ | `worktree.OpenDraftPR()` via `gh` |
| Slash commands (`/spec`, `/review`, `/split`) | ✓ | Prompt-template wrappers |
| MCP server exposing the board | ✓ | `kanban_*` tools via `RegisterKanbanTools` |
| Workspace modes (local sqlite / github issues) | ✓ | github mode: pull/push + comments |
| Per-run cost tracking | ✓ | `CostCalculator` + atomic `AddCost` |
| Budget caps | ✓ | per-card / per-board / per-run, board config |
| Attachments | ✓ | Up to 32 MB, on-disk per card |

### Changed

- `gateway.AuthService` grew `KanbanPreview *preview.Manager`.
- Crate version bumped to `0.2.18`.

## [1.0.44] — 2026-05-30

### Added — Kanban / Agent Orchestration (kanbots.dev parity, wave 3)

**File attachments on cards** — kanbots.dev's "drop a file onto a card" affordance:

- New `card_attachments` table (migration `0004_attachments.sql` for both sqlite + postgres).
- `datastore.CardAttachmentRepo` + sqlstore implementation.
- HTTP API:
  - `GET /api/v1/boards/{bid}/cards/{cid}/attachments` — list
  - `POST /api/v1/boards/{bid}/cards/{cid}/attachments` — multipart upload (field `file`, 32 MB cap)
  - `GET /api/v1/attachments/{id}` — download with proper `Content-Disposition`
  - `DELETE /api/v1/attachments/{id}` — remove row + on-disk file
- Files stored under `<state_dir>/workspace/kanban/attachments/<card_id>/<id>-<filename>`.
- CLI: `opsintelligence kanban attachments list|upload`.

**Sentry → kanban importer** — kanbots.dev's "Auto-pull error groups onto board; one click dispatches to agent":

- New `internal/kanban/sentry/import.go` adapter with `Importer.Import(boardID, org, project, query)`.
- Fetches Sentry issues via the REST API and upserts them as cards in the board's first column.
- Idempotent: cards are matched by `metadata.sentry_id`, so re-running refreshes title + description without duplicating.
- Card priority is derived from Sentry level (fatal/error → p1, warning → p2, info → p3); `card_type` set to `bug`.
- Card description embeds the culprit, the metadata value, and a permalink back to Sentry.
- HTTP endpoint: `POST /api/v1/boards/{id}/sentry/import` (`PermBoardsManage`).
- CLI: `opsintelligence kanban sentry-import --board <id> --org <slug> --project <slug> [--query is:unresolved]`.
- Wired automatically when a token is configured (`devops.sentry.token` in opsintelligence.yaml or `OPSINTELLIGENCE_SENTRY_TOKEN`).

**Config additions:**

- `devops.sentry.token` — Sentry Auth Token (scopes `event:read`, `project:read`).
- `devops.sentry.base_url` — defaults to `https://sentry.io`; override for self-hosted instances.

### Changed

- `gateway.AuthService` grew `AttachmentRoot string` and `KanbanSentry KanbanSentryImporter`.
- `datastore.Store` grew `CardAttachments() CardAttachmentRepo`.
- Crate version bumped to `0.2.17`.

### Still missing vs kanbots.dev (next wave)

- Branch preview public URL (tunneling)
- UI: drag-to-move, decision modal, cost dashboard

## [1.0.43] — 2026-05-30

### Added — Kanban / Agent Orchestration (kanbots.dev parity, wave 2)

**GitHub workspace mode** (`Board.Mode = "github"`):
- New `internal/kanban/githubmode/sync.go` adapter bridges board cards ↔ real GitHub issues for a per-board configured repo.
- `Sync.PullIssues` pulls open issues from `owner/repo` and upserts them as cards in the first column (Inbox). Existing cards (matched by `issue_number`) get title/body refreshed.
- `Sync.PushCardCreated` opens a new GitHub issue when a card is created in a github-mode board.
- `Sync.PushCardMoved` updates GitHub labels to `kanban/<column-slug>` + `type/<card-type>` + `priority/<priority>`; closes the issue when the card moves to a "Done"-style column.
- `Sync.PostRunComment` writes a Markdown summary (status, model, branch, elapsed, cost, error) to the GitHub issue after each agent run finishes.
- New GitHub-client methods `CreateIssue`, `SetIssueLabels`, `CloseIssue` in `internal/devops/github/github.go`.
- HTTP endpoint `POST /api/v1/boards/{id}/github/sync` (gated on `PermBoardsManage`) triggers a pull on demand.
- CLI: `opsintelligence kanban sync-github --board <id>`.
- Wired automatically when a GitHub token is configured (`devops.github.token` or `OPSINTELLIGENCE_GITHUB_TOKEN`).

**MCP server exposes the board** (`internal/mcp/kanban_adapter.go`):
- Read-only tools: `kanban_list_boards`, `kanban_get_board`, `kanban_list_cards`, `kanban_get_run`.
- Write-side tools (when a dispatch service is wired): `kanban_create_card`, `kanban_move_card`, `kanban_dispatch`, `kanban_stop_run`, `kanban_answer_decision`.
- `opsintelligence mcp` automatically registers the read-only tool set when a datastore is available, so external MCP clients (Claude Desktop, Cursor, Codex) can browse the board without an extra server.

### Changed

- `BoardCards.Create` HTTP handler now mirrors github-mode card creation to GitHub before responding; on partial failure it returns `{card, github_error}` so the operator can retry.
- `BoardCards.Move` HTTP handler now mirrors the column change to GitHub labels (best-effort; logged in the response on failure).
- Crate version bumped to `0.2.16`.

### Still missing vs kanbots.dev (next wave)

- Sentry import → board
- Branch preview public URL (tunneling)
- Attachments
- UI: drag-to-move, decision modal, cost dashboard

## [1.0.42] — 2026-05-30

### Added — Kanban / Agent Orchestration (kanbots.dev parity, wave 1)

Closes the largest gap against [kanbots.dev](https://www.kanbots.dev/). The previous kanban implementation already had drag-to-move boards + git-worktree-per-run + decision prompts + Claude Code / Codex drivers. This wave adds the orchestration layer.

**8 new agent CLI drivers** (registered alongside the existing Claude Code / Codex / in-process Go runner; bring the total to 11 — matching the kanbots.dev roster):

- `gemini` — Google Gemini CLI
- `cursor-agent` — Cursor agent CLI
- `gh-copilot` — GitHub Copilot CLI (`gh copilot suggest`)
- `opencode` — OpenCode CLI
- `amp` — Sourcegraph Amp CLI
- `qwen` — Qwen Code CLI
- `droid` — Factory Droid CLI
- `ccr` — Claude Code Router CLI (reuses Claude Code auth)
- `acp` — generic Agent Client Protocol driver for any ACP-compliant CLI

A new `GenericLineDriver` factors the common spawn / stdout / event-emit plumbing so adding a CLI is now ~15 lines of config.

**Autopilot**:
- `feature-dev` mode: round-robin a set of personas across up to 4 parallel slots on the same card. Stops on completion, operator stop, or session budget cap.
- `qa` mode: run a list of shell checks (typecheck / tests / lint / build / e2e) inside the card's worktree and dispatch fix-runs against each failure, up to `--max-fix-attempts` per check.
- New `kanban.Autopilot` type with in-memory session tracking, `Start{FeatureDev,QA}` / `Stop` / `Get` / `List`.

**Budget caps** (parsed from a board's `config_json` under the `budget` key):
- `per_card_usd` — refuses dispatch if a card has already accrued more.
- `per_board_usd` — refuses dispatch when summed cost across all the board's cards exceeds the cap.
- `per_run_usd` — enforced mid-stream by `runAgent`: cancels the agent process and finalizes the run as "stopped_budget" the moment the running cost crosses the ceiling.

**Slash commands** (`/spec`, `/review`, `/split`) — front-loaded into the prompt before it reaches the agent. Match kanbots.dev's templates:
- `/spec` asks the agent to write an implementation spec and stop before coding.
- `/review` asks the agent to inspect the branch's existing work and produce a go/no-go.
- `/split N` asks the agent to emit N independently-dispatchable subtasks as strict JSON.

**Decision-prompt resumption** — the long-standing "Phase 3 TODO" in `dispatch_service.go`. `AnswerDecision` now finalizes the paused run as `completed_paused`, then dispatches a follow-up that reuses the same worktree + branch with the question + answer + a "continue from where the previous run left off" directive baked into the prompt.

### Added — `opsintelligence kanban` CLI

Thin client over the gateway's `/api/v1/boards` + `/api/v1/autopilot` endpoints so the operator can drive the whole flow from a terminal:

```
opsintelligence kanban boards   list | create
opsintelligence kanban cards    list | add | move
opsintelligence kanban dispatch
opsintelligence kanban runs     stop | get
opsintelligence kanban autopilot start | list | stop
opsintelligence kanban agents   list
```

`dispatch` accepts `--slash spec|review|split` and `--slash-args` so slash commands flow end-to-end from the CLI.

### Added — `/api/v1/autopilot` HTTP API

- `GET /api/v1/autopilot` — list sessions
- `POST /api/v1/autopilot` — start (`mode=feature-dev|qa` + opts)
- `GET /api/v1/autopilot/{id}` — session detail (cycles, child runs, total cost)
- `POST /api/v1/autopilot/{id}/stop` — stop a running session

Permission-gated via existing RBAC (`PermRunsDispatch`, `PermRunsCancel`, `PermRunsRead`).

### Still missing vs kanbots.dev (next waves)

- GitHub workspace mode (Octokit-equivalent issue sync)
- Sentry import → board
- Branch preview public URL (tunneling)
- Attachments
- MCP server exposing the board (`kanbots-mcp-server` equivalent)
- UI: drag-to-move, decision modal, cost dashboard

### Changed

- Crate version bumped to `0.2.15`.

## [1.0.41] — 2026-05-30

### Fixed

- **Graph tab cursor jumped back to the first node every 3 seconds.** The 3s `reloadRegistry` tick called `unlockedRefreshSelectedContent`, which unconditionally reset `selectedNodeID = ""`. That wiped the user's `↑`/`↓` graph cursor on every snapshot push so navigation was effectively unusable. The cursor reset now lives in `setSelected` and only fires when the user actually switches *repos*; navigating within the call graph of the same repo is sticky.

### Changed

- Crate version bumped to `0.2.14`.

## [1.0.40] — 2026-05-30

### Fixed — Repos TUI showed "No repos configured" despite registry having entries

Same root cause as v1.0.38's Status fix, but on the repos snapshot path: Go's `scanJSON.{CVEs,Bottlenecks,Suggestions}` and `findingJSON.CVEIDs` are slices without `omitempty`, so when a repo's scan has no findings (the common case) Go emitted `"cves":null` / `"bottlenecks":null` / `"suggestions":null` / `"cve_ids":null`. Rust's matching `Vec<Finding>` / `Vec<String>` with bare `#[serde(default)]` rejected null, the nested `ScanResultView` deserialization failed, the parent `ReposSnapshot` deserialization failed, the snapshot was silently dropped, and the TUI fell back to the empty-default state — exactly the "No repos configured" symptom on a registry that `opsintelligence repos list` shows 3 entries for.

Comprehensive sweep on the Rust side: every `Vec<T>` field that comes from a Go-serialized payload now uses `deserialize_with = "null_as_empty_vec"`. 29 fields updated across `WizardPlan`, `WizardForm`, `WizardDone`, `WizardField{Select,MultiSelect}`, `ReposSnapshot`, `RepoMemoryView`, `ScanResultView`, `Finding`, `CallGraphView`, `DashboardSnapshot`, `DashboardStatus`, etc.

### Changed

- Crate version bumped to `0.2.13`.

## [1.0.39] — 2026-05-30

### Added

- **`OPSINTEL_TUI_DEBUG`** now also logs `repos.snapshot` builds: `[repos-snap] entries=N memory=BOOL scan=BOOL users=N`. Use to verify whether the registry-side load is actually populating the snapshot Go ships, when the TUI shows "No repos configured" but `opsintelligence repos list` shows entries.

### Changed

- Crate version bumped to `0.2.12`.

## [1.0.38] — 2026-05-29

### Fixed — the big "Status TUI shows STOPPED + empty Config/Limits/Usage" bug

Confirmed via the `OPSINTEL_TUI_PROTO_TRACE` capture: Go was sending JSON like `"channels":null` and `"agents":null` (the default `encoding/json` rendering of a nil slice). Rust's `Vec<T>` with `#[serde(default)]` rejects `null` — default only kicks in when the field is **missing**, not when it's present-but-null. That rejection cascaded through the whole `DashboardSnapshot` deserialization, the snapshot was silently dropped, and the TUI fell back to its all-default state: pill blank, `STOPPED` body, empty version/skills/CPU/RAM, **and** empty Config/Limits/Usage tabs.

Fixed on both sides so neither bites again:

- **Go (`internal/tuibridge/dashboard.go`)** — `buildStatus` now always emits an empty `[]string{}` for `channels` when the input is nil; `buildAgents` returns an empty `[]agentInfo{}` instead of nil when there are no tasks.
- **Rust (`crates/opsintel-tui/src/protocol.rs`)** — every `Vec<T>` snapshot field uses a new `null_as_empty_vec` deserializer that accepts both arrays and `null`, returning an empty Vec on null. Applied to `DashboardSnapshot.{config,limits,usage,agents,logs}`, `DashboardStatus.channels`, `ReposSnapshot.{entries,users}`.

### Changed

- Crate version bumped to `0.2.11`.

## [1.0.37] — 2026-05-29

### Added — Protocol diagnostics

- **`OPSINTEL_TUI_DEBUG=/path/to/log`** — when set, the bridge appends one line per `dashboard.snapshot` to the given file with `info.PID`, `info.Version`, `ps.alive`, `ps.cpu` so you can verify what the daemon-side cache actually sees.
- **`OPSINTEL_TUI_PROTO_TRACE=/path/to/log`** — when set, the bridge appends every outgoing JSON-RPC message to the file (prefixed with `→`). Useful for diffing actual wire data against what Rust deserializes.
- Both env vars are passive: they only fire when set, so no overhead on normal runs.

### Changed

- Crate version bumped to `0.2.10`.

## [1.0.36] — 2026-05-29

### Added — Realtime progress streaming for out-of-process Repo Intel TUI

- **`opsintelligence repos` now sees daemon progress in real time** even when running as a separate CLI process from the daemon. A 1s poller in the bridge re-reads `<memory_dir>/progress.json` (which the daemon's `Manager.writeProgressFile` already writes on every index/scan event) and merges new events into the snapshot. The Repos tab progress bars and percentages update live without needing the bridge to share an in-process `Manager`.

### Changed

- Crate version bumped to `0.2.9`.

## [1.0.35] — 2026-05-29

### Fixed

- **`opsintelligence tui-ping` failed in headless CI runners** (and elsewhere) because the Rust `--headless` mode read JSON from `stdin` while the Go bridge writes to fd 3 of the subprocess via `ExtraFiles`. The headless path now respects `OPSINTEL_TUI_PROTO_IN`/`OPSINTEL_TUI_PROTO_OUT` when set and falls back to stdin/stdout when not. CI smoke test passes again.

### Changed

- Crate version bumped to `0.2.8`.

## [1.0.34] — 2026-05-29

### Added — Repo Intelligence parity pass

Restores feature parity with the legacy Go `cmd/opsintelligence/tui/repos_tui.go`:

- **`/` search / filter on the Repos tab**. Matches case-insensitive substrings of name / language / index status / scan status / risk. The status pill switches to `matched/total repos` while a filter is active. `Esc` clears the query; `Enter` exits search mode but keeps the filter applied.
- **Vim keys**: `h`/`l` for left/right tab cycle, `j`/`k` for up/down, `g`/`G` for jump-to-top / jump-to-bottom, plus `Ctrl-U` / `Ctrl-D` for half-page scroll.
- **Graph tab is navigable now**. Go ships up to 200 nodes per snapshot; Rust shows the full list with a `►` cursor and `↑↓` (or `j`/`k`) moves between nodes. The selected node's callers + callees are rendered live, fed back over a new `repos.graph_select` notification.
- New `repos.graph_select` notification (Rust → Go) and `CallGraphView.selected_idx` / `CallGraphView.nodes` protocol fields.

### Changed

- Crate version bumped to `0.2.7`.

## [1.0.33] — 2026-05-29

### Added — Editable, persistable config from the Status dashboard

- **Inline config editor on the `status` TUI**. Press `e` on the Config or Limits tabs to edit any flagged row. Free-form values open a text input; boolean / enum rows open a vertical select. Hitting `⏎` ships the new value to Go which patches `opsintelligence.yaml` via the same `mergeOnboardYAML` path the wizard uses, so edits survive restart.
- **Editable rows** (this wave):
  - Config: `routing.default`, `enterprise`, `agent.planning.mode`, `agent.reflection`, `memory.mempalace.enabled`, `agent.local_intel.enabled`.
  - Limits: `agent.max_iterations`, `memory.working_token_budget`, `memory.mempalace.search_limit`, `gateway.max_websocket_clients`, `agent.subagent_tasks.max_concurrent`, `agent.subagent_tasks.retain_limit`, `agent.local_intel.max_tokens`, `agent.local_intel.smart_routing_max_tokens`.
- **Toast feedback** at the bottom of the dashboard reports save success (green) or failure (red) for ~5s after each edit, so the user knows the YAML was patched without leaving the TUI.
- **Type coercion** on save: bare integers stay integers, `true`/`false` become bools, floats stay floats, anything else is a string scalar — so the resulting YAML round-trips cleanly.

### Protocol additions

- `KeyValue` now carries `yaml_path` / `choices` / `hint` (all optional, backwards compatible).
- New `dashboard.edit` notification (Rust → Go) and `dashboard.edit_result` notification (Go → Rust).
- New `DashboardOptions.EditConfig` callback (Go side); the CLI's `status` command wires it to `mergeOnboardYAML + os.WriteFile`.

### Changed

- Crate version bumped to `0.2.6`.

## [1.0.32] — 2026-05-29

### Fixed — TUI correctness + diagnosability pass

- **Stale terminal content bled through every TUI screen**. `outer_block` and `panel_block` only painted their borders, not their interiors — so anything previously on the alt-screen (install-script stdout, prior commands, the OpsIntelligence startup banner) showed through any cell the body content didn't overwrite. Both helpers now fill with `theme::BACKGROUND` before drawing the border, so the screen is opaque end-to-end. Affects Dashboard, Repos, Doctor, Monitor, REPL, and Wizard views.
- **`opsintelligence status` showed blank `version` / `skills` and `0%` CPU / `0.0 MB` RAM when the daemon was stopped**. The renderer continued to emit the static rows even when `alive=false`, which made the screen look broken. The Status tab now prints an explicit "Daemon is not running. Start it with: `opsintelligence start`" hint above the empty rows when the orchestrator is down.
- **TUIs exited with a bare `error: EOF` when the user pressed `q`**. The clean Rust EOF on graceful shutdown was being treated as a failure. Added `Bridge.CloseErr()` that returns `nil` on `io.EOF` and the real error otherwise; all six view runners (dashboard, doctor, monitor, repl, repos, wizard) now use it.

### Added — TUI diagnosability

- **Rust panic hook**: any panic in the TUI renderer now tears the alt-screen down *before* the default panic handler runs, so the panic message reaches the user's real terminal instead of vanishing with the alt-screen. A breadcrumb is also written to `$OPSINTEL_TUI_LOG_DIR/opsintel-tui-panic.log`.
- **Hint on `enable_raw_mode` failure** (the cryptic "Device not configured (os error 6)") — the wrapper now prints a one-line explanation that stdin must be a TTY.
- **Protocol parse-error breadcrumbs** on both sides: previously, `serde_json::from_str` / `json.Unmarshal` failures were silently dropped, leaving the user with a blank Empty view. Both sides now surface the offending line.
- **Go bridge stderr breadcrumbs** when `cmd.Start` fails (spawn) or when the Rust subprocess exits with a non-zero code (read loop). Replaces what used to be silent return-to-shell.

### Changed

- Crate version bumped to `0.2.5`.

## [1.0.29] — 2026-05-28

### Added — Proper "Setup complete" page

- **Rich Done screen** at the end of `opsintelligence onboard`. Renders three sections inside the success panel:
  - **Hero** — `✓ Setup complete` headline + subline.
  - **Configuration** — aligned key/value list of config path, datastore, log path, and dashboard URL (computed from the user's final `gwHost`/`gwPort` choices).
  - **Next** — primary-coloured command list (`opsintelligence start`, `agent`, `status`, `skills marketplace`) with one-line descriptions.
- **`WizardOptions.BuildDone func() WizardDoneSpec`** for late-evaluated Done content. Runs after every step's `OnSubmit` has mutated state, so the summary reflects the user's actual choices (not the defaults captured at wizard construction). Static `DoneHeadline`/`DoneSubline`/`DoneSummary`/`DoneNext` fields remain available for callers that don't need late evaluation.
- **`WizardDone` / `WizardDonePair` / `WizardDoneSpec`** protocol additions on both sides — `protocol.rs` exposes the typed payload; `tuibridge/wizard.go` ships the same shape over JSON-RPC.

### Fixed

- **Done screen bleed-through.** Stale alt-screen content (install-script stdout, prior shell output) showed through the gaps in the previous Done panel because `Block` only paints its border, not its interior. Both the full frame and the panel block now set `bg(theme::BACKGROUND)`, so ratatui repaints every cell.

### Changed

- Crate version bumped to `0.2.4`.

## [1.0.28] — 2026-05-28

### Fixed

- **Wizard sidebar highlighted the wrong group on skipped sub-flows.** The plan-builder counted `formNum` for every form step in `opts.Steps`, including ones whose `Skip()` returned true at runtime (e.g. the 5 Secondary-Provider sub-flow steps when `secChoice="none"`). Plan `step_num` drifted ahead of the runtime counter, so on form 9 (`Gateway — Expose via Tailscale Funnel`) the sidebar lit up `Secondary Provider`. Plan-build now applies the same `Skip()` filter as the runtime loop, and `totalForms` (the `N / 32` header denominator) matches.

### Added

- `make tui` — convenience target that rebuilds the Rust subprocess for the host platform and the Go binary in one step. Equivalent to `make build-tui-host && make build-go` but discoverable in `make help`.

### Changed

- Crate version bumped to `0.2.3`.

## [1.0.27] — 2026-05-28

### Fixed

- **Release build failure** — `internal/tuibridge/wizard.go:199` redeclared `formNum` with `:=` after the sidebar-dedupe loop introduced the same name earlier in the same scope (`no new variables on left side of :=`). Changed the second occurrence to a plain reassignment. Affected every platform in the v1.0.26 release matrix.

### Changed

- Crate version bumped to `0.2.2`.

## [1.0.26] — 2026-05-28

### Fixed

- **CI / release build failure** — `crates/opsintel-tui/src/views/repl.rs` referenced an out-of-scope `inner` binding inside `render_chat`; corrected to `area.width`. Also dropped four `unused_imports` warnings (`Block`, `Borders`, `Alignment`, `WizardOption`) that hardened release builds.
- **Wizard sidebar showed duplicated step names** (e.g. `AI Provider` × 5, `Secondary Provider` × 5). Each provider sub-flow emits multiple form steps that share the same `Title`; Go now collapses consecutive same-titled steps into a single sidebar group and carries the 1-based form-step number at which the group activates. Rust sidebar highlights the correct group as the wizard advances.
- **Wizard appeared stuck on multi-field steps** (most reproducibly on `Gateway — Expose via Tailscale Funnel`). Old behavior was "Enter advances unless cursor is on the last interactive field" — pressing Enter on a leading `Select` silently moved focus to the next field with no obvious visual change. **Enter now always submits the form**; `Tab` / `↓` / `j` navigate between fields. Matches huh's UX expectation.

### Changed

- Crate version bumped to `0.2.1`.

## [1.0.25] — 2026-05-28

### Added — Wizard form engine + dashboard-style chrome

- **Generic wizard form engine** in Rust. The Go side ships a `WizardStep` slice (Input / Password / Select / MultiSelect / Confirm / Note / SideEffect / Note); the Rust subprocess renders the form, collects answers, and ships them back via JSON-RPC. Powers `onboard`, `quickstart`, and `skills configure`.
- **Mouse + keyboard interaction**: click to focus a field, click options to select, click Yes/No on confirm, scroll wheel on long forms, `Tab/↑↓/Enter/Esc` keyboard navigation.
- **Dashboard chrome unified across all views** (`status`, `repos tui`, `doctor`, `monitor`, `agent`, `onboard`):
  - Single outer frame with title `─ OpsIntelligence  ·  <Section> ─` embedded in the top border
  - Status pill in the top-right corner (`RUNNING` / `3 repos` / `PID 12345 · 02:14:33` / etc.)
  - Section panels with sharp single-line borders and section name in the title
  - Bottom command bar with key shorthand in colored pills (`⏎ Send  ⌃J Newline  …`)
- **Shared chrome widgets** in `crates/opsintel-tui/src/widgets/chrome.rs` (`outer_block`, `panel_block`, `render_pill`, `render_command_bar`) so future views are 3-line additions.
- **Sidebar step tracker** in the onboarding wizard (`wizard.plan` protocol message) — shows all steps with `●` completed / `►` active / `●` pending markers.

### Fixed

- **TUI failed to initialise input reader (EOF on launch)**. The Rust subprocess inherited a piped stdin from the Go bridge, so `crossterm::enable_raw_mode()` couldn't set termios and `event::read()` couldn't receive keyboard input. Fixed by switching the bridge to **inherit the controlling TTY for stdin/stdout/stderr** and using **extra file descriptors (fds 3 and 4)** for JSON-RPC. `cmd.ExtraFiles` in Go, `OPSINTEL_TUI_PROTO_IN=3` / `OPSINTEL_TUI_PROTO_OUT=4` env vars in the Rust child.
- **Duplicate splash headers** before the onboarding wizard. Removed `tui.PrintOnboardBanner` + `tui.PrintOnboardWelcomeSubtitle` calls so the user sees a single Rust alt-screen instead of two stacked banners.

### Changed

- **Rust crate version**: `0.1.0` → `0.2.0` (post-chrome-rework milestone).
- **CI/Release workflows** install Rust toolchain + `Cargo.toml`-matched target, build the embedded TUI binary per platform via `cargo build --release --target <triple>`, then run `go build` (which picks up the staged binary via `go:embed`). Windows uses `x86_64-pc-windows-msvc` for the Rust subprocess (no mingw needed on the Rust side).
- **CI clippy step is advisory** (`continue-on-error: true`) — surfaces warnings without gating PRs while the new crate stabilises.
- **`install.sh` source-build path** now requires `cargo` and builds the Rust TUI before `go build` runs. Clear error message points to https://rustup.rs.

## [1.0.24] — Rust TUI migration (initial)

### Changed — Rust TUI migration (complete)

Every interactive terminal UI has been rewritten in Rust using [`ratatui`](https://github.com/ratatui-org/ratatui) and runs as a subprocess of the Go binary. The Rust executable is built from `crates/opsintel-tui/` and embedded via `go:embed`; the Go core talks to it over newline-delimited JSON-RPC on stdio. From the user's perspective `opsintelligence` remains a single binary.

#### Ported interactive surfaces

| Command | Before | After |
|---|---|---|
| `agent` / `repl` | bubbletea + bubbles | `views/repl.rs` (streaming + markdown + tool calls) |
| `status` | bubbletea | `views/dashboard.rs` (6-tab snapshot view) |
| `repos tui` | bubbletea + huh + viewport | `views/repos.rs` (5 tabs + edit form) |
| `doctor` | bubbletea | `views/doctor.rs` (live check list) |
| `monitor` | bubbletea | `views/monitor.rs` (tailing event table) |
| `onboard` | 35 huh forms | `views/wizard.rs` (generic form engine) |
| `quickstart` | huh + bubbletea | wizard DSL |
| `skills configure` | huh forms | wizard DSL |

#### New infrastructure

- `internal/tuibridge/` — JSON-RPC bridge, `go:embed` of the Rust binary, sha256-hashed cache extraction, snapshot builders for each view, generic `WizardStep` DSL with 5 field types (Input/Password/Select/MultiSelect/Confirm/Note).
- `crates/opsintel-tui/` — Rust workspace with `views/{repl,dashboard,repos,doctor,monitor,wizard}.rs`, ported palette from `tui/theme.go`, shared widgets (text area with UTF-8 cursor, fenced-block markdown renderer, spinner).
- Hidden dev commands: `tui-ping` (Phase 1 smoke test), `tui-wizard-demo` (form-engine demo), `tui-onboard-preview` (sub-flow preview).

#### Dependency drop

`go.mod` no longer depends on `github.com/charmbracelet/huh`, `bubbles`, or `bubbletea`. Only `lipgloss` remains, used by a handful of non-interactive stdout helpers (`tui/banner.go`, `startup.go`, `theme.go`, `effects.go`, `guides.go`, `onboard_step.go`, `util.go`).

#### Files removed

`cmd/opsintelligence/tui/{repl,dashboard,repos_tui,doctor,monitor,status,setup,onboard_model,onboard_summary}.go` and `runOnboardingLegacy` / `collectProvider` / `collectProviderFiltered` / `ensureSelectValue` / `BuildOnboardSteps` / `providerSteps` dead-code branches — ~10,000 LOC of Go TUI deleted. Replaced by ~3,800 LOC of Rust.

#### Build & dev workflow

- `make build-tui-host` compiles the Rust binary for the host platform and stages it at `internal/tuibridge/assets/`. `make build-go` depends on it; the `go:embed` directive picks up the staged binary at compile time.
- `make build-tui` cross-builds all 5 release targets (`darwin-{arm64,amd64}`, `linux-{amd64,arm64}`, `windows-amd64`).
- `make tui-ping` round-trips a JSON-RPC ping through the embedded binary as an end-to-end smoke test.
- `--tui-dev-binary=PATH` (also `OPSINTEL_TUI_DEV_BINARY` env) overrides the embedded binary so iterating on the Rust side doesn't require a Go rebuild.

#### Migration impact

- One additional toolchain required: `rustup` (Rust 1.75+). CI matrix updated to cross-build the TUI before `go build`.
- Distribution unchanged from a user perspective — still a single `opsintelligence` binary that extracts the embedded Rust subprocess to `$XDG_CACHE_HOME/opsintelligence/tui/<hash>/` on first run.
- Onboarding behavioural change: the legacy tabbed bubbletea summary screen at the end of `opsintelligence onboard` is replaced with a plain-text summary printer. Run `opsintelligence status` for the live tabbed view of the merged config (now in Rust).

## [1.0.22] — 2026-05-24

### Fixed

- **Gemini provider registry name mismatch** (`internal/provider/gemini/gemini.go`): `Name()` returned `"google"` but the routing config and provider registry expected `"gemini"`, causing gateway startup to fail with `resolve model "gemini/gemini-2.5-flash": provider "gemini" not found`. Fixed by changing `Name()` to return `"gemini"` so the provider prefix matches routing exactly.
- **Dashboard unavailable on startup** (`internal/provider/gemini/gemini.go`): because the gateway failed to start due to the provider name mismatch, the dashboard at `/dashboard/` was completely unreachable. The fix above restores full gateway functionality including dashboard, API, and health endpoints.
- **PID lock race / stale launchd agent** (`cmd/opsintelligence/main.go`, `cmd/opsintelligence/service_darwin.go`): removed old `KeepAlive` launchd plist at `~/Library/LaunchAgents/com.opsintelligence.agent.plist` that was fighting with manual `opsintelligence start` invocations, causing PID file conflicts and stale process restarts.

## [1.0.21] — 2026-05-24

### Fixed

- **Gemini onboarding visibility** (`cmd/opsintelligence/onboard_steps.go`): the onboarding wizard (`RunOnboardWizard`) uses a separate provider list from the legacy sequential flow. Added "Google Gemini (AI Studio)" to the wizard's `allPrimary` / `allSecondary` lists, API key prompts, model picker, and config pre-population so it appears correctly in the TUI.

## [1.0.20] — 2026-05-24

### Added

- **Google Gemini (AI Studio) provider** (`internal/provider/gemini/`): simple API-key-only provider using the OpenAI-compatible Gemini endpoint (`generativelanguage.googleapis.com/v1beta/openai/`). No GCP project ID, location, or service account required — just an API key from https://aistudio.google.com/app/apikey.
  - `gemini.go` — thin wrapper around `openaicompat` with Gemini-specific defaults and model catalog.
  - `catalogs.GeminiModels()` — 6 models: `gemini-2.5-flash`, `gemini-2.5-pro`, `gemini-2.0-flash`, `gemini-2.0-flash-lite`, `gemini-1.5-flash`, `gemini-1.5-pro`.
  - Added `Gemini *ProviderCreds` to `config.ProvidersConfig`.
  - Registered in `registerProviders()` alongside existing providers.
- **Onboarding support for Gemini**: `opsintelligence onboard` now shows "Google Gemini (AI Studio)" as a primary/secondary provider option with a model picker and single API key prompt. The existing "Google Vertex AI (Gemini)" option remains for GCP-backed setups.
- **`.opsintelligence.yaml.example`**: added `gemini:` section with inline docs and the AI Studio link.

## [1.0.19] — 2026-05-24

### Added

- **Kanban Dispatch Service** (`internal/kanban/dispatch_service.go`): full card → agent → run orchestration. Replaces the gateway placeholder with a real async dispatcher that creates worktrees, spawns agents, streams events, and rolls up costs.
- **Git Worktree Manager** (`internal/kanban/worktree/manager.go`): per-run git isolation. Clones repos (or reuses local paths), creates worktrees from a base branch, and cleans up on completion. Supports branch auto-naming and promotion (push + comment).
- **Agent Driver Framework** (`internal/kanban/dispatcher/`): pluggable `AgentDriver` interface with three implementations:
  - `go_driver.go` — uses the internal Go LLM provider chain (`internal/provider/`) with streaming, token tracking, and tool-call event emission.
  - `claude_code_driver.go` — spawns `claude -p --output-format stream-json` in the worktree directory.
  - `codex_driver.go` — spawns `codex -q -f` in the worktree directory.
- **Cost Calculator** (`internal/kanban/cost/calculator.go`): per-model pricing table (Gemini, GPT-4o, Claude, DeepSeek, Groq, etc.) that computes USD cost from token-in/token-out counts on every run.
- **Decision Prompt Detection + Resume** (`internal/kanban/decisions.go`): scans agent output for questions, creates `PendingDecision` rows, and signals a waiting goroutine when the human answers via the API. Run status transitions `running → awaiting → running/completed`.
- **GitHub Bidirectional Sync** (`internal/kanban/github_sync.go`): `SyncBoard()` polls open GitHub issues and creates/updates kanban cards; `SyncCardToGitHub()` pushes card comments back as issue comments. Uses the existing `internal/devops/github` client (added `ListIssues()`).
- **Sentry Import** (`internal/kanban/sentry_import.go`): fetches error groups from the Sentry API and creates bug cards with inferred priority (`p0` for fatal/high-count, `p1` for error/moderate-count).
- **MCP Server** (`internal/kanban/mcp_server.go`): exposes the kanban board as 5 Model Context Protocol tools — `kanban_list_boards`, `kanban_get_board`, `kanban_list_cards`, `kanban_get_card`, `kanban_dispatch_card`.
- **Autopilot** (`internal/kanban/autopilot.go`): background round-robin loop that auto-dispatches queued cards to available personas every 30 seconds.
- **Gateway Wiring** (`internal/gateway/kanban_dispatcher.go`, `internal/gateway/kanban_api.go`, `cmd/opsintelligence/gateway_auth.go`, `cmd/opsintelligence/main.go`):
  - `KanbanDispatcher` interface on `AuthService` decouples gateway from kanban package.
  - `POST /api/v1/boards/{id}/cards/{cid}/dispatch` now calls `kanban.Service.Dispatch()` (creates run + worktree + goroutine).
  - `POST /api/v1/runs/{rid}/stop` now cancels the in-flight context and finalizes the run.
  - `POST /api/v1/runs/{rid}/decisions/{did}` now signals the waiting goroutine via `DecisionResume`.
  - `attachKanbanToGateway()` wires worktree manager + driver registry + dispatch service at boot.

## [1.0.18] — 2026-05-22

### Added

- **Multi-tenant GitHub App** (`internal/githubapp/`): any GitHub organisation can install a single registered GitHub App and have all their events routed to their own self-hosted OpsIntelligence instance. Core components:
  - `config.go` — operator-level config (`app_id`, `private_key_path`, `webhook_secret`, `public_url`).
  - `auth.go` — RS256 JWT signing and cached per-installation access tokens via the GitHub App API. Includes `VerifyInstallation` to confirm an installation belongs to the App before saving config.
  - `store.go` — `Installation` type and `InstallationRepo` interface for persisting per-org installation records.
  - `conntoken.go` — `ConnectToken` type and `ConnectTokenRepo` interface for one-time WebSocket authentication tokens.
  - `hub.go` — server-side WebSocket hub that tracks active client connections keyed by `installation_id` and pushes `EventEnvelope` messages; includes ping/pong keepalive and graceful disconnect handling.
  - `connector.go` — client-side outbound WebSocket connector (`Connector`) that dials the relay from the org's OpsIntelligence instance with automatic exponential-backoff reconnection; no public IP or inbound firewall rules required.
  - `relay.go` — HTTP relay that re-signs payloads with the org's configured webhook secret and forwards to their `/api/webhook/github` endpoint (fallback when no WebSocket is connected).
  - `handler.go` — HTTP handler mounting `POST /api/github-app/webhook` (GitHub event receiver), `GET/POST /api/github-app/setup` (post-install setup page), `GET /api/github-app/connect` (WebSocket upgrade endpoint), and `GET /api/github-app/installations` (JSON list for dashboard/CLI).
- **Event dispatch priority** (`internal/githubapp/handler.go`): events are delivered in order — (1) active WebSocket connection push, (2) HTTP relay to configured public endpoint, (3) local agent runner — so on-premise deployments with no public URL still receive all GitHub events.
- **Post-install setup page** (`internal/githubapp/handler.go`): after installing the GitHub App, org admins are redirected to a setup page that verifies the installation via the GitHub API, generates a unique connect token, and renders a ready-to-paste `github_app_connector:` YAML snippet with the relay URL, installation ID, and token.
- **`github_app_connector` config** (`internal/config/config.go`): new client-side config block enabling the outbound WebSocket connector on a self-hosted instance (`relay_url`, `installation_id`, `connect_token`, `reconnect_interval`).
- **`github_app` config** (`internal/config/config.go`): new relay-side config block (`app_id`, `private_key_path`, `private_key_pem`, `webhook_secret`, `public_url`, `github_api_url` for GitHub Enterprise Server).
- **`github_app_installations` and `github_app_connect_tokens` tables** (`internal/datastore/migrations/sqlite/0002_github_app.sql`, `…/postgres/0002_github_app.sql`): new migration adding installation registry and connect token tables to the ops-plane datastore.
- **`GitHubAppInstallations()` and `GitHubAppConnectTokens()` repo accessors** (`internal/datastore/repos.go`, `internal/datastore/sqlstore/`): SQL implementations of `InstallationRepo` and `ConnectTokenRepo` using the shared `sqlstore.Store`.
- **Gateway `GitHubApp` field** (`internal/gateway/server.go`): `Server` now accepts a `gitHubAppMounter` interface that is mounted during `Start()` at `/api/github-app/*`. Uses an interface to keep the gateway package free of a direct import on `internal/githubapp`.
- **`attachGitHubAppToGateway` and `StartGitHubAppConnector`** (`cmd/opsintelligence/gateway_auth.go`): wiring helpers called at gateway boot. `attachGitHubAppToGateway` loads the private key, opens the datastore, and mounts the handler. `StartGitHubAppConnector` starts the outbound WebSocket connector as a background goroutine.
- **`opsintelligence github-app` command group** (`cmd/opsintelligence/github_app_cmd.go`): CLI for managing installation records — `installations list`, `installations show <id>`, `installations set-endpoint <id> <url> [--webhook-secret]`, `installations clear-endpoint <id>`.
- **`.opsintelligence.yaml.example`**: documented `github_app:` (relay) and `github_app_connector:` (client) config sections with inline setup instructions.

## [1.0.12] — 2026-05-07

### Added

- **RAG Chat endpoint** (`internal/gateway/rag_chat_api.go`, `internal/gateway/server.go`): new `POST /api/rag-chat` streams LLM responses grounded in indexed repository knowledge. Searches the repointel hybrid FTS5 store first; falls back to loading repo memory JSON files directly when the store has no chunks yet (no embeddings required — keyword search only). Emits a `sources` SSE event with attribution metadata before the token stream so the UI can render source pills immediately. Same SSE wire format as `/api/chat`.
- **`RepoIntelAdapter` search helpers** (`internal/gateway/rag_chat_api.go`): added `SearchRepos`, `SearchRepo`, `LoadRepoMemory`, and `ListRepoIDs` methods to expose the repointel manager to the new RAG chat handler without coupling it directly to the manager type.
- **ChatGPT-style web UI** (`internal/webui/assets/index.html`, `app.js`, `style.css`): redesigned chat interface with a mode toggle (RAG Chat / Agent Chat), a repo selector sidebar that lists all indexed repos from `/api/v1/repos`, source attribution pills rendered below each assistant reply, and example prompt buttons on the empty state. RAG mode sends to `/api/rag-chat` with the selected repo IDs; Agent mode continues using `/api/chat`.

### Fixed

- **Hybrid FTS5 store always empty after indexing** (`internal/repointel/hybridstore.go`): `UpsertChunks` used `ON CONFLICT(chunk_id) DO UPDATE SET …` on the `vec0` virtual table. `sqlite-vec`'s `vec0` does not implement the upsert syntax — the error caused the entire transaction to roll back on every re-index, leaving both `repo_chunks` (metadata) and `fts_repo_chunks` (FTS5 keyword index) empty. Fixed by switching vec0 to DELETE + INSERT, matching the pattern already used for FTS5. Vec insert failures are now non-fatal so FTS5 search keeps working even when no embedding is provided.
- **Clone skipped silently for GitHub repos with token** (`internal/repointel/indexer.go`): `shouldUseClone` returned `false` for GitHub repos whenever a token was configured, falling back to the GitHub REST API and leaving `data/repointel/clones/` always empty. The correct default is to clone whenever `ClonesDir` is set — the token is embedded in the authenticated clone URL (`https://TOKEN@github.com/…`). Cloning is now the default; operators can opt out by setting `repo_intel.disable_clone: true`.

### Changed

- **`shouldUseClone` logic inverted** (`internal/repointel/indexer.go`): clone is now the default when `ClonesDir` is configured (including the layout default `data/repointel/clones`), regardless of platform or token. Previously only non-GitHub repos or token-less GitHub repos were cloned; now all repos are cloned unless the operator sets `repo_intel.disable_clone: true`.
- **`IndexerConfig.ForceClone` deprecated** (`internal/repointel/indexer.go`, `internal/config/config.go`): `force_clone` is now a no-op — cloning is already the default. The field is retained for backward compatibility and will be removed in a future release. Replace with `disable_clone: true` to revert to API-only behaviour.
- **`repo_intel.disable_clone` config field added** (`internal/config/config.go`, `cmd/opsintelligence/main.go`): explicit opt-out for git clone. When `true`, indexing uses the GitHub REST API for GitHub repos (non-GitHub platforms always clone regardless of this flag).

## [1.0.11] — 2026-05-06

### Added

- **Per-agent log directories** (`cmd/opsintelligence/tui/dashboard.go`, `internal/dirs/layout.go`): every agent type now writes to its own isolated directory under `logs/` — master → `logs/agent/`, sub-agents → `logs/subagents/<task-id>/`, specialist agents → `logs/subagents/<name>/<run-id>/`, repo intel → `logs/repointel/<repo-id>/`. The dashboard Logs tab scans the full directory tree on each tick using a `map[string]int64` per-file offset (zero CPU when idle) instead of two fixed paths.
- **Repointel trace events** (`internal/repointel/manager.go`): indexing and scanning now emit `task_start` / `task_done` trace events (via caller-supplied `TraceFunc` — no direct runtrace import in the manager), making repo intel activity visible in the dashboard Logs tab alongside agent turns.
- **PR review chain-of-thought pipeline** (`internal/tools/pr_review_chain.go`, `internal/tools/devops.go`): `/pr-review:` and `devops.github.review_pr` now route through `prompts/chains/pr-review.yaml` (gather → analyze → critique → render → post) when the prompt library is available. Injects methodology from `config/pr_review/methodology.md` and repo intelligence context; structured XML reasoning (`<plan>`, `<findings>`, `<critique>`) is private and stripped before the verdict is posted as a formal GitHub review with inline line-level comments.
- **`Runner.Cleanup()`** (`internal/agent/runner.go`): new method that calls `memory.Manager.DeleteSession()` to free working memory and episodic records after a short-lived runner (sub-agent, specialist) completes.

### Changed

- **Sub-agent and specialist trace isolation** (`internal/tools/subagent.go`, `cmd/opsintelligence/main.go`): each async sub-agent run gets its own `logs/subagents/<task-id>/runtrace.ndjson`; each specialist invocation gets a per-run UUID directory `logs/subagents/<name>/<run-id>/runtrace.ndjson` so parallel runs of the same agent type never interleave.
- **Default `MaxConcurrent = 2`** (`internal/subagents/tasks.go`): changed from 8 → 2 sub-agents running in parallel by default; override via `agent.subagent_tasks.max_concurrent` in config.
- **Dashboard Logs tab** (`cmd/opsintelligence/tui/dashboard.go`): `model_iteration` events now show `tools_offered` count and `skills_enabled` names; log entries carry a `Source` label derived from the file path (e.g. `master`, `sub:abc12345`, `repointel:github-Hmbown-DeepSeek-TUI`) visible in the ROLE/SOURCE column.

### Fixed

- **Working memory leak** (`internal/tools/subagent.go`, `internal/agents/orchestrator.go`): `Runner.Cleanup()` is now called after every sub-agent and specialist task completes, releasing working memory and episodic SQLite records that previously accumulated for the lifetime of the process.
- **Repo registry and memory paths** (`cmd/opsintelligence/main.go`, `cmd/opsintelligence/repos_cmd.go`): default `RegistryPath` and `MemoryDir` now resolve against `layout.RepoIntel` (`data/repointel/`) instead of the pre-migration `repointel/` root, preventing registry loss after the v0.4 layout migration.

## [1.0.10] — 2026-05-06

### Changed

- **Repo Intelligence TUI** (`cmd/opsintelligence/tui/repos_tui.go`, `cmd/opsintelligence/repos_cmd.go`): pressing **`s`** now **syncs immediately** — calls **`Manager.SyncRepo`** when the manager is embedded in the TUI, otherwise **`notifyRepoSyncViaGateway`** via optional **`OnSyncRequest`**. Default memory dir for the standalone repos TUI defaults to **`data/repointel/memory`** under **`state_dir`**.

## [1.0.9] — 2026-05-06

### Added

- **Repo Intelligence local git clones** (`internal/repointel/cloner.go`, layout + config): shallow **clone/cache** under state dir for indexing when API-only paths are insufficient; clone URL resolution (GitHub token, GitLab, Bitbucket).

### Changed

- **Indexing & full-repo index** (`internal/repointel/indexer.go`, `full_index.go`): improved tree/blob handling and integration with clone-backed sources.
- **Agent dashboard TUI** (`cmd/opsintelligence/tui/dashboard.go`) and **`repos` CLI** (`cmd/opsintelligence/repos_cmd.go`): repo intel / observability UX and gateway wiring refinements (`cmd/opsintelligence/main.go`, `internal/config/config.go`, `internal/dirs/layout.go`).

## [1.0.8] — 2026-05-06

### Fixed

- **Run trace dashboard** (`internal/webui/dashboard/assets/app.js`, `style.css`): treat zap-style JSON lines (`msg` + `level` / `caller`) as **`log`** with readable summaries instead of **`?`**; widen stream filter **`subagent`** to match **`specialist:`** child roles.
- **Run trace merge API** (`internal/gateway/runtrace_api.go`): when merging master + sub-agent NDJSON, **split the line budget across files** so a busy master file cannot squeeze sub-agent traces out of the tail; infer **`session_id`** hints (`cron:`, `subagent:`) for stream when **`runner_role`** is absent.
- **Run trace row hints** (`internal/webui/dashboard/assets/app.js`, `internal/agent/runner.go`): richer summaries for **`model_iteration`** / **`task_start`** (**`tools_offered`**, **`skills_cnt`**, **`local_intel`**, **`li_advisory`**, **`backend`**, routing); classify zap text via **`message`** as well as **`msg`**; **`model_iteration`** lines now include **skills** and **local intel** fields for parity with task start.

## [1.0.7] — 2026-05-06

### Fixed

- **Repo Intelligence TUI empty / stale list** (`cmd/opsintelligence/tui/repos_tui.go`): reload **`repos.yaml` from disk** on auto-refresh and manual **`r`** so rows match the gateway/agent; fix **selection vs search filter** (highlight used filtered-row index as full-list index).
- **`loadConfig` with nil logger** (`cmd/opsintelligence/main.go`): use **`zap.NewNop()`** when `log == nil` so paths like `repos tui` never panic if the YAML file is missing before onboarding.

## [1.0.6] — 2026-05-06

### Fixed

- **`repos add` / `repos sync` live enqueue with Tailscale Funnel** (`cmd/opsintelligence/repos_cmd.go`): try **`http://127.0.0.1:<gateway.port>`** before the public **`https://…ts.net`** origin so the CLI notifies a locally running gateway even when Funnel is not accepting connections on **443**.

## [1.0.5] — 2026-05-06

### Fixed

- **Release workflow Zig setup flakiness** (`.github/workflows/release.yml`): install Zig via **`mlugg/setup-zig`** (community mirrors, minisign verify, Actions cache) instead of a single-host **`curl`** from ziglang.org, which can reset mid-download.

## [1.0.4] — 2026-05-06

### Fixed

- **Agent REPL TUI log pollution + flicker** (`cmd/opsintelligence/main.go`, `internal/cron/cron.go`): route interactive agent/cron logs to file-backed run trace during Bubble Tea REPL mode so structured JSON lines no longer bleed into the alternate-screen terminal UI.
- **Release workflow Zig setup flakiness** (`.github/workflows/release.yml`): replace action-cache Zig setup with retrying direct download/install and upgrade `actions/checkout` to `@v5` to avoid Node 20 deprecation warnings.

## [1.0.3] — 2026-05-05

### Added

- **Canonical state directory layout** (`internal/dirs`): single `Layout` type for `data/`, `logs/`, `runtime/`, `config/`, skills, workspace, etc. **`dirs.Migrate`** moves existing flat `~/.opsintelligence` trees idempotently; **`runAgent`** runs migration then **`EnsureAll`**. **`Config.Dirs()`** and **`applyDefaults`** derive default paths from the layout.

### Changed

- **Bundled skill docs** (`skills/summarize/SKILL.md`, `skills/tmux/SKILL.md`): copy edits and clearer tmux guidance.
- **Embedded tsnet + Funnel without `TS_AUTHKEY`** (`internal/gateway/server.go`): when `bind` is tailscale/tailnet and `tailscale.mode` is **funnel** but **`TS_AUTHKEY`** is unset, fall back to **host** `tailscale funnel` on `0.0.0.0` so the gateway does not block on interactive tsnet login.

### Fixed

- **Onboarding channel multi-select** (`onboard_steps.go`): pre-select options that match already-chosen channels when revisiting the step.
- **Skills repair noise** (`internal/skills/loader.go`): skip skills with no **`opsintelligence.install`** block instead of surfacing a confusing repair failure.
- **Local intel missing native libs** (`internal/agent/runner_localintel.go`): log missing **libllama** / gollama at **Info** with a setup hint so the TUI status bar does not show a false error.

## [0.3.60] — 2026-05-01

### Fixed

- **`install.sh` downloads hanging indefinitely** (`install.sh`): `curl_get` now sets **`--max-time`** (default **300s** via `OPSINTELLIGENCE_CURL_MAX_TIME`), increases connect timeout to **30s**, and logs the **exact download URL** before fetching. When stderr is a TTY (typical `curl … | bash`), curl uses **`--progress-bar`** so progress is visible instead of a silent stall.

## [0.3.59] — 2026-05-01

### Fixed

- **Host `tailscale funnel`** (`internal/gateway/server.go`): Start funnel with **`cmd.Start()`** instead of `CombinedOutput()` — the CLI is long-running, so the previous code blocked forever, never recorded `hostFunnelPort`, and never logged the public webhook URLs. Kill the child on gateway shutdown; apply **`TAILSCALE_BE_CLI=1`** for the macOS `.app` CLI when spawning funnel/status/off.

### Changed

- **`install.sh`**: **`OPSINTELLIGENCE_CURL_RETRIES`** (default `3`) controls curl `--retry`; set to **`0`** for a single download attempt. Log a short note when retries are enabled so repeated curl messages are less confusing.

## [0.3.58] — 2026-05-01

### Added

- **Host Tailscale Funnel** (`internal/gateway/server.go`): When `gateway.bind` is loopback/LAN and `gateway.tailscale.mode: funnel`, the gateway runs **`tailscale funnel <port>`** on the host (same CLI resolution as onboarding: env, PATH, macOS `.app` bundle). Optional **`gateway.tailscale.reset_on_exit`** tears down funnel on shutdown. Logs public HTTPS URL and Teams/GitHub webhook paths once status is available.
- **Onboarding — Funnel without embedded tsnet** (`onboard_steps.go`): New step for loopback/LAN to enable host Funnel and capture the machine `*.ts.net` hostname; Teams setup omits standalone `listen_addr` when using embedded bind + Funnel and writes **`expose_via: gateway`**; YAML emits `tailscale.mode` when Funnel is chosen even without `bind: tailscale`.

### Changed

- **`PublicGatewayBaseURL`** (`internal/config/config.go`): Treat **`tailscale.mode: funnel`** with a real **`gateway.host`** as `https://…` for both embedded tsnet and host Funnel (not only when `bind` is tailscale); avoid treating loopback placeholders as Funnel origins.
- **Embedded tsnet startup logs** (`internal/gateway/server.go`): Retry MagicDNS suffix resolution for up to **60s** before logging failure.

### Fixed

- **`srv.Tailscale.ResetOnExit`** was never wired from config (`cmd/opsintelligence/main.go`).

## [0.3.57] — 2026-05-01

### Fixed

- **Tailscale dashboard URL** (`cmd/opsintelligence/onboard.go`, `main.go`, `repos_cmd.go`, `pr_reviews_cmd.go`): Status and CLI helpers now prefer **`https://opsintelligence.<MagicDNS-suffix>`** for embedded `gateway.bind` tailscale/tailnet, matching the tsnet listener hostname instead of `gateway.host` from YAML (which names the OS machine).
- **Tailscale CLI resolution** (`onboard.go`): Resolve binary via `OPSINTELLIGENCE_TAILSCALE_BIN`, `TAILSCALE_CLI`, `PATH`, then **`/Applications/Tailscale.app/Contents/MacOS/Tailscale`** on macOS; run bundled CLI with **`TAILSCALE_BE_CLI=1`** so `status --json` works when the GUI install omits PATH.

## [0.3.56] — 2026-05-01

### Fixed

- **Onboarding — Tailscale Funnel hostname** (`onboard.go`, `onboard_steps.go`): Introduce `placeholderGatewayHost` so loopback-style gateway hosts are replaced whenever `tailscale status --json` succeeds; require a real hostname when **Funnel** is selected; avoid emitting `https://127.0.0.1` webhook hints. Legacy sequential onboard gains the same hostname field and validation.

## [0.3.55] — 2026-04-30

### Added

- **Onboarding + Tailscale Funnel URLs** (`onboard.go`, `onboard_steps.go`, `onboard_summary.go`): Auto-detect Tailscale FQDN via `tailscale status --json`, optional hostname field on the Tailscale step, summary line for the public HTTPS origin, and post-onboard copy-paste lines for **Teams** (`…/teams/api/messages`) and **GitHub** (`…/api/webhook/github`) when Funnel is selected.
- **Gateway startup hints** (`internal/gateway/server.go`): After tsnet connects, log the resolved public URL and funnel webhook paths for Teams and GitHub.

### Changed

- **`PublicGatewayBaseURL`** (`internal/config/config.go`): For `gateway.bind` tailscale/tailnet with `tailscale.mode: funnel` and a non-empty `gateway.host`, return `https://…` without a port (Funnel terminates TLS on 443).
- **`resolvePublicGatewayURL`** (`cmd/opsintelligence/main.go`): When Funnel is active and `gateway.host` is empty, fall back to `tailscale status --json` so the dashboard shows a usable public base URL.
- **Gateway bind** (`internal/gateway/server.go`): Treat `bind: tailscale` the same as `tailnet` for embedded Tailscale (matches YAML written by onboarding).

## [0.3.54] — 2026-04-30

### Added

- **Microsoft Teams on the gateway** (`channels.teams.expose_via: gateway`): Mount the Bot Framework webhook on the OpsIntelligence gateway at `/teams/` (`/teams/api/messages`, `/teams/health`) so a single public HTTPS surface (for example Tailscale Funnel or a reverse proxy in front of the gateway) serves both the dashboard and Teams. Standalone mode remains the default when `expose_via` is empty or `standalone`.

### Changed

- **`internal/channels/msteams`**: Refactor HTTP routing into `buildMux`, `Handler`, and `GatewayHandler` so the same handler can run behind either a dedicated listener or the gateway.

### Fixed

- **Teams + `expose_via: gateway`**: Use one `*msteams.Channel` with `WithReliableOutbound` and register `channelSenders["msteams"]` so tool-based outbound matches inbound. Require `--serve` / `opsintelligence start` when using gateway exposure.

## [0.3.53] — 2026-04-30

### Fixed

- **Background autonomous runs** (`internal/agent/runner.go`): `HandleChatCommand` now forks the runner with `WithSession` before `go RunAutonomous`, so the background goroutine does not share per-turn scratch state (`localIntelScratch`, routing fields, audit hashes) with the interactive runner.

## [0.3.52] — 2026-04-30

### Added

- **Microsoft Teams inbound JWT verification** (`internal/channels/msteams/verify.go`, `msteams.go`, tests): Verify Bot Framework Bearer tokens (OpenID metadata → JWKS, RS256, issuer/audience/expiry) before handling activities. **`Channel.WithEmulatorMode()`** disables verification for the Bot Framework Emulator / local development.

## [0.3.51] — 2026-04-30

### Fixed

- **Repo Intel hybrid search without embedder** (`internal/repointel/manager.go`): Open `repointel.db` whenever embedding dimensions are known (default 1536). Keyword (FTS) search works without a configured embedder; vectors are optional for ranking and indexing.
- **Agent + sub-agent semantic RAG** (`internal/agent/runner.go`, `cmd/opsintelligence/main.go`, `internal/tools/subagent.go`): Wire `EmbedQuery` from the embeddings registry so prompt-time RAG and lessons use the same vectors as repo_intel mirroring, even when the **chat** provider cannot embed (e.g. Anthropic).
- **Palace prompt routing vs. repo_intel** (`internal/memory/palace_router.go`): Documents with `source_type: repo_intel` bypass heuristic palace filters so mirrored codebase chunks are not dropped from grounding context.
- **Repos TUI Graph tab** (`cmd/opsintelligence/tui/repos_tui.go`): Preserve call-graph list selection across periodic registry refresh (only reset cursor when the selected repo row changes).

### Changed

- **Skills registry concurrency** (`internal/skills/loader.go`): Protect the in-memory skill map with `sync.RWMutex` for safe concurrent reads/registration.
- **Git hygiene** (`.gitignore`): Ignore local dev binaries `opsintel_dev`, `opsintel_*`; remove accidentally tracked `opsintel_dev` from the repository.

## [0.3.50] — 2026-04-30

### Added

- **Full-repository hybrid index** (`internal/repointel/full_index.go`, `manager.go`, `indexer.go`, `hybridstore.go`): After the existing snapshot index, sync walks the GitHub recursive tree (bounded by config), fetches text blobs, chunks them as hybrid `source` kind, and embeds for scoped search. YAML under `repo_intel`: `full_index_disable`, `full_index_max_files`, `full_index_max_file_kb`, `full_index_chunk_runes`, `full_index_concurrency` (`internal/config/config.go`, `cmd/opsintelligence/main.go`).
- **Repo-scoped search API** (`internal/gateway/repos_api.go`): `POST /api/v1/repos/{id}/search` with JSON `{ "query", "limit" }` returns hybrid FTS + vector hits for that repo. Response may include `index_tree_truncated` when GitHub’s tree was truncated.
- **Dashboard “Ask repo” tab** (`app.js`, `style.css`): Question/keyword search against the indexed repo; surfaces tree-truncation warnings when applicable.
- **Semantic RAG mirror** (`internal/repointel/semantic_rag.go`): Full-index file chunks are mirrored into agent semantic memory alongside existing repo intel chunks.
- **GitHub tree truncation visibility** (`model.go`, `registry.go`, `full_index.go`, `manager.go`, `repos_api.go`, dashboard, `repos_tui.go`): Registry field `index_tree_truncated` persists the last full-index tree `truncated` flag so operators know search/RAG may be incomplete on huge trees.

### Changed

- **Sync pipeline** (`manager.go`, `repos_tui.go`): Progress steps extended to include full-tree index and semantic RAG mirror (8 steps total in the TUI).

## [0.3.49] — 2026-04-30

### Added

- **Repo Intel call graph policy** (`internal/config/config.go`, `internal/gateway/repos_api.go`, dashboard): `repo_intel.show_callgraph_library_packages` (default **false**). External package/module nodes and import edges stay out of the dashboard graph until operators opt in via Settings → Repo Intelligence or YAML. `GET /api/v1/repos/{id}` includes `show_callgraph_library_packages` for the UI. Settings schema documents `max_files_per_repo` and the new flag.

### Changed

- **Indexer file selection for graphs** (`internal/repointel/indexer.go`, `indexer_select_test.go`): Treat common source extensions (`.go`, `.ts`, …) as high-priority tier with entry points; within that tier prefer **larger** files so small config blobs no longer starve Go repos out of the snapshot (empty call graphs).
- **Dashboard call graph** (`app.js`, `style.css`): Stronger physics and stabilization, Fit / Re-layout controls, optional import edges when policy allows (busy graphs default to calls-only), toolbar copy when packages are disabled by policy.

## [0.3.48] — 2026-04-30

### Added

- **Repo Intelligence HTTP API** (`internal/gateway/repos_api.go`): `GET /api/v1/repos/{id}/callgraph` returns persisted call graph JSON; `GET /api/v1/repos/{id}/symbols` returns the symbol index written with each graph build.
- **Dashboard Repo Intel** (`internal/webui/dashboard/assets/app.js`, `app.html`, `style.css`): "Call graph" tab with embedded **vis-network** (vendored `vis-network.min.js`, no CDN). Repo detail loads full `GET /api/v1/repos/{id}` for description, timestamps, HEAD SHA, errors, and artifact filenames. Scan tab renders `cves`, `bottlenecks`, and `suggestions` plus scan summary (matches `ScanResult` JSON). Memory tab renders architecture, languages, key files, conventions, dependencies, test patterns, CI summary, review hints, common issues, and operator notes (matches `RepoMemory` JSON). Users tab reads `{ users: [...] }` and shows handle / role / email.

### Changed

- **Call graph build during every sync** (`internal/repointel/manager.go`, `callgraph.go`, `indexer.go`): If `RawFiles` is empty, refetches file snapshots via new `Indexer.FetchRawFiles` (no LLM). Always persists `*-callgraph.json`, `*-symbols.json`, and `*-callgraph.html` after a successful index path (including empty or import-only graphs). Import-only sources get `Kind: "file"` anchor nodes; import edges use dashed lines in HTML export. Default `MaxFilesPerRepo` when unset raised from 20 to 32 (`internal/repointel/indexer.go`, `internal/config/config.go`).

### Fixed

- **Dashboard Repo Intel showed almost no data** because the UI expected non-existent `scan.findings` and `memory.summary` / `memory.technologies` instead of the real API shapes.

## [0.3.47] — 2026-04-30

### Changed

- **Use terminal's native background everywhere** (`onboard_model.go`, `repos_tui.go`, `onboard_step.go`, `theme.go`): Removed all `WithWhitespaceBackground`, `Background(ColorBackground)`, `Background(ColorSurface)`, and `Background(ColorDashboardBg)` fills. The terminal's own background colour shows through at all times; text colours, borders and selection indicators provide the visual structure. Both light and dark terminal profiles are fully respected.

## [0.3.46] — 2026-04-30

### Fixed / Changed

- **Dark palette lifted off pure black** (`theme.go`): `ColorBackground` dark raised from `#141411` to `#1e1c1a`; `ColorSurface` to `#252321`; `ColorChromeBg` to `#302e2b`; `ColorDashboardBg` to `#2a2926`. Each level is now clearly distinguishable from the terminal's own `#000` and from each other, creating visible elevation hierarchy.
- **Onboarding wizard padding no longer bleeds black** (`onboard_model.go`): `padded` wrapper gains `Background(ColorBackground)` so its top/bottom/side spacing shows warm charcoal, not terminal-native pure black.
- **Form card elevated above page canvas** (`onboard_step.go`): `t.Form.Base` and `t.Focused.Base` now use `ColorSurface` (`#252321`) instead of `ColorBackground` (`#1e1c1a`), giving the form a visually-lifted card appearance that mirrors the website's card-on-surface pattern.

## [0.3.45] — 2026-04-30

### Changed

- **Repos dashboard visual overhaul** (`repos_tui.go`, `theme.go`):
  - Fill the entire alt-screen canvas with the warm-dark background (`ColorBackground`) via `lipgloss.Place + WithWhitespaceBackground` — terminal's own pure black no longer bleeds through between rendered elements.
  - Reduce orange to its single purposeful role: the `▶` selection cursor and progress-bar fill. All other elements (table headers, section headers, tab titles, selected-row text, dividers, borders) now use `ColorEmphasis` (cream/white) or `ColorOutlineVariant` to match the website's "minimal orange" philosophy.
  - `TabActive` changed from a solid orange background pill to bold orange text only — the website uses orange as one tiny accent dot, not as a fill colour.
  - `DashboardDivider` changed from `ColorAccentLavender` (orange) to `ColorOutlineVariant` (subtle grey).

## [0.3.44] — 2026-04-30

### Fixed

- **Onboarding wizard blank body** (`onboard_model.go`): When a form step completed, `activateStep()` set `m.form` to the next form and then the caller immediately overwrote it with `m.form = nil`, leaving every subsequent step with no form to render (black body area). Fixed by clearing `m.form = nil` *before* calling `activateStep()`. Also injects a synthetic `tea.WindowSizeMsg` with the current terminal dimensions into the freshly created form so it renders at the correct size immediately, without waiting for a terminal resize event.

## [0.3.43] — 2026-04-30

### Added

- **Adaptive TUI theme**: Migrated all TUI color constants to `lipgloss.AdaptiveColor` pairs aligned with the AssistClaw High-End Tech design system (cream/neutral surfaces for light terminals, warm dark shell palette for dark terminals). Semantic color tokens (`ColorBackground`, `ColorSurface`, `ColorOnSurface`, `ColorBrandAccent`, `ColorRiskCritical`, `ColorPatchOK`, `ColorWarn`, etc.) replace every hardcoded hex and ANSI escape in `theme.go`, `effects.go`, `doctor.go`, `repos_tui.go`, and `repl.go`.
- **Unified `huh` form theme**: `applyOpsHuhTheme` consolidates all `huh.Theme` field styling (form base, group title/description, focused/blurred inputs, buttons, selectors, error indicators) so both `OnboardTheme` and `setupTheme` share the same adaptive palette.
- **Onboarding wizard shell** (`onboard_model.go`): New bubbletea alt-screen model (`RunOnboardWizard`) with a persistent progress header (brand gradient, orange pill `N/M`, step title/subtitle, divider) that stays anchored across every form. Supports form steps (`MakeForm` factory, lazy-called at step activation) and side-effect steps (background goroutine + spinner).
- **`BuildOnboardSteps`** (`onboard_steps.go`): Converts the entire `runOnboarding` sequential flow into `~40` typed `OnboardWizardStep` values — primary/secondary provider sub-steps (select → Bedrock auth → credentials → model → custom model), Plano smart-routing, embeddings, local Gemma download, MemPalace install, gateway, per-channel credential forms (Telegram, Discord, Slack, WhatsApp, Teams), skills marketplace with custom-path install, DevOps/webhook config, config merge/save, and login service registration.
- **Full-bleed alt-screen canvas**: `lipgloss.Place` + `WithWhitespaceBackground(ColorBackground)` fills the entire terminal viewport so the host terminal default background never shows through during onboarding.

### Fixed

- **Config API secret redaction** (`internal/gateway/config_api.go`): Added `omitSecretFields` post-processor that strips known secret YAML field names (`api_key`, `token`, `bot_token`, `dsn`, `credentials`, etc.) from the JSON response for principals without `secrets:read`, preventing field-name disclosure even when values are empty.
- **`TestConfigGet_RedactsSecretsWithoutSecretsRead`**: Tightened assertion from `"api_key"` (substring that falsely matched the non-secret `"api_keys"` config struct) to `"api_key":` (exact JSON key with colon); corrected `"legacy-shared-token"` (wrong hyphen) to `"legacy_shared_token"` (correct yaml underscore tag).

## [0.3.42] — 2026-04-30

### Added

- **Architecture diagrams**: `architecture-overview.drawio`, `architecture-memory-flow.drawio` (runtime + ingest tabs), `architecture-memory.drawio`, and `architecture-connectivity.drawio` in the repo root for docs and reviews.
- **Draw.io agent skill**: `skills/drawio-skill/` (SKILL.md, styles, references, assets) for consistent diagram generation aligned with project conventions.

## [0.3.41] — 2026-04-30

### Added

- **Dashboard repo drill-down**: Clicking a repository row on `#/repos` opens a detail view with scan results, index memory, and users tabs, plus sync actions.

### Changed

- **Onboarding progress**: `opsintelligence onboard` now prints one full-width overall progress line per step (no duplicate mini-bar in the step header) and a final 100% line after configuration is saved.
- **Gateway config JSON**: Config API responses round-trip through YAML so JSON keys match `yaml` tags (snake_case) instead of Go struct field names.
- **Gateway auth**: Static gateway Bearer token is honored for API calls when session auth is enabled, with a system principal for RBAC and audit.
- **Repos API**: Repo routes use `RawPath` so repo IDs containing slashes stay correctly encoded; sync errors map to 404 vs 500 more accurately.

## [0.3.38] — 2026-04-28

### Added

- **Repo progress API**: Added `GET /api/v1/repos/progress` to expose persisted cross-process RepoIntel progress state for shared runtime visibility.
- **Queue reliability regression tests**: Added coverage for live sync notify path and registry/manager reload behavior across multi-process updates.

### Changed

- **Direct live enqueue from CLI**: `repos add` and `repos sync` now attempt immediate notify of the running RepoIntel manager via gateway sync endpoint, with durable file-backed queue fallback.
- **Manager cross-process consistency**: RepoIntel manager now reloads registry state during pending/monitor scans and sync operations, preventing stale in-memory views from leaving jobs stuck in `pending` until restart.
- **Runtime mode visibility**: Repo TUI context strip now shows whether it is operating in `live` manager mode or `file-backed` mode.

## [0.3.37] — 2026-04-28

### Added

- **RepoIntel dashboard surface**: Added a dedicated `#/repos` dashboard view that lists configured repositories with live index/scan status, risk, user counts, and one-click sync actions.
- **RepoIntel settings in dashboard**: Added `repo_intel` to the settings UI and config API wiring so YAML-backed RepoIntel values are visible/editable from the web dashboard.

### Changed

- **Repo TUI progress visibility**: Repo TUI now loads `progress.json` on startup and derives fallback progress rows from registry status (`indexing`/`scanning`) so progress bars and context-strip updates remain visible even when explicit step events are sparse.

## [0.3.36] — 2026-04-28

### Added

- **Hybrid repo search backend**: Added `HybridStore` (FTS5 + sqlite-vec with reciprocal-rank fusion), structured chunking, call graph extraction, and markdown reference generation for richer RepoIntel retrieval.

### Changed

- **RepoIntel manager orchestration**: Extended manager/indexer/registry flow for hybrid memory persistence, improved synchronization bookkeeping, and more robust repo processing lifecycle behavior.
- **Repo intelligence UX surfaces**: Updated `repos` command handling and Repo TUI workflows to better support large indexed memory/search interactions.

## [0.3.35] — 2026-04-28

### Added

- **RepoIntel semantic memory store**: Added sqlite-vec backed vector storage for per-repo memory documents, enabling semantic similarity search across indexed repositories.

### Changed

- **RepoIntel manager processing loop**: Manager now enqueues pending repos on startup, periodically polls for newly pending repos, and monitors indexed repositories for HEAD SHA changes to auto-trigger re-index/re-scan.
- **Indexer API surface**: Added lightweight current HEAD SHA lookup used by the monitor loop for change detection.

## [0.3.34] — 2026-04-27

### Added

- **Repo URL input normalization**: `repos add/sync/status/remove` now accept common GitHub/GitLab URL formats (HTTPS/SSH, optional `.git`) and normalize to `owner/name`.

### Changed

- **Repo learning UX defaults**: Onboarding now enables `repo_intel` by default, and `repos add` auto-queues initial index/scan so first-repo setup works without manual config edits.
- **Repo command self-heal behavior**: `repos add` and `repos sync` auto-enable `repo_intel` when disabled and print an explicit one-time restart hint for already-running agents.
- **Pending status guidance**: `repos status` now surfaces a clear hint when jobs remain pending because `repo_intel.enabled` is off.
- **CLI/TUI polish**: Updated REPL/setup terminal surfaces to improve onboarding and interactive setup flow consistency.

## [0.3.33] — 2026-04-27

### Added

- **Enterprise roadmap docs**: Added `doc/enterprise_devops_agent_roadmap.md` with a prioritized governance-first rollout and gap-to-surface mapping for autonomous DevOps deployments.
- **Policy fingerprinting**: Added security policy bundle hashing (`POLICIES.md` + `teams/**/*.md`) to support stronger audit correlation.
- **MS Teams channel adapter baseline**: Added the initial Microsoft Teams channel adapter scaffolding and related inbound/outbound channel utilities.

### Changed

- **Bounded autonomy controls**: Added `agent.autonomy.max_tool_calls_per_turn` and enforced per-turn tool-call budgets in master and sub-agent runners.
- **Tool-call audit metadata**: Extended security audit records with model and policy-bundle metadata while preserving hash-chain verification compatibility for legacy entries.

## [0.3.32] — 2026-04-24

### Fixed

- **doctor**: If stdin or stdout is not a TTY (CI, pipes, `go test` subprocesses), print plain-text check output instead of the Bubble Tea dashboard, which failed with `could not open a new TTY`.

## [0.3.31] — 2026-04-24

### Fixed

- **PR review fetch**: After a successful GitHub fetch, `doFetch` no longer returned `fmt.Errorf("%w", nil)` (a mistaken attempt to thread `CommitSHA` through the error return). That non-nil error formatted as `%!w(<nil>)` and caused PR review pool tasks to fail immediately after "fetching diff".

## [0.3.23] — 2026-04-22

### Added
- **MemPalace Onboarding**: Added optional MemPalace setup to the `onboard` flow, including automatic Python venv creation and package installation.
- **Onboarding UX**: Integrated terminal spinners for long-running onboarding tasks such as downloading/copying the Gemma GGUF model and installing MemPalace.

### Changed
- **TUI Refactor**: Exported spinner and MemPalace setup primitives to allow reuse across CLI onboarding and quickstart commands.

## [0.3.22] — 2026-04-22

### Added
- **A2A Task Management**: Implemented task store in the gateway server to track, poll (`tasks/get`), and cancel (`tasks/cancel`) streaming Agent-to-Agent requests.
- **Fact Check Tool**: Added the `fact_check` tool to the core agent catalog for verifying information accuracy.

### Changed
- **Streaming Contexts**: Updated the agent runner to support task-scoped cancellation via `context.Context` in `RunStream`, enabling clean interruption of long-running agent tasks.

## [0.3.21] — 2026-04-21

### Fixed
- **Agent Pre-authorization**: Updated `runner.go` and `runner_auto.go` core system prompts. The agent is now explicitly pre-authorized to use `devops.github.review_pr` and `devops.github.submit_review` when in autonomous mode or explicitly tasked with posting a review, resolving issues where the agent would hesitate and ask for write confirmation during automated PR reviews.
- **PR Review Pool Skip Handling**: Updated `PRReviewCmdHandler` to correctly parse the JSON `{"skipped": true}` response returned when a draft PR is skipped. The background pool now surfaces a clean "PR Review skipped" message in the chat instead of printing raw JSON.

## [0.3.20] — 2026-04-21

### Fixed
- **PR Review Drafts**: Draft PRs no longer cause a hard task failure in the background pool. They now return cleanly as `skipped` to ensure tasks complete successfully while logging the reason.
- **Configurable Draft Reviews**: Added `devops.github.allow_draft_review` to `opsintelligence.yaml` (default: `false`). Set to `true` to force reviews on draft PRs.
- **Inline Comments Limitation**: Fixed an issue where the agent would incorrectly claim it "cannot post inline comments" due to a lack of explicit instructions. The `pr-review` skill prompt now explicitly confirms this capability and instructs the agent to diagnose HTTP 403 (insufficient PAT scope) instead of giving up.

## [0.3.19] — 2026-04-20

### Added

- **Configurable PR Review Pool**: The number of parallel PR review sub-agents is now controlled by `devops.github.pr_review_workers` in the config (default 4). Tasks beyond the limit queue and are picked up automatically when a slot frees.
- **Pool monitoring in agent system prompt**: The master agent's ambient context now shows active PR reviews (task ID, status, elapsed, last event) every turn via a system prompt augmentor — oversight is automatic, not polled.
- **Agent monitoring tools**: `pr_review_tasks`, `pr_review_cancel`, and `pr_review_events` are now registered in the agent tool registry when GitHub is configured, enabling the agent to list, inspect, and cancel review tasks directly.
- **Gateway REST API**: `GET /api/v1/pr-reviews`, `GET /api/v1/pr-reviews/{id}/events?since=N`, and `POST /api/v1/pr-reviews/{id}/cancel` — all auth-gated, JSON responses.
- **CLI subcommand**: `opsintelligence pr-reviews [list|events|cancel]` hits the running gateway to list tasks, stream event logs, or cancel reviews without opening a browser.

### Fixed

- `pr_review_tools.go`: fixed a pre-existing compile error where `b.WriteString` was erroneously called with three arguments (a strconv leftover).

## [0.3.18] — 2026-04-20

### Added

- **Agent Tool Graph Updates**: Exposed the new `devops.github.review_pr` and `devops.github.submit_review` tools to the agent's intent-routing graph (`tool_graph.go`). The execution graph now perfectly prioritizes the single-call `review_pr` approach, falls back to `submit_review` if needed, and reserves `pr_comment` for simple chat messaging.
- **CLI Support**: Updated `opsintelligence tools` list with detailed explanations for the three distinct PR commenting tools.

## [0.3.17] — 2026-04-20

### Added

- **Deep PR Review Integration**: Added new prompt libraries (`prompts/pr-review/post.md`) and dedicated PR tools (`devops_review_pr.go`) to significantly expand the autonomous agent's context and capabilities for performing line-by-line PR reviews. 
- Integrated these capabilities directly into the core GitHub client (`internal/devops/github/github.go`) allowing reviews to securely function on private/org repositories without checking out code manually.

## [0.3.16] — 2026-04-20

### Changed

- **PR Review Skills:** Updated `pr-review.md` and `gh-pr-review/SKILL.md` to instruct the agent to use the native `devops.github.submit_review` tool instead of attempting to look for a local environment checkout. This ensures the assistant correctly posts line-by-line comments directly to GitHub using the diff provided via API.

## [0.3.15] — 2026-04-20

### Fixed

- **GitHub PR tools:** correctly parse `owner/repo` from the `repo` parameter instead of erroneously falling back to `defaultOrg`, preventing `404 Not Found` API errors when agents pass the full repository path.

## [0.3.13] — 2026-04-20

### Added

- **GitHub PR conversation comments from chat:** new `devops.github.pr_comment` tool posts Markdown comments to pull request conversation threads using the configured `devops.github` PAT (no `gh` binary required on channel/webhook hosts).
- **Tool graph coverage for PR review intent:** `devops.github.pr_comment` is now seeded/connected as a companion after `pull_request` and `pr_diff` evidence steps so agents can publish feedback when asked.

### Changed

- **DevOps guidance and templates:** `skills/devops/*`, webhook docs, README posture text, and the default SOUL template now document the narrow `pr_comment` write path while keeping heavier writes (approve/merge/deploy) behind explicit human confirmation.
- **GitHub client posture docs:** package comments now describe read-mostly behavior with explicit narrow write support for PR/issue conversation comments.

### Fixed

- **Regression in PR feedback automation:** agents no longer need to fall back to "cannot comment from this interface" when no host `gh` is available; they can post conversation comments through the native devops GitHub tool surface.

## [0.3.12] — 2026-04-19

### Added

- **LocalIntel smart routing (`agent.local_intel.smart_routing`):** optional on-device Gemma pass suggests tool names and a short skill-focus line before the cloud model; hints merge ahead of ToolGraph catalog selection (still capped by provider `MaxTools`). Sub-agents inherit the same `local_intel` config.
- **Onboarding:** when Local Intel is enabled, generated YAML documents `smart_routing: false` with a one-line comment.

### Changed

- **LocalIntel gating:** advisory scratch, smart routing, stream fallback, and run-trace `local_intel_enabled` / backend labels use **`localIntelPresent()`** (enabled **and** engine loaded). If GGUF/runtime is missing, LocalIntel no-ops even when `enabled: true`.

### Fixed

- **Bedrock:** clamp large system text, message text, tool results, and tool descriptions before Converse/ConverseStream to avoid `ValidationException` / request body length limits; surface `stream.Err()` after streaming events.
- **Agent model stream:** up to one trimmed retry on recoverable errors, then optional **LocalIntel** stream fallback when the on-device engine is present.
- **Autonomous runs:** same LocalIntel scratch + smart routing prep and stream resilience as interactive runs.

## [0.3.11] — 2026-04-19

### Changed

- **Onboarding / `skills configure`:** Skills and DevOps steps use huh group titles and descriptions; keyboard hints live on the multiselect field instead of a separate printed header (avoids stale “skills” copy above later forms on small terminals).
- **DevOps onboarding form:** Short per-field guidance for Jenkins and SonarQube URLs and tokens.

### Fixed

- **Managed MemPalace:** `mempalace init` is invoked with `--yes` so `opsintelligence mempalace setup` and `memory.mempalace.managed_venv` complete without a TTY (avoids upstream `EOFError` on room approval when stdin is closed).

## [0.3.10] — 2026-04-19

### Added

- **Enterprise posture (binary installs):** `agent.enterprise`, `agent.subagent_tasks`, `gateway.max_websocket_clients`; planning defaults when enterprise is on; `doc/enterprise-binary-server.md`.
- **`opsintelligence guides github`:** TUI cheat sheet for GitHub PAT vs webhook HMAC vs `gh` PR reviews.
- **Dashboard setup guides:** Gateway, DevOps, and Webhooks settings pages show short “where does this credential go” asides; settings landing summarizes GitHub setup paths.

### Changed

- **TUI theme:** Replaced lime-green primary palette with slate + blue/cyan accents; success indicators use cyan instead of green.
- **GitHub webhooks:** Example YAML and `doc/github-webhooks.md` document CodeRabbit-style PR review flow (`pr-review` + `gh api`); default adapter prompt mentions posting reviews.
- **`pr-review` render:** Walkthrough plus collapsible Major / Minor / Nitpick sections for GitHub-flavored Markdown.

### Fixed

- **WebSocket hub:** Cap concurrent clients when `max_websocket_clients` is set; synchronous register ack before starting pumps.

## [0.3.9] — 2026-04-19

### Added

- **Pseudo-XML tool calls:** if the model emits blocks like `<function=devops.github.list_prs><parameter=owner>…</parameter></function>` instead of native tool JSON, the runner parses them, strips the markup from assistant text, and executes the tools (same spirit as the existing markdown `bash` fallback). Covered by unit tests.

### Changed

- **Messaging channels (e.g. WhatsApp):** model tokens are buffered for the turn and sent once at the end so pseudo-tool markup is not streamed into the chat; CLI / in-app surfaces that stream internally are unchanged.
- **Dashboard → Run trace:** each event shows a one-line summary (kind, iteration, tool, chain, skills, query preview, errors) above the pretty-printed JSON.

### Fixed

- **Agent loop:** a turn with tool calls inferred from XML/markdown no longer stops early only because the API reported `stop` without native tool parts (so PR-style flows can run tools and continue).
- **Run trace:** `tool_call` and `tool_done` events include **`iteration`** so they align with `model_iteration` in the tail file.

## [0.3.8] — 2026-04-19

### Changed

- **Dashboard → Run trace:** **Auto-refresh (10s)** is enabled by default when opening the tab (still cleared when navigating away; can be turned off).

### Fixed

- **Dashboard → Run trace:** render each tail line whether the API returns JSON strings or already-parsed objects (avoids `"[object Object]" is not valid JSON`).
- **Bedrock (Converse / ConverseStream):** map registry tool names to AWS-safe names (`[a-zA-Z0-9_-]+`) and back, so dotted tools such as `devops.github.pull_request` no longer trigger `ValidationException` on history replay or new turns.

## [0.3.7] — 2026-04-19

### Added

- **Dashboard → Run trace:** tail master / sub-agent NDJSON via **Run trace** in the sidebar (`GET /api/v1/runtrace`, permission **`run_trace.read`** on built-in roles except viewer). Gateway **Bind mode** help documents **lan** for Tailscale/LAN access to `/dashboard/`.

### Changed

- **`opsintelligence status`:** when stopped, prints dashboard URL, `curl /health`, SSH port-forward hint, and `tail -f` for the run trace file; live status view shows the same hints when running.

### Fixed

- **Gateway `GET /`:** when phase-2 auth is enabled, redirect to `/dashboard/`; legacy root uses `io.ReadSeeker` for embedded `index.html` (avoids empty responses on `/` while `/health` still works).

## [0.3.6] — 2026-04-18

### Added

- **Run trace (NDJSON) defaults on:** `agent.run_trace_mode` defaults to `auto`, so an unset `run_trace_file` resolves to `logs/runtrace.ndjson` under `state_dir` for every agent entry point (CLI, gateway, channels, webhooks, sub-agents). Disable with `run_trace_mode: off` or `OPSINTELLIGENCE_RUN_TRACE_MODE` / `OPSINTELLIGENCE_RUN_TRACE=0`. Optional path overrides: `OPSINTELLIGENCE_RUN_TRACE_FILE`, `OPSINTELLIGENCE_RUN_TRACE_SUBAGENT_FILE`.
- **`internal/observability/runtrace`:** context-scoped trace path (`WithOutputPath`) so `chain_run` logs to the same file as the active runner; optional dedicated `agent.run_trace_subagent_file` for sub-agents.
- **`task_start` / `model_iteration`:** `run_trace_mode`, `routing_intents` (tool-graph keyword alignment), `skills_context_chars`, `runner_role` (master vs sub-agent). `task_done` now records `finish` for `stop`, `max_iterations`, and `error` (including stream failures).
- **Smart prompts:** `smart_prompts.extra_source_dirs` and `OPSINTELLIGENCE_SMART_PROMPTS_EXTRA` merge extra prompt roots; CLI opens library paths from config.
- **Onboarding YAML merge:** selective replace vs deep merge (`onboard_merge.go`) so re-onboard does not wipe unrelated keys; optional DevOps and GitHub webhook steps.

### Changed

- **PR review path:** `devops.github.pull_request` tool; `pr-review/gather` uses injected PR JSON/diff; chain steps remain tool-free in the prompt runner; skills and default webhook prompt updated accordingly.
- **Tool graph:** `ClassifyIntents` returns sorted, de-duplicated labels for tracing and graph seeds.

### Fixed

- **GitHub webhook default prompt:** raw string no longer embeds nested backticks that broke compilation (`internal/webhookadapter/github/adapter.go`).

## [0.3.5] — 2026-04-17

### Fixed

- **Linux arm64 release binary:** Zig/musl cross-builds with **`opsintelligence_localgemma`** panic at startup (`jupiterrider/ffi` / `purego.Dlopen` → *Dynamic loading not supported*). Official **`linux-arm64`** artifacts are now built with **`fts5` only** (no in-process Gemma in that tarball); other targets unchanged.

## [0.3.4] — 2026-04-17

### Fixed

- **GitHub Releases / Gemma:** GitHub caps each release asset at **2 GiB** ([REST API](https://docs.github.com/rest/releases/assets)); the default Q4_K_M GGUF is ~**3 GiB**, so attaching `gemma-4-e2b-it.gguf` fails. Release CI now ships **`gemma-4-e2b-it-MIRROR_MANIFEST.txt`** (HF URLs) instead of the binary. **`internal/localintel`** uses Hugging Face mirrors only.

## [0.3.3] — 2026-04-17

### Added

- **Bundled skill `mastering-aws-cli`:** AWS CLI v2 quick-reference (`skills/aws-cli-main`) listed in `skills/marketplace.json`.

### Fixed

- **Onboarding local Gemma:** no confirm/path prompts; auto-provisions from bundled `models/*.gguf` or public mirrors (no credentials). Copy/download errors print a warning and **do not** fail onboarding.
- **Local Gemma GGUF bootstrap:** Hugging Face mirrors (Unsloth → bartowski Q4_K_M). *(v0.3.4: GitHub cannot host the GGUF; see release manifest.)*

## [0.3.2] — 2026-04-17

### Fixed

- **Onboarding wizard compile and runtime correctness:** `runOnboarding` now returns `(bool, error)` so `opsintelligence onboard` can branch on `shouldStart` and detach correctly; DevOps YAML placeholders are declared; login service install uses `OPSINTELLIGENCE_STATE_DIR`; copy references `OPSINTELLIGENCE_LOCAL_GEMMA_GGUF` in the Gemma path hint.

## [0.3.1] — 2026-04-18

### Fixed

- **MemPalace managed `mempalace init`:** create the `mempalace/world` directory under state dir before invoking the
  upstream CLI. Some mempalace versions error with "Directory not found" when the path does not
  exist yet (`opsintelligence mempalace setup` / `memory.mempalace.managed_venv`).

## [0.3.0] — 2026-04-17

### Added

- **AssistClaw-equivalent messaging channels.** Telegram, Discord, Slack, and WhatsApp adapters are
  wired into the daemon (reliable outbound for Telegram/Discord/Slack; WhatsApp session DB under
  `state_dir`). Configuration matches AssistClaw (`channels.telegram`, `channels.discord`,
  `channels.slack`, `channels.whatsapp`). Onboarding includes an AssistClaw-style **Messaging
  channels** multi-select and per-channel forms; DevOps API integrations are a separate step.

## [0.2.6] — 2026-04-17

### Fixed

- **Onboarding matches AssistClaw-style opt-in configuration.** The wizard now asks whether to
  configure provider API keys and connection details (skip and edit YAML later), shows an API key
  field only when the chosen provider typically needs one, and uses a **multi-select** for Slack /
  GitHub / GitLab / Jenkins / SonarQube so only chosen integrations prompt for credentials (empty
  selection skips all).

## [0.2.5] — 2026-04-17

### Fixed

- **Onboarding TUI matches AssistClaw-style sequential screens.** Each onboarding step now runs as
  its own `huh.NewForm` (`Run()` per group) with alt-screen, instead of one form containing every
  group. Provider-specific pages (OpenRouter, Azure OpenAI, Bedrock, Vertex) appear only when
  relevant. This removes any sense of stacked or overlapping steps.

## [0.2.4] — 2026-04-17

### Fixed

- **Onboarding TUI now behaves as true step-by-step pages instead of one dense mixed surface.**
  The provider and integration wizard was technically in alt-screen, but it still felt visually
  cluttered because all provider-specific fields were part of a giant single flow.
  Onboarding now uses titled, paged groups with conditional visibility:
  - Provider selection
  - Provider credentials
  - OpenRouter options (only when provider is `openrouter`)
  - Azure OpenAI options (only for `azure_openai`)
  - AWS Bedrock options (only for `bedrock`)
  - Vertex options (only for `vertex`)
  - Slack
  - Integrations overview + per-integration pages
  - Team policy

  This resolves the “I can see next interfaces with first one” experience and makes
  the flow read like one clean interface at a time.

## [0.2.3] — 2026-04-17

### Fixed

- **Onboarding TUI now uses an isolated alternate screen.** `opsintelligence onboard`
  now runs the `huh` form with `tea.WithAltScreen()`, preventing visual bleed with
  regular terminal scrollback and fixing the “previous interface still visible”
  behaviour while navigating or scrolling.

### Added

- **Missing provider options restored in onboarding.** The interactive wizard now
  includes `azure_openai`, `bedrock`, `vertex`, and `voyage` in addition to the
  previously-added providers. Provider-specific optional fields were added so the
  generated YAML can capture real-world enterprise configs:
  - Azure OpenAI: `api_version`
  - Bedrock: `region`, `profile`, `access_key_id`, `secret_access_key`
  - Vertex: `project_id`, `location`, `credentials`
  - Voyage: `api_key`, optional `base_url`, `default_model`

## [0.2.2] — 2026-04-17

### Added

- **Onboarding now exposes the full provider surface instead of only
  OpenAI/Anthropic.** `opsintelligence onboard` now offers the same
  rich provider choices already supported by the runtime registry:
  OpenAI, Anthropic, Groq, Mistral, Together, OpenRouter, NVIDIA,
  Cohere, DeepSeek, Perplexity, xAI, HuggingFace, Ollama, vLLM,
  and LM Studio. The wizard also captures optional `base_url`,
  `default_model`, and OpenRouter attribution fields so users can
  configure modern provider stacks without editing YAML by hand.

### Changed

- **PR review output style now matches rich inline-review formatting.**
  The PR-review render prompt and `gh-pr-review` comment templates now
  emit structured findings in the style:
  `⚠️ Potential issue | Critical/High/Low` + `Impact:` +
  `Suggested fix:` (with GitHub `suggestion` blocks when present).
  This aligns OpsIntelligence review comments with the visual and
  triage-friendly format used in your reference screenshot.

## [0.2.1] — 2026-04-17

### Fixed

- **GitHub Actions release workflow published empty assets.** The
  `release` job ran `cp bin-archives/* dist/` but `dist/` was only
  created when `GEMMA_GGUF_SOURCE_URL` was set. With the variable
  empty, `cp` failed into a missing directory; `|| true` hid the
  failure and `softprops/action-gh-release` uploaded nothing — hence
  `404` on `releases/latest/download/opsintelligence-darwin-arm64`.
  A **Stage binaries for release** step now always `mkdir -p dist`
  before copying, lists `dist/`, and **fails the job** if no
  `dist/opsintelligence*` files exist.

- **`install.sh` bootstraps Go from go.dev when neither a release
  binary nor a system `go` is available.** The v0.2.0 behaviour
  still required a pre-installed Go for the automatic source-build
  fallback after a GitHub `404`, which blocked machines that only
  had `curl` + `git`. The installer now downloads the official Go
  tarball for the detected OS/arch (same layout as go.dev/dl),
  extracts it to a temp dir, runs `go build`, then removes the
  toolchain. Opt out with `OPSINTELLIGENCE_SKIP_GO_BOOTSTRAP=1`.
  Override the version with `OPSINTELLIGENCE_BOOTSTRAP_GO_VERSION`
  (default tracks `go.mod`, currently 1.26.2). macOS builds that
  fail with cgo errors still point at `xcode-select --install`.

### Documentation

- **README — "Installing on a client or locked-down machine".** Step-by-
  step guidance for deployments where the host is not the operator's
  own workstation: prefer pinned GitHub release binaries, optional
  `NO_SOURCE_FALLBACK=1` + `OPSINTELLIGENCE_SKIP_GO_BOOTSTRAP=1` for
  strict binary-only policy, prerequisites when IT allows a source
  build, and manual copy-the-binary as an alternative to `install.sh`.

## [0.2.0] — 2026-04-17

Phase 3d (users, roles, API keys over `/api/v1` + dashboard management
UI) plus installer and local-intel fixes so `install.sh` and
`WITH_GEMMA=1` work before this repo publishes its own release assets.

### Fixed

- **Installer no longer hard-errors on a missing release binary.**
  The `install.sh` shipped with v0.1.0 was binary-first and bailed
  out with a `[✗] Failed to download pre-built binary ... 404`
  whenever the target platform/version combination didn't have an
  asset uploaded yet. Since OpsIntelligence is still a young fork,
  that's the common case.
  - `install_binary()` now treats a 404 (or any curl failure) as a
    soft signal: if Go 1.24+ is installed locally, it transparently
    falls back to `build_binary_from_source` — same code path as
    `FORCE_BUILD=1`, just triggered automatically.
  - Operators who want the old strict behaviour can opt out with
    the new `NO_SOURCE_FALLBACK=1` env var (useful for airgapped
    mirrors that must only ship signed binaries).
  - Hard error paths are preserved for the truly unrecoverable
    case: release missing **and** Go not installed. The message now
    links to https://go.dev/dl/ so the operator knows what to do.
  - Install script header, README install table, and
    `--help` output document the new behaviour.

- **Gemma GGUF download now has a fallback mirror chain.**
  `WITH_GEMMA=1 bash install.sh` (and `opsintelligence local-intel
  setup`) used to point at a single URL:
  `github.com/hridesh-net/OpsIntelligence/releases/latest/download/gemma-4-e2b-it.gguf`
  — which 404s until we cut a release that bundles the GGUF.
  - `internal/localintel.BootstrapGGUF` now tries
    `DefaultGGUFURL` first and, on 404/transport failure, walks
    through `FallbackGGUFURLs` (which defaults to the AssistClaw
    release — byte-for-byte the same Gemma 4 E2B-IT GGUF). This
    matches AssistClaw's out-of-the-box behaviour and means brand
    new installs get Gemma without extra env vars.
  - The fallback chain is **only** used when the caller has not
    pinned a URL explicitly via `--url` or
    `OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_URL`. Pinning disables the
    chain on purpose — if you point us at an internal mirror, a
    silent fallthrough to a public URL would be a compliance
    footgun.
  - SHA-256 mismatches short-circuit the chain immediately — we do
    not try to find a mirror whose bytes satisfy a broken integrity
    pin.
  - New tests cover the happy fallthrough
    (`TestBootstrapGGUF_FallbackURL_SkipsFailedPrimary`), the pinned
    case (`TestBootstrapGGUF_ExplicitURL_DoesNotFallBack`), and the
    integrity guard (`TestBootstrapGGUF_SHA256Mismatch_AbortsChain`).

### Added

- **Phase 3d: Users, Roles & API Keys management.** The dashboard's
  `#/users` and `#/apikeys` routes are no longer placeholders —
  both surfaces are live, RBAC-gated, and audit-logged.
  - **`internal/gateway/users_api.go`** — HTTP twin of
    `opsintelligence admin user` + `role`:
    - `GET /api/v1/users` (`users.read`) — list users with role
      names; password hash is never serialised.
    - `POST /api/v1/users` (`users.manage` + `secrets.write`) —
      create a local user with argon2id-hashed password and
      optional initial roles.
    - `GET /api/v1/users/{id}` (`users.read`).
    - `PATCH /api/v1/users/{id}` — partial update of email,
      display name, status, password. Self-edit allowed without
      `users.manage` **except** status changes; resetting another
      user's password requires `secrets.write`.
    - `DELETE /api/v1/users/{id}` (`users.delete`) — with
      self-delete + last-owner guards.
    - `GET|POST /api/v1/users/{id}/roles` and
      `DELETE /api/v1/users/{id}/roles/{roleIDOrName}` —
      `roles.manage` for mutation, `users.read` for list.
      Accepts `role-owner`, `owner`, or short names.
  - **`internal/gateway/roles_api.go`** — read-only role catalogue:
    - `GET /api/v1/roles` (`roles.read`) seeds built-in roles
      lazily so a fresh deployment never 404s on `role-viewer`.
    - `GET /api/v1/roles/{idOrName}` returns permissions so the
      dashboard can render "what can this role do?".
  - **`internal/gateway/apikeys_api.go`** — HTTP twin of
    `opsintelligence admin apikey`:
    - `GET /api/v1/apikeys` — `apikeys.read.all` lists everyone;
      `apikeys.read.own` (or `?mine=1` scoping) returns just the
      caller's keys.
    - `POST /api/v1/apikeys` — mints. Self-mint needs
      `apikeys.manage.own`; minting for another user needs
      `apikeys.manage.all`. The plaintext `opi_<keyid>_<secret>`
      token is returned **exactly once** in the response body;
      argon2id-hashed secret is persisted via
      `auth.GenerateAPIKey`. Honours `auth.api_keys.enabled` —
      disabled config rejects regardless of RBAC.
    - `DELETE /api/v1/apikeys/{id}` — accepts `ak-<keyid>` or
      bare `key_id`; owner can revoke with `apikeys.manage.own`,
      anyone else needs `apikeys.manage.all`.
  - **Guardrails** (both surfaces enforce):
    1. Last-owner cannot be disabled, deleted, or lose `role-owner`.
    2. Caller cannot delete themselves.
    3. A user without `users.manage` cannot flip their own status.
    4. Password mutations for another user require `secrets.write`.
    5. API-key plaintext is returned only on mint.
  - **Audit** — every mutation writes a row (`ResourceType=user`
    or `apikey`) with path/method metadata plus role/key IDs and
    `mint_type` (`self`/`delegated`).
  - **Routing (`internal/gateway/authsvc.go`)** — new mounts use
    per-method routers: `GET` through `Protect`, mutating verbs
    through `ProtectCSRF` (cookie sessions require
    `X-CSRF-Token`; API-key callers are exempt by scheme).
- **Phase 3d: Dashboard Users & API Keys UI.** The
  `#/users` and `#/apikeys` routes now render full management
  surfaces in the SPA shell shipped in phase 3c.
  - **`internal/webui/dashboard/assets/app.html`** — replaced the
    two placeholder cards with `#users-body` / `#apikeys-body`
    mount points and added a generic `#modal-backdrop` element
    that every management dialog (invite, edit, roles, mint,
    revoke, show-plaintext) hangs off.
  - **`internal/webui/dashboard/assets/app.js`** — real
    renderers, RBAC-aware action buttons, modals:
    - `renderUsersView` — `GET /api/v1/users`, tabular layout
      with username + email, status pill, role chips, last
      login. Action buttons: **Edit** (display name / email /
      reset password), **Enable/Disable** (dark-pilled toggle),
      **Roles** (grant+revoke picker using cached
      `GET /api/v1/roles`), **Delete**. Buttons that would need
      permissions the operator lacks are disabled client-side
      via `meHasPerm`, and the backend remains authoritative.
    - **Invite flow** — `openInviteUserModal` minimal form with
      username, email, display name, password, initial-role
      multiselect; calls `POST /api/v1/users`.
    - `renderAPIKeysView` — `GET /api/v1/apikeys`, tabular layout
      with key ID, name, owner (for `apikeys.read.all`), status
      pill, created / expires / last-used, revoke button. Mint
      button launches `openMintKeyModal`.
    - **Mint flow** — `POST /api/v1/apikeys` with optional owner
      (only shown when the caller has `apikeys.manage.all`),
      optional expiry (Go duration), comma-separated scopes. The
      response pipes through `showMintedKey` which dispays the
      plaintext in a warn-banner modal with a one-click copy
      button and explicit "you will not see this again" copy.
    - **Revoke flow** — confirm-then-`DELETE` for both users
      and keys; errors round-trip through the generic modal.
  - **`internal/webui/dashboard/assets/style.css`** — new
    component styles for `.admin-table`, status `.pill-*`, role
    `.chip-role`, `.modal-backdrop`, `.warn-banner`,
    `.token-row`, and `.role-matrix`.
  - **`internal/webui/dashboard/dashboard_test.go`** — smoke
    tests now assert the new DOM anchors, JS renderers, and CSS
    classes all ship in the embedded bundle.
- **`doc/users-apikeys-api.md`** — new reference covering the
  permission matrix, the complete request/response shape of every
  endpoint, the guardrails enforced server-side, and the full set
  of audit actions emitted by the new handlers.

### Testing

- `internal/gateway/users_api_test.go` exercises the happy path
  and the security edges:
  `TestUsers_List_RequiresUsersRead`,
  `TestUsers_List_OwnerSees_AllUsers` (verifies password-hash
  leakage), `TestUsers_Create_OwnerCanMintWithRole`,
  `TestUsers_Create_DeveloperDenied`,
  `TestUsers_PatchSelf_AllowedWithoutUsersManage`,
  `TestUsers_Delete_BlocksLastOwner`,
  `TestUsers_Roles_GrantAndRevoke`,
  `TestRoles_List_ReturnsBuiltIns`,
  `TestAPIKeys_Create_ReturnsPlainTokenOnce` (checks the list
  endpoint does **not** leak the plaintext after mint),
  `TestAPIKeys_Create_OwnForOtherRequires_ManageAll`,
  `TestAPIKeys_Revoke_OwnerCanRevokeAny`.

## [0.1.0] — 2026-04-16

First tagged release of OpsIntelligence, cut from the AssistClaw fork.
This tag ships the complete agent + gateway + datastore + dashboard
surface that phases 1 through 3c have landed, plus a cleaned-up
install/uninstall flow ready for both local and cloud deployments.

### Release highlights

- Autonomous DevOps agent: PR review, Sonar triage, CI/CD monitoring,
  runbooks, incident scribe, with team-configurable policy files
  under `teams/<active>/`.
- Master + sub-agent supervision loop with `subagent_intervene`,
  `supervisor_report`, and shared-context opt-in.
- Webhook adapter framework with a first-class GitHub adapter
  (HMAC-SHA256 verification, event/action filtering, dedicated CLI
  setup flow).
- Ops-plane datastore (users, roles, API keys, sessions, OIDC state,
  audit log, task history) — SQLite by default, Postgres for cloud.
  Strictly separate from the agent memory tiers.
- Dashboard SPA at `/dashboard/` with login, overview, tasks (SSE),
  users + API keys placeholders, and full-parity Settings pages for
  every config section (`gateway`, `auth`, `datastore`, `providers`,
  `mcp`, `channels`, `webhooks`, `agent`, `devops`).
- Authentication + RBAC — Argon2id passwords, bootstrap flow, API
  keys scoped to permissions, CSRF double-submit, OIDC-ready,
  `authenticator` middleware on every protected route.
- `internal/configsvc` shared service so the CLI and the dashboard
  mutate `opsintelligence.yaml` through the same optimistic-
  concurrency-controlled code path.
- Skills marketplace + `skills install` from GitHub / path /
  marketplace; comprehensive `gh-pr-review` skill covering local
  checkout, test/lint, and GitHub Reviews API posting.
- Smart-prompt chains (`pr-review`, `sonar-triage`, `cicd-regression`,
  `incident-scribe`) with meta prompts `self-critique`,
  `evidence-extractor`, `plan-then-act`.

### Install / uninstall

- **`install.sh`** — refreshed for the ops-plane surface. Scaffolds
  `$STATE_DIR/datastore/` (so headless cloud installs never race on
  permissions), fixed the header box alignment, and expanded the
  `--help` output to include the new post-install dashboard hints.
  The "done" banner now points at
  `http://127.0.0.1:18790/dashboard/`, names the first-run owner
  bootstrap explicitly, and lists the datastore path
  (`$STATE_DIR/ops.db`, SQLite by default).
- **`uninstall.sh`** — now aware of the datastore.
  - `--purge` removes `ops.db`, `ops.db-wal`, `ops.db-shm` along
    with the existing config/memory/skills/security trees; the
    confirmation preview calls this out explicitly.
  - New `--keep-datastore` flag (pair with `--purge`) snapshots
    `ops.db*` aside, wipes the rest of the state tree, and restores
    the datastore — the supported migration path when moving
    OpsIntelligence between hosts without losing users, roles, API
    keys, or the audit log.
  - Non-purge summary now calls out `ops.db` explicitly and offers
    both `--purge` and `--purge --keep-datastore` next-steps.

### Added

- **Phase 3c: Settings UI wired to the configsvc HTTP API.** The
  dashboard shipped in phase 2c now ships a real Settings surface
  instead of a placeholder card. Every section listed in
  `internal/gateway/config_api.go`'s `putConfigSection` is editable
  in the browser, against the same `configsvc` the CLI calls.
  - **`internal/webui/dashboard`** — promoted from "minimal shell"
    to a hash-routed SPA:
    - Hash-based router: `#/overview`, `#/tasks`, `#/users`,
      `#/apikeys`, `#/settings/<section>`. Direct linking + back/
      forward work; no server-side reload.
    - Schema-driven Settings renderer. `CONFIG_SCHEMA` declares the
      fields per section (text / password / number / checkbox /
      tri-state checkbox / select / textarea / duration / tags /
      kv-tags / kv-textarea / nested objects / nullable objects).
      Adding a new section is "add a schema entry + a sub-nav link";
      no new render/save code needed for the common cases.
    - Settings panels for `gateway`, `auth`, `datastore`, `agent`,
      `channels`, `webhooks` (including the typed GitHub adapter
      sub-form), and `devops` (GitHub / GitLab / Jenkins / Sonar).
    - Custom Settings panels for `providers` (cloud + Azure +
      OpenRouter + HuggingFace + Bedrock + Vertex + local Ollama /
      vLLM / LM Studio, each independently nullable with a
      "Configured" toggle and provider-specific fields) and `mcp`
      (built-in server + dynamic Add/Remove client list mirroring
      `opsintelligence mcp add/remove`).
    - Optimistic-concurrency save flow. Each section caches the
      revision token returned by `GET /api/v1/config/<section>`,
      sends it back as `If-Match` on `PUT`, and surfaces 409
      conflicts as a non-destructive "Saved by someone else, reload"
      toast.
    - Sensitive-field handling. Password / token / DSN inputs render
      empty with a `(leave blank to keep current value)` placeholder;
      the serializer re-sends the original (server-redacted) value
      when the field is left blank, so saving a form never
      accidentally clears a stored secret.
    - CSRF-correct writes — every state-changing fetch picks up the
      `opi_csrf` cookie and forwards it as `X-CSRF-Token`, matching
      the gateway's double-submit middleware (`ProtectCSRF`).
    - Toast component for save success / warning / error and a
      reload button on every form for explicit refresh.
  - **`internal/webui/dashboard/dashboard_test.go`** — smoke tests
    that run the embedded `Handler()` through `httptest` and assert
    the SPA bundle still ships the entry points the new UI depends
    on (`CONFIG_SCHEMA`, `loadSettingsSection`, `If-Match`,
    `renderProvidersSection`, `renderMCPSection`), the settings
    sub-nav is present in `app.html`, the dashboard styles ship
    `.settings-shell` / `.toast`, and the `/dashboard/` redirect
    still lands on `/dashboard/app` (regression check for the
    phase-2c upstream-host bug).

- **Phase 3a kickoff: shared `internal/configsvc` for CLI/UI config parity.**
  - Added `internal/configsvc` with atomic config writes, revision tokens,
    and optimistic-concurrency support (`UpdateWithRevision` +
    `ErrRevisionConflict`) so upcoming dashboard APIs can avoid blind
    last-write-wins behavior.
  - Added typed config operations for key surfaces (`gateway`, `auth`,
    `datastore`, `providers`, `channels`, `webhooks`, `mcp`, `agent`,
    `devops`) plus targeted helpers for `skills` and MCP clients.
  - Migrated CLI config mutations to `configsvc` for:
    - `opsintelligence mcp add`
    - `opsintelligence mcp remove`
    - `opsintelligence skills enable`
    - `opsintelligence skills disable`
    - Any command path that toggles enabled skills via
      `toggleSkillInConfig` (including `skills add/install/remove`).
  - Added `doc/configsvc.md` describing the service contract used by
    both CLI and the upcoming phase-3b HTTP handlers.

- **Gateway auth endpoints + dashboard shell (phase 2c of the
  cloud-dashboard + RBAC rollout).** The phase-2b primitives are now
  actually reachable from a browser: start the gateway and a minimal
  login → owner-bootstrap → dashboard frame → logout flow is live on
  `/dashboard/` and `/api/v1/auth/*`.
  - **`internal/gateway/authsvc.go`** — new `AuthService` that wires
    `auth.Authenticator`, `auth.SessionManager`, and `rbac.Resolver`
    together from `config.AuthConfig`, then mounts the phase-2 HTTP
    surface on an `http.ServeMux`. Handlers:
    - `GET  /api/v1/auth/status`    — public; tells the SPA whether
      the owner has been bootstrapped, which credential flows are
      enabled, and the min-password policy. No auth required.
    - `POST /api/v1/auth/bootstrap` — first-run only. Anonymous until
      the users table has one row; refuses further anonymous writes
      afterwards. Creates the `owner` principal, grants `role-owner`,
      mints a session + CSRF cookie, returns the principal JSON.
    - `POST /api/v1/auth/login`     — public. Argon2id verify with
      opportunistic bcrypt-→-argon2id rehash on success; sets the
      session + CSRF cookies; returns principal + `expires_at`.
    - `POST /api/v1/auth/logout`    — authenticated. Revokes the
      session row server-side and expires both cookies.
    - `GET  /api/v1/whoami`         — authenticated. Returns the
      caller's principal DTO (`type`, `user_id`, `username`, `roles`,
      …) suitable for the dashboard's side-panel.
    - `AuthService.Protect` / `AuthService.ProtectCSRF` — handler-
      wrapping helpers used by future phase-3b endpoints to require a
      non-anonymous principal (optionally with double-submit CSRF).
  - **`internal/webui/dashboard`** — tiny embedded SPA served under
    `/dashboard/`. `login.html` auto-switches between "Sign in" and
    "First-run setup" based on `/api/v1/auth/status`; `app.html` is
    the post-login shell with a nav sidebar, a live whoami card, and
    four placeholder panels (Tasks / Users & Roles / API keys /
    Settings) that will get filled in phase 3c. `app.js` mirrors the
    `opi_csrf` cookie into `X-CSRF-Token` for mutating calls. All
    assets are `//go:embed`-bundled so the binary stays single-file.
  - **`internal/gateway/server.go`** — new `Server.AuthService` field.
    When non-nil, the gateway auto-mounts the phase-2 auth surface
    AND the dashboard at `/dashboard/`. The legacy `Bearer <token>`
    path on `/api/status`, `/api/chat`, `/api/webhook/`, etc. is
    untouched for backwards compatibility; the same shared token is
    also accepted by the new `Authenticator` chain as a synthetic
    `system:legacy-shared-token` principal.
  - **`cmd/opsintelligence/gateway_auth.go`** — `attachAuthToGateway`
    opens the ops-plane datastore with `Migrations: "auto"`,
    `SeedBuiltInRoles`-es on every boot, constructs the
    `AuthService`, and attaches it to the gateway. Wired into both
    `opsintelligence gateway serve` (foreground) and
    `opsintelligence gateway start` (background daemon). Auth is
    disabled cleanly when `datastore.driver == "none"`, leaving the
    gateway in its legacy Bearer-only mode.
  - **`internal/gateway/authsvc_test.go`** — unit tests over the
    full surface against a fresh in-memory sqlite: fresh-store
    status, login happy-path/wrong-password/missing-fields, whoami
    with/without session, bootstrap creates owner + rejects
    double-bootstrap + enforces min-password, logout clears cookie
    and subsequent whoami 401s.
  - End-to-end smoke passed against the real binary (sqlite backend,
    `gateway serve`): `GET /status` → `bootstrap_needed: true` →
    `POST /bootstrap` → 201 + session → `GET /whoami` → owner →
    `POST /logout` (with CSRF) → `GET /whoami` → 401 → `POST /login`
    → 200 → `GET /whoami` → owner again. `/dashboard/` redirects to
    `/dashboard/app`; `app.js`/`style.css`/`login.html` served from
    the embedded FS. Legacy bearer token continues to authenticate
    both `/api/status` and `/api/v1/whoami`.

- **Auth primitives + Authenticator middleware + admin CLI (phase 2b
  of the cloud-dashboard + RBAC rollout).** Everything the HTTP
  gateway and dashboard need to turn a request into a `*auth.Principal`
  backed by a real user row, plus the operator-facing CLI to
  provision those rows on day one.
  - **`internal/auth/passwords.go`** — argon2id default hasher with
    PHC-style envelope (`$argon2id$v=19$m=...,t=...,p=...$salt$digest`)
    and bcrypt (`$2a$/$2b$/$2y$`) verify-only path for migrating
    legacy rows. `HashPassword`, `VerifyPassword`, `NeedsRehash`,
    `RandomToken`, `ConstantTimeEqual` utilities. `ErrInvalidCredentials`
    / `ErrMalformedHash` sentinels split user-visible 401s from
    corrupt-data logs.
  - **`internal/auth/apikeys.go`** — wire format `opi_<key_id>_<secret>`
    so leaked keys grep cleanly; 8-char lowercase `key_id` is the
    public handle shown in audit / dashboard, 32-byte secret is
    argon2id-hashed and never stored. `GenerateAPIKey`, `ParseAPIKey`,
    `VerifyAPIKey` (revoke + expiry aware), `MaskAPIKey` helper.
  - **`internal/auth/sessions.go`** — `SessionManager` built on
    `datastore.SessionRepo`. Signed HttpOnly session cookie +
    double-submit CSRF cookie, `Secure` flag tracks TLS by default.
    `Create` / `Load` / `Touch` / `Revoke` / `IssueCSRF` / `CSRFTokenFrom`.
  - **`internal/auth/middleware.go`** — `Authenticator` HTTP
    middleware running the credential chain
    `cookie → API key bearer → legacy shared token`. Attaches
    `*auth.Principal` to request context via `auth.WithPrincipal`,
    touches session rows async, 401s with `WWW-Authenticate: Bearer`
    by default, supports `AllowAnonymous` for `/api/v1/bootstrap`,
    plus a sibling `RequireCSRF` middleware that only fires for
    cookie-authed unsafe methods (API keys/bearer tokens bypass).
    Custom `ErrorHandler` hook for JSON rendering in the gateway.
  - **`internal/config.AuthConfig`** — YAML surface for every knob:
    local policy, API key expiry defaults, session cookie/TTL,
    CSRF toggle, full OIDC block (wired in phase 4), legacy shared
    token (inherits `OPSINTELLIGENCE_GATEWAY_TOKEN`),
    `allow_anonymous_bootstrap`. Defaults applied in
    `applyAuthDefaults`; `Secure` cookie flag auto-tracks
    `gateway.tls.cert`/`gateway.tls.key`.
  - **`opsintelligence admin` CLI** with `init`, `user
    {add,list,disable,enable,delete,password}`, `role
    {list,grant,revoke}`, `apikey {create,list,revoke}`. Interactive
    password prompts go through `golang.org/x/term` without echo,
    API-key secrets print exactly once at creation time. The
    command group is the CLI twin of the Settings UI that lands in
    phase 3c.
  - **Tests**: argon2id hash/verify round-trip, bcrypt interop,
    malformed-hash rejection, salt uniqueness, `NeedsRehash` on
    weaker params, API-key generate/parse/verify with revoke +
    expiry, masked token never leaks secret, Authenticator chain
    against a real SQLite store (401 without creds, 401 on revoked
    session, 200 on cookie / API key / legacy token, `AllowAnonymous`
    path, CSRF GET bypass + POST reject + POST accept).
  - **Documentation**: `.opsintelligence.yaml.example` gains a
    fully-commented `auth:` block mirroring every knob.
  - **Deferred to phase 2c**: minimal dashboard shell (login page +
    empty Settings frame) wired to this middleware.
  - **Deferred to phase 3a**: `internal/configsvc` shared layer so
    CLI commands and the dashboard REST API both drive config
    through identical methods.
- **RBAC engine + identity primitives (phase 2a of the cloud-dashboard
  + RBAC rollout).** New `internal/auth` and `internal/rbac` packages
  establish the identity and authorisation layer above the ops-plane
  datastore. Pure, allocation-light, and dependency-free for the hot
  path so HTTP middleware / the agent runner / the security guardrail
  can enforce permissions without importing password hashing or OIDC.
  - **`internal/auth.Principal`** — the identity object threaded
    through request context and tool calls. Four principal types
    (`user`, `apikey`, `system`, `anonymous`), each with a fixed
    meaning and safe defaults. `WithPrincipal` / `PrincipalFrom` /
    `MustPrincipal` handle ctx plumbing; `SystemPrincipal(name)` mints
    the audit-tagged internal actor used by cron, webhook handlers,
    and master→subagent invocations.
  - **Permission catalogue** (`internal/rbac/permissions.go`) —
    34 dotted, namespaced `Permission` constants covering the v1
    surface: `agent.*`, `tasks.*`, `users.*`, `roles.*`, `apikeys.*`,
    `audit.*`, `skills.*`, `tools.*`, `webhooks.*`, `channels.*`,
    `settings.*`, `secrets.*`, `datastore.*`, `dashboard.*`, `chat.*`.
    Wildcards supported (`tasks.*`, `*`) and matched by an
    allocation-free `Permission.Matches`.
  - **Built-in roles** (`internal/rbac/roles.go`) — six shipped
    roles (`owner`, `admin`, `operator`, `developer`, `auditor`,
    `viewer`) defined in Go and re-seeded on every boot via
    `SeedBuiltInRoles`, so tweaking a role is a code change not a
    migration. Custom roles coexist unchanged.
  - **Enforcement engine** (`internal/rbac/engine.go`) — `Enforce`,
    `EnforceAny`, `EnforceAll`, and the fast `Can` / `CanAny`
    variants. Sentinel errors `ErrDenied` and `ErrNotAuthenticated`
    let handlers split 401 vs 403; `DeniedError` carries principal
    and permission for audit logs. System principals always allow,
    anonymous always fails.
  - **Bootstrap + Resolver** (`internal/rbac/bootstrap.go`) —
    `SeedBuiltInRoles` is idempotent (re-seeds on every boot);
    `BootstrapOwner` creates the `user-owner` row on a fresh database
    and grants `role-owner`. `Resolver.ForUser` / `ForAPIKey` build a
    flattened, scope-intersected Principal from the datastore so the
    Authenticator middleware (phase 2b) only does the lookup once per
    credential check.
  - **Tests** cover exact/wildcard/global permission matching, owner
    bypass, viewer cannot invoke the agent, roles reference only
    declared permissions, idempotent re-seed, API-key scope
    intersection against an SQLite-backed store, and Principal
    context round-trip.
- **Ops-plane datastore layer (phase 1 of the cloud-dashboard +
  RBAC rollout).** New `internal/datastore` package introduces the
  persistence surface for users, roles, permissions, API keys,
  sessions, audit log, task history and OIDC state. Strictly
  separate from agent memory (`internal/memory` /
  `internal/mempalace`) — different tables, different DSN, different
  lifecycle.
  - **Interfaces first**: `Store`, `UserRepo`, `RoleRepo`,
    `APIKeyRepo`, `SessionRepo`, `AuditRepo`, `TaskHistoryRepo`,
    `OIDCStateRepo`; upstream auth/RBAC/gateway code depends on
    these, not on the driver.
  - **Two drivers** at `internal/datastore/driver/sqlite` (bundled
    default, backed by `mattn/go-sqlite3`, adds `_loc=UTC` to DSNs
    so datetime comparisons round-trip across hosts) and
    `internal/datastore/driver/postgres` (new `lib/pq` dependency).
    Side-effect import
    `github.com/opsintelligence/opsintelligence/internal/datastore/drivers`
    registers both.
  - **Embedded migrations** under
    `internal/datastore/migrations/{sqlite,postgres}/0001_init.sql`;
    per-driver DDL kept in sync for every version number. Applied
    via `datastore.RunMigrations` / `Store.Migrate`, tracked in a
    portable `schema_migrations` table.
  - **Shared sqlstore** at `internal/datastore/sqlstore/` implements
    every repo against `database/sql` with a tiny `Dialect`
    interface that does placeholder rewriting (`?` → `$N`) and
    bool-literal selection. New schema columns only need one set of
    scan/insert helpers across drivers.
  - **Sentinel errors** `ErrNotFound`, `ErrConflict`, `ErrExpired`,
    `ErrInvalidConfig` with driver-error mapping (handles
    lib/pq SQLSTATE 23505 and mattn's "UNIQUE constraint failed").
- **`opsintelligence datastore` CLI.** New subcommands:
  - `datastore migrate` — apply all pending migrations (prints
    before/after version).
  - `datastore status` — show driver, redacted DSN, applied /
    latest / bundled counts, up-to-date vs pending.
  - `datastore ping` — verify connectivity (5 s timeout).
  - `datastore down` — deliberate stub; emits guidance for manual
    reverse SQL instead of silent destructive rollbacks.
- **`DatastoreConfig`** added to `internal/config.Config` with
  defaults in `applyDefaults`: driver `sqlite`, DSN
  `file:<state_dir>/ops.db?_foreign_keys=on&_busy_timeout=5000`
  (tilde-expanded so onboarding's `state_dir: "~/..."` template
  resolves correctly), migrations `auto`. The
  `OPSINTELLIGENCE_DATASTORE_DSN` env var overrides the YAML value.
- **`.opsintelligence.yaml.example`** gains a `datastore:` block
  with both SQLite and Postgres examples inline.

### Added (prior)

- **Pluggable webhook-adapter framework.** New
  `internal/webhookadapter` package introduces a typed, first-class
  contract for inbound action webhooks (GitHub today, GitLab /
  Bitbucket / Jira / Datadog / PagerDuty as peers later). An `Adapter`
  owns `Name / Path / Enabled / Verify / Parse / Filter / Render`; the
  shared `Router` mounts every registered adapter under
  `/api/webhook/<path>`, enforces a 2 MiB body cap, runs
  `Verify → Parse → Filter → Render`, acquires a slot from a shared
  semaphore (`webhooks.max_concurrent`, default 10, saturation → 503 +
  `Retry-After: 30`), responds 202 Accepted, and detaches the agent run
  into a background goroutine with a shared timeout
  (`webhooks.timeout`, default 10m). Filter results with reason prefix
  `healthcheck:` (e.g. GitHub's `ping`) return 204 No Content.
- **GitHub adapter** at `internal/webhookadapter/github/`. Replaces the
  previous `internal/gateway/github_webhook.go` (now removed). Same
  HMAC-SHA256 verification, same event/action allowlist, same nested
  `text/template` prompt rendering — just now behind the shared
  adapter contract, so adding GitLab/Bitbucket/Datadog next is a
  drop-in change rather than another bespoke handler.
- **Config restructure**: `webhooks.github.*` is now
  `webhooks.adapters.github.*`. Router-level concurrency and timeout
  moved to `webhooks.max_concurrent` / `webhooks.timeout` so every
  adapter shares a single pool. Legacy `webhooks.mappings` remain fully
  supported as a fallback for ad-hoc generic receivers.
- **Master ↔ child supervision layer.** The master agent now sees a
  live dashboard of active sub-agents on every one of its turns
  (auto-injected via the new `Runner.WithSystemPromptAugmentor` hook)
  — no polling required. Each entry shows task id, status, elapsed,
  goal, last progress event, and pending intervention count. Children
  have their own augmentor that drains pending interventions at the
  top of each iteration and surfaces them as a `## SUPERVISOR
  GUIDANCE` block. Ambient parent/child oversight, zero extra tool
  calls.
- **Async / parallel sub-agent orchestration (generalised).**
  `internal/subagents.TaskManager` now carries per-task
  `ProgressEvent` streams (with `KindProgress|Blocked|Error|Lifecycle`),
  pending/applied `Intervention` lists, and a `SharedNote` audit
  trail. `ExecFn` now threads a `task_id` through so the child's
  runner can wire supervisor hooks scoped to its own task. Task
  retention (default 256), per-task event-log bound (default 128),
  bounded concurrency (default 8) all unchanged. Existing six async
  tools (`subagent_run_async`, `_parallel`, `_status`, `_wait`,
  `_tasks`, `_cancel`) unchanged.
- **New master-side supervisor tools**: `subagent_intervene(task_id,
  guidance)` pushes authoritative guidance that the child obeys on
  its next iteration; `subagent_stream(task_id?, since_index?)` drains
  the ordered event log for inspection; `subagent_share_context(task_id,
  note)` records an explicit opt-in context share (audit-trail only,
  isolation invariant preserved); `subagent_read_context(task_id)`
  reads back the shared-context trail.
- **New child-side tool** `supervisor_report(message, phase?, kind?)`,
  pre-bound to the child's own `task_id`, for posting
  `ProgressEvent`s back to the TaskManager. Children can report
  `progress`, `blocked`, or `error` kinds. Injected into the child's
  tool registry only on the tracked (async) path; the legacy
  synchronous `subagent_run` does not get supervision.
- **Docs**: [`doc/webhook-adapters.md`](doc/webhook-adapters.md)
  (framework reference) and
  [`doc/supervised-subagents.md`](doc/supervised-subagents.md) (the
  parent/child model, lifecycle, and tool surface).
- **Updated `doc/github-webhooks.md`** to reference the new adapter
  layout and the shared router-level concurrency knobs.

### Changed

- `subagents.ExecFn` signature is now
  `func(ctx, task_id, sub_agent_id, prompt)` — callers must update
  custom executors. The TaskManager threads the task id so executors
  can install per-task supervisor hooks.
- `SubAgentSvc.runSync` remains for the synchronous path but now
  delegates to `runSyncWithTask("", …)` — supervision is only
  available on tracked async tasks.
- Sub-agent child runners now also have `subagent_intervene`,
  `subagent_stream`, `subagent_share_context`, and
  `subagent_read_context` in their `subAgentOmit` list (a child
  cannot intervene on itself or a sibling).

### Removed

- `internal/gateway/github_webhook.go` and its `_test.go` — logic
  migrated unchanged into `internal/webhookadapter/github/`.

- **Smart prompts & prompt chaining.** New `internal/prompts` package
  introduces `SmartPrompt` / `Chain` types, a filesystem loader (YAML
  frontmatter + Go `text/template` body), and a bounded sequential
  `Runner` that pipes each step's output into the next as `{{.prev}}`.
  Chains are hard-capped, never loop, and never call write-action tools.
- **Shipped library of DevOps chains**:
  `pr-review` (gather → analyze → critique → render),
  `sonar-triage` (fetch → classify → recommend),
  `cicd-regression` (fetch → compare → report), and
  `incident-scribe` (summarize → update → postmortem).
  Plus three meta prompts: `meta/self-critique`, `meta/evidence-extractor`,
  and `meta/plan-then-act`.
- **`chain_run` and `chain_list` agent tools** so the LLM can invoke a
  named chain (or single meta prompt) mid-conversation.
- **Smart Prompts Index** injected into the agent's system prompt (via
  `ExtensionPromptAppend`) listing all available chain and meta prompt
  ids with one-line purposes.
- **`opsintelligence prompts` CLI** with `ls`, `show <id>`, and
  `run <id> --input key=value` subcommands for inspecting and smoke-
  testing prompts from the terminal.
- **Embedded seed**. The prompt library is embedded via `go:embed
  all:seed/prompts` inside `internal/config/` and seeded into
  `<state_dir>/prompts/` on first `init`. Operator edits are never
  overwritten on re-init.
- **DevOps skill nodes** now start with a "Fast path" hint directing the
  model to the relevant chain, and `skills/devops/SKILL.md` documents
  the full `chain_run` vocabulary alongside the existing
  `read_skill_node` flow.
- **Docs**. New [`doc/smart-prompts.md`](doc/smart-prompts.md) explaining
  the library, chain schema, override model, and authoring guidelines,
  plus a README section pointing at it.

### Notes

- The chain library is authored from scratch; techniques (structured
  reasoning phases, self-critique passes, evidence-first rendering,
  explicit budgets) are informed by patterns common to modern public
  agent system prompts, but no GPL source material was copied.

### Fixed

- **`opsintelligence_localgemma` build now compiles cleanly.** Dropped the
  `ffi.Available()` / `ffi.InitError()` references in
  `internal/localintel/gemma_engine.go` — `jupiterrider/ffi` v0.5.x removed
  those symbols (its `init()` now panics if libffi is missing, so there is
  nothing meaningful to probe from userspace). Runtime errors are now
  surfaced via `gollama.Backend_init()` instead. Fixes `go build
  -tags opsintelligence_localgemma ./...` and the doctor snapshot test.
- **Doctor snapshot refreshed** to match the DevOps-only surface
  (dropped `channel.whatsapp` legacy check, added `devops.network`).

### Changed

- **Skills tree hard-focused on DevOps.** Removed 42 consumer/personal
  skills inherited from the AssistClaw base fork (Apple Notes / Reminders,
  Bear, Obsidian, Notion, Trello, 1Password, iMessage, BlueBubbles, Bluetooth,
  Apple Music / Sonos / Spotify, Gemini, Whisper / TTS, ordercli, peekaboo,
  GOG, Weather, and more). `skills/` now contains only DevOps-relevant
  skills: `devops`, `gh-pr-review`, `github`, `gh-issues`, `slack`,
  `healthcheck`, `tmux`, `xurl`, `summarize`, and `skill-creator`.
- **`skills/marketplace.json` rewritten** to match the retained set with
  DevOps-oriented tags (pr-review, ci-cd, monitoring, runbooks, etc.).
- **Tool graph (`internal/graph/tool_graph.go`) extended with DevOps
  intents**: `IntentPRReview`, `IntentSonar`, `IntentCICD`,
  `IntentIncident`, and `IntentDevOpsGeneric`, each mapped to the
  smart-prompt chains and `devops.*` tools used for that workflow. BFS
  seeds now route "review PR", "sonar", "pipeline failed", "incident",
  etc. straight to `chain_run` plus the right evidence-fetching tool.
- **Runner identity + common workflows** updated in
  `internal/agent/runner.go`: the DevOps-first persona is now the default
  system prompt (when no SOUL.md/IDENTITY.md is present), and the
  "Common Workflows" section leads with the four DevOps chains before
  the generic building blocks.
- **Gateway A2A Agent Card** advertises DevOps capabilities
  (`devops.pr-review`, `devops.sonar-triage`, `devops.cicd-regression`,
  `devops.incident-scribe`, `smart-prompt-chains`, `webhooks`) and a
  DevOps-oriented default description.
- **`opsintelligence tools list`** now surfaces `chain_run`, `chain_list`,
  and every `devops.*` tool in the built-in table, matching what the
  runner actually registers.
- **CLI root `--help`** rewritten to describe the DevOps agent surface
  (skill graph, gh-pr-review skill, smart-prompt chains, team policies)
  instead of the old "hardware-integrated assistant" tagline.

### Added (skills)

- **`gh-pr-review` skill** at [`skills/gh-pr-review/`](skills/gh-pr-review)
  — a proper standalone skill for reviewing GitHub pull requests. Covers
  the full loop: identify PR, gather evidence with `gh` / `gh api`,
  check out into a disposable `git worktree`, run the repo's
  lint/test/build locally, post a review through the Reviews API with
  line-level comments and one-click ```suggestion``` blocks, and
  submit `APPROVE` / `REQUEST_CHANGES` / `COMMENT`. Ships with:
  - [`SKILL.md`](skills/gh-pr-review/SKILL.md) — workflow overview +
    safety posture (read-only by default; merges require human "yes").
  - [`commands.md`](skills/gh-pr-review/commands.md) — full `git` +
    `gh` + `gh api` reference used throughout the workflow.
  - [`comments.md`](skills/gh-pr-review/comments.md) — review-comment
    and suggestion templates (single-line, multi-line, rename, insert,
    delete, blocker without suggestion, review summary bodies,
    replies, thread resolution).
  - Runnable helpers under `scripts/`: `pr-evidence.sh`,
    `apply-and-test.sh`, and `post-review.sh` (validates payload,
    requires explicit "yes" before submitting).
- The `skills/devops/pr-review.md` graph node and
  `skills/devops/SKILL.md` map-of-content now cross-link to this
  skill and to `skills/github/` / `skills/gh-issues/` as companion
  skills.

## [v0.1.0] - 2026-04-16

Initial public release of **OpsIntelligence**.

### Project

- Hard-forked from [AssistClaw](https://github.com/hridesh-net/AssistClaw)
  at the commit that shipped AssistClaw's `doctor` Sprint 03.
  OpsIntelligence inherits the agent runner, 3-tier memory system,
  lazy-loaded skill graph, tool catalog, MCP support, cron scheduler,
  webhook endpoint, security guardrail, and extensions framework.
- Module path: `github.com/opsintelligence/opsintelligence`.
  Binary: `opsintelligence`. State directory: `~/.opsintelligence/`.

### Added

- **DevOps platform clients** under `internal/devops/{github,gitlab,jenkins,sonar}`
  with a shared `HTTPDoer` interface and `httptest`-backed unit tests.
- **`devops.*` agent tools** registered in the tool catalog:
  `devops.github.*` (PRs, diffs, workflow runs, combined status),
  `devops.gitlab.*` (MRs, pipelines, pipeline jobs),
  `devops.jenkins.*` (jobs, builds),
  `devops.sonar.*` (quality gate, issues, hotspots).
- **Team rule system.** New `teams:` config block with an `active` team
  and a `dir` (default `<state_dir>/teams`). Every `*.md` under
  `teams/<active>/` is merged into the system prompt via
  `extensions.prompt_files`.
- **DevOps skill graph** at `skills/devops/` with an INDEX entry node
  and leaf nodes for `pr-review`, `sonar`, `cicd`, `incidents`, and
  `runbooks`. Each node cross-links via wikilinks.
- **DevOps-flavoured workspace templates**: new `SOUL.md`, `IDENTITY.md`,
  and `HEARTBEAT.md` templates focused on DevOps posture,
  read-only-by-default safety, and a morning sweep checklist.
- **Example team**. `teams/example-team/` ships five policy templates
  (`README`, `pr-review`, `sonar`, `cicd`, `secrets-and-safety`) and is
  seeded into the state directory on `init`.
- **Config presets**: `.opsintelligence.yaml.example` includes copy-paste
  cron heartbeat entries and webhook mapping presets for GitHub, GitLab,
  and Jenkins.
- **Doctor reachability checks** for enabled DevOps integrations
  (`doctor_devops.go`).

### Changed

- **Channels are enterprise-only.** Only Slack and the REST/WebSocket
  gateway remain as supported channels. All other channel wiring has
  been removed from `main.go`, `doctor_cmd.go`, the channels adapter
  capability registry, the tool graph keywords, and the `message` tool
  surface.
- **Onboarding wizard.** `opsintelligence onboard` is now a minimal,
  DevOps-focused flow. It collects one LLM provider API key, optional
  Slack tokens, optional GitHub/GitLab/Jenkins/SonarQube tokens, and an
  active team name, then writes `~/.opsintelligence/opsintelligence.yaml`.
  Advanced configuration is edited directly in YAML.
- **README** rewritten to describe OpsIntelligence's DevOps scope,
  integrations, safety posture, and team-rule system.

### Removed

- WhatsApp, Telegram, and Discord channel packages, wiring, config
  fields, doctor checks, capability registry entries, and vendored
  dependencies.
- Consumer-oriented README sections (WhatsApp/Telegram/Discord
  quickstart, "edge intelligence for your phone" framing).
