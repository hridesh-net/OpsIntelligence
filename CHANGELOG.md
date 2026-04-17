# Changelog

All notable changes to this project are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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
