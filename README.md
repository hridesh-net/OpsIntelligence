<h1 align="center">OpsIntelligence</h1>

<p align="center">
  <em>An autonomous DevOps agent you can trust in production.</em><br>
  PR review · SonarQube triage · CI/CD monitoring · incident support · runbooks.
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=for-the-badge&logo=go" alt="Go">
  <img src="https://img.shields.io/badge/License-MIT-22c55e?style=for-the-badge" alt="MIT">
  <img src="https://img.shields.io/badge/Posture-read--only%20by%20default-6f42c1?style=for-the-badge" alt="Read-only by default">
</p>

---

## What this is

OpsIntelligence is an autonomous agent for **DevOps teams inside a
company**. It plugs into the systems you already run — GitHub (Actions),
GitLab (CI), Jenkins, SonarQube, Slack — and carries out the DevOps work
that normally eats a senior engineer's week:

- Review a pull/merge request against your team's policy.
- Watch CI pipelines on `main`, flag real regressions, ignore flakes.
- Read SonarQube quality gates and decide block vs. flag vs. ignore.
- Help an on-call engineer triage a production incident and draft the
  postmortem.
- Execute a runbook one step at a time, with a human in the loop.

It is **configurable per team**: drop a handful of Markdown policy files
into `teams/<your-team>/` and the agent follows *your* rules rather than
generic defaults.

## What this is not

- It is **not** an autonomous deployment bot. OpsIntelligence is
  **read-only by default** on every surface. Merging a PR, retrying a
  pipeline, rolling back, silencing a Sonar rule — all require explicit
  human confirmation in the same turn.
- It is **not** a general consumer assistant. Messaging runs through
  **Slack** or the **REST/WebSocket gateway** only. Consumer channels
  (Telegram, WhatsApp, Discord) have been removed from the core.

## Relationship to AssistClaw

