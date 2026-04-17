# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