OpsIntelligence is a hard fork of the
[AssistClaw](https://github.com/hridesh-net/AssistClaw) runtime. It
inherits AssistClaw's agent loop, 3-tier memory, lazy-loaded skill
graph, tool catalog, MCP support, cron scheduler, webhooks, security
guardrail, and extensions framework. Everything consumer-oriented has
been stripped out, and a first-class `devops.*` tool surface plus
team-aware rule system have been added in their place.

---

## Built-in integrations

| Platform | Status | What it reads |
|---|---|---|
| **GitHub** (cloud & Enterprise) | first-class | PRs, diffs, Actions runs, combined status |
| **GitLab** (cloud & self-hosted) | first-class | MRs, pipelines, jobs |
| **Jenkins** | first-class | jobs, builds, queue status |
| **SonarQube / SonarCloud** | first-class | quality gates, issues, hotspots |
| **Slack** | first-class | inbound + outbound messaging |
| **Everything else** (PagerDuty, Datadog, Sentry, Jira, Grafana, Azure DevOps, Bitbucket, Terraform Cloud, …) | via MCP | plug in any MCP server |

Every integration is off until you give it a token, and each token lives
in an environment variable via `token_env:` — never in the YAML file.

---

## Install

**One-liner (recommended, pulls the latest release binary):**

```bash
curl -fsSL https://raw.githubusercontent.com/hridesh-net/OpsIntelligence/main/install.sh | bash
```

**Pin a specific version:**

```bash
OPSINTELLIGENCE_VERSION=v0.2.5 bash install.sh
```

**Build from source (requires Go matching `go.mod`, currently 1.26+):**

```bash
git clone https://github.com/hridesh-net/OpsIntelligence.git
cd OpsIntelligence
FORCE_BUILD=1 bash install.sh
```

The installer is loud about what it does: it drops the `opsintelligence`
binary under `/usr/local/bin` (falls back to `~/.local/bin` if not
writable), scaffolds `~/.opsintelligence/` (state + datastore), and
optionally registers a login service so the gateway starts on sign-in.
Pass `SKIP_SERVICE=1` if you don't want that.

### Installing on a client or locked-down machine

Use this when **you are not on your own dev box** — e.g. a customer VM,
corporate laptop, or host where security policy limits what may be
downloaded or compiled.

1. **Prefer a pre-built binary only.** Have the client install from a
   **tagged GitHub release** that includes
   `opsintelligence-<os>-<arch>` for their platform (see
   [Releases](https://github.com/hridesh-net/OpsIntelligence/releases)).
   Pin the version explicitly:
   `OPSINTELLIGENCE_VERSION=v0.2.5 bash install.sh` (adjust tag as
   needed).
2. **Avoid surprise downloads.** On restricted networks, the default
   installer may try to **clone the repo**, **pull Go from go.dev**, or
   **fetch a GGUF** — any of which can be blocked by policy or proxy.
   If you must enforce “binary-only, no extra fetches,” set:
   `NO_SOURCE_FALLBACK=1` and `OPSINTELLIGENCE_SKIP_GO_BOOTSTRAP=1`
   before running the script. Then either the release asset installs
   cleanly, or the install fails fast with a clear message (you supply
   the binary or an IT-approved toolchain yourself).
3. **If a source build is allowed**, install **Go** (same major version
   as `go.mod`), **git**, and on macOS **Xcode Command Line Tools**
   (`xcode-select --install`) **before** running the installer, then
   use `FORCE_BUILD=1` or let the 404 fallback build; no go.dev
   bootstrap is needed if `go` is already on `PATH`.
4. **Copy the binary yourself.** Alternative: download the release
   artifact on a machine that *can* reach GitHub, copy
   `opsintelligence` (and optionally bundled `skills/`) to the client,
   place it in `INSTALL_DIR`, run `chmod +x`, and scaffold config with
   `STATE_DIR` or the CLI — the installer is optional if you only need
   the binary + state directory layout.

Common environment toggles:

| Variable | Default | What it does |
|---|---|---|
| `OPSINTELLIGENCE_VERSION` | `latest` | Release tag to install |
| `INSTALL_DIR` | `/usr/local/bin` | Where the binary lands |
| `STATE_DIR` | `~/.opsintelligence` | Config + datastore root |
| `FORCE_BUILD=1` | — | Build from source even when a release binary exists |
| `NO_SOURCE_FALLBACK=1` | — | Disable the automatic source-build fallback that kicks in when the release asset 404s |
| `OPSINTELLIGENCE_SKIP_GO_BOOTSTRAP=1` | — | When a source build is needed but no system `go` exists, do not download the official Go tarball from go.dev (fail instead; for airgapped installs) |
| `OPSINTELLIGENCE_BOOTSTRAP_GO_VERSION` | `1.26.2` | Go version to download for bootstrap (must satisfy `go.mod`); override if go.dev layout changes |
| `SKIP_VENV=1` | — | Skip Python venv for the tool sandbox |
| `SKIP_SERVICE=1` | — | Skip launchd/systemd registration |
| `WITH_MEMPALACE=1` | — | Bootstrap managed MemPalace after install |
| `WITH_GEMMA=1` | — | Download the default Gemma GGUF for local-intel |

> **Note on pre-built binaries.** While OpsIntelligence is still a
> young fork, not every platform/version combination has a pre-built
> release asset yet. If the installer gets a `404` from
> `github.com/hridesh-net/OpsIntelligence/releases/...`, it will
> **automatically fall back to a source build** — no need to pass
> `FORCE_BUILD=1`. If you do not have Go installed, the installer
> **downloads the official Go toolchain tarball from go.dev** into a
> temp directory, builds once, then deletes the toolchain. Set
> `OPSINTELLIGENCE_SKIP_GO_BOOTSTRAP=1` to disable that (e.g. airgapped
> installs where you must supply a pre-installed Go). Set
> `NO_SOURCE_FALLBACK=1` if you specifically want the old
> binary-only behaviour (useful for airgapped mirrors).
>
> **Note on Gemma for local-intel.** `WITH_GEMMA=1 bash install.sh`
> (or `opsintelligence local-intel setup`) tries the OpsIntelligence
> release asset first, then transparently falls back to the
> AssistClaw release which ships the same `gemma-4-e2b-it.gguf`.
> That way `local-intel` works out of the box on a brand-new
> OpsIntelligence install even before our own release carries the
> GGUF. Override the URL explicitly via
> `OPSINTELLIGENCE_LOCAL_GEMMA_GGUF_URL=...` or `--url` to pin a
> specific source (e.g. an internal mirror) — doing so disables the
> fallback chain.

**Uninstall:**

```bash
bash uninstall.sh                      # remove binary + service, keep state
bash uninstall.sh --purge              # remove everything incl. ops.db
bash uninstall.sh --purge --keep-datastore  # wipe state but preserve users/RBAC
```

`--keep-datastore` is useful when moving OpsIntelligence to a new host:
your users, roles, API keys, and audit log stay behind for the next
install to pick up.

## Quick start

```bash
# 1. Install (see above) or build locally:
make build    # -> ./bin/opsintelligence

# 2. Onboard (writes ~/.opsintelligence/opsintelligence.yaml)
./bin/opsintelligence onboard

# 3. Seed your state directory with the example team
./bin/opsintelligence init    # drops teams/example-team/ policy files

# 4. Verify reachability before you run live
./bin/opsintelligence doctor

# 5. Start the daemon (Slack + gateway + cron + webhooks)
./bin/opsintelligence start
```

The first onboarding run collects: one LLM provider API key, optional
Slack tokens, optional GitHub / GitLab / Jenkins / SonarQube tokens,
and the active team name. Anything advanced (memory tiers, MCP
clients, cron, webhooks) can be edited either in
`~/.opsintelligence/opsintelligence.yaml` **or** from the dashboard —
see below.

See [`.opsintelligence.yaml.example`](.opsintelligence.yaml.example)
for the complete, commented reference — including copy-paste cron
heartbeats and webhook presets for GitHub/GitLab/Jenkins.

---

## Dashboard

Once the daemon is up, the dashboard is served out of the gateway
itself:

```
http://127.0.0.1:18790/dashboard/
```

The **first** visit walks you through creating the initial owner
account (datastore-backed, RBAC-protected). After that, the dashboard
gives you:

- **Overview** — running tasks, recent events, gateway health.
- **Tasks** — live event stream (SSE), per-task transcripts.
- **Users & Roles** — invite, edit, enable/disable, delete, and
  grant/revoke built-in roles (`owner`, `admin`, `operator`,
  `developer`, `auditor`, `viewer`). Self-delete and last-owner
  changes are guarded server-side. Backed by
  `/api/v1/users` + `/api/v1/roles`.
- **API keys** — mint (with name, expiry, scopes, and — for
  `apikeys.manage.all` — any user as owner), list, revoke. The
  plaintext `opi_<keyid>_<secret>` token is shown exactly once in a
  copy-to-clipboard dialog at mint time. Backed by
  `/api/v1/apikeys`. See
  [`doc/users-apikeys-api.md`](doc/users-apikeys-api.md) for the
  full permission matrix and request/response shape.
- **Settings** — full parity with the CLI: gateway bind + TLS, auth /
  OIDC, datastore, LLM providers, MCP clients, channels, webhooks,
  agent + DevOps guardrails. Every section uses optimistic concurrency
  (`If-Match`) so two admins editing in parallel never silently
  overwrite each other.

For cloud installs, flip `gateway.bind: lan` (or `0.0.0.0`), set
`gateway.tls.cert` + `gateway.tls.key`, and optionally wire OIDC under
`auth.oidc` — all of which can be done from the same Settings page
once an owner exists.

---

## Configuring a team

A **team** is a directory of Markdown files that tells OpsIntelligence
how *your* team wants to run DevOps. On startup, every `*.md` under
`teams/<active>/` is merged into the agent's system prompt.

```
~/.opsintelligence/teams/platform/
├── README.md
├── pr-review.md          # severity rubric, size limits, checks before ship
├── sonar.md              # quality-gate thresholds, false-positive policy
├── cicd.md               # required pipelines, flaky-test policy, rollback
├── secrets-and-safety.md # PII handling, token hygiene, owner approvals
└── runbooks/             # optional: runbook files the agent can execute
```

Start from the shipped [`teams/example-team/`](teams/example-team/)
templates, rename to your team, and edit. The agent will quote back to
you which policy a decision is grounded in.

---

## The DevOps skill graph

OpsIntelligence ships with a built-in skill graph under
[`skills/devops/`](skills/devops/) that the agent lazy-loads when it
needs it:

- [`SKILL.md`](skills/devops/SKILL.md) — the entry node / map of content.
- [`pr-review.md`](skills/devops/pr-review.md) — end-to-end review workflow.
- [`sonar.md`](skills/devops/sonar.md) — quality-gate & issue triage.
- [`cicd.md`](skills/devops/cicd.md) — pipeline monitoring across platforms.
- [`incidents.md`](skills/devops/incidents.md) — on-call triage support.
- [`runbooks.md`](skills/devops/runbooks.md) — safe runbook execution & authoring.

Copy the folder to `~/.opsintelligence/skills/devops/` (or point
`agent.skills_dir` at the repo path during development). Each node
is callable from the agent via `read_skill_node("devops", "<node>")`.

Alongside the skill graph, OpsIntelligence ships the standalone
[`gh-pr-review`](skills/gh-pr-review/SKILL.md) skill — the opinionated
how-to for reviewing a GitHub PR end-to-end: `gh pr checkout` into a
disposable worktree, run the repo's lint/test/build locally, post a
review through the GitHub Reviews API with line-level comments and
one-click ```suggestion``` blocks, and submit
Approve / Request-changes / Comment. Pair it with the `pr-review`
smart-prompt chain: the chain produces the verdict, `gh-pr-review`
posts it back.

---

## Smart prompts & prompt chaining

DevOps questions worth answering well rarely fit in one prompt. The
agent ships a curated library of **smart prompts** wired into named
**chains** the model can invoke via the `chain_run` tool — each chain
is a bounded, self-critiquing pipeline (gather → analyze → critique →
render). See [doc/smart-prompts.md](doc/smart-prompts.md) for the full
reference.

```
opsintelligence prompts ls                                     # list chains + prompts
opsintelligence prompts show pr-review                         # full chain spec
opsintelligence prompts run pr-review --input pr_url=https://…  # execute locally
```

Shipped chains: `pr-review`, `sonar-triage`, `cicd-regression`,
`incident-scribe`. Shipped meta prompts: `self-critique`,
`evidence-extractor`, `plan-then-act`. Every prompt is a Markdown file
you can override at `~/.opsintelligence/prompts/<id>.md`.

---

## Safety posture

OpsIntelligence treats production-adjacent systems with caution:

- **Read-only by default** on GitHub, GitLab, Jenkins, Sonar, and any
  MCP-connected platform.
- **Explicit-confirmation writes.** The agent may *prepare* a command
  (e.g. produce the `gh workflow run` invocation or the Jenkins URL),
  but a human types "yes, do it" in the same turn for the agent to
  proceed.
- **Owner-only paths.** `POLICIES.md`, `RULES.md`, and everything under
  `policies/` in the state directory are written by a human operator on
  disk. Any attempt by the agent to edit them via `write_file`, `edit`,
  `apply_patch`, or `bash` is blocked.
- **Secrets never in YAML.** Tokens live in environment variables
  referenced via `token_env:`. The doctor command checks that every
  referenced env var is set before the daemon starts.
- **PII hygiene.** Logs fetched from CI/CD or monitoring may contain
  user data. The agent summarizes, does not quote verbatim, and never
  echoes secrets it happens to see in a diff.

---

## Commands

```
opsintelligence onboard     # Interactive setup (writes the YAML)
opsintelligence init        # Create state_dir + seed templates
opsintelligence doctor      # Validate config + reachability
opsintelligence start       # Run daemon (Slack + gateway + cron + webhooks)
opsintelligence run "..."   # One-shot agent turn from the CLI
opsintelligence skills ls   # List installed skills
opsintelligence tools ls    # List registered tools (including devops.*)
opsintelligence prompts ls  # List smart-prompt chains & meta prompts
opsintelligence prompts run <chain> --input key=value
```

Run `opsintelligence <cmd> --help` for the full option list.

---

## Development

```bash
make build    # go build -tags fts5 ./cmd/opsintelligence
make test     # go test ./...
make lint     # gofmt + go vet
./bin/opsintelligence doctor --config .opsintelligence.yaml.example --skip-network
```

`go test ./internal/devops/...` exercises the GitHub, GitLab, Jenkins,
and SonarQube client contracts against `httptest` fixtures — no live
calls.

---

## License

MIT — see [LICENSE](LICENSE).
