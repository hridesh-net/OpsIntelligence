# GitHub Webhook Integration

OpsIntelligence ships a first-class GitHub webhook **adapter** — a
typed plugin in the `webhookadapter` registry (see
[webhook-adapters.md](./webhook-adapters.md) for the framework itself).
It is the recommended way to wire a repository or organisation webhook
to the agent: every push, PR, or CI event automatically starts an agent
run (optionally fanning out to sub-agents), without the caller paying
for top-level-key-only template substitution or a blocking agent
invocation.

## Why a dedicated handler?

The generic `/api/webhook/{path}` endpoint was designed for simple,
shallow payloads. GitHub deliveries are none of those things:

- Payloads are deeply nested (`pull_request.user.login`,
  `workflow_run.conclusion`, `check_suite.head_sha`), so top-level-key
  substitution is insufficient.
- GitHub authenticates deliveries with an HMAC-SHA256 signature in the
  `X-Hub-Signature-256` header, not a custom token.
- Real event type lives in the `X-GitHub-Event` HTTP header — it is
  *not* in the JSON body for most events.
- GitHub enforces a ~10-second delivery timeout; agent runs routinely
  take minutes, so the response must be asynchronous.

The dedicated handler solves each of these.

## Endpoint

```
POST /api/webhook/github          (default; override via webhooks.adapters.github.path)
```

## Configuration

```yaml
webhooks:
  enabled: true                    # webhooks subsystem must be on
  max_concurrent: 10               # shared across ALL adapters
  timeout: "10m"                   # shared per-run agent timeout
  adapters:
    github:
      enabled: true
      secret: "${OPSINTEL_GITHUB_WEBHOOK_SECRET}"   # REQUIRED
      path: "github"               # full URL suffix → /api/webhook/github
      events:                      # (event, action) allowlist
        pull_request:        [opened, reopened, synchronize, ready_for_review, edited]
        pull_request_review: [submitted]
        workflow_run:        [completed]
        check_suite:         [completed]
        issues:              [opened, reopened]
        push:                []    # event has no action field
    prompts:
      pull_request: |
        PR {{.action}} on {{.repository.full_name}}#{{.pull_request.number}}
        — "{{.pull_request.title}}" by @{{.pull_request.user.login}}.
        Run `chain_run id="pr-review"`; fan out Sonar / pipeline checks
        with `subagent_run_parallel` if useful.
      workflow_run: |
        {{.workflow_run.name}} → {{.workflow_run.conclusion}} on
        {{.repository.full_name}} ({{.workflow_run.head_branch}}).
        On failure run `chain_run id="cicd-regression"`.
    # allow_unverified: true        # LOCAL TESTING ONLY — disables HMAC
    # default_prompt: "..."         # fallback when no event-specific prompt set
```

## Environment variables

```bash
OPSINTEL_GITHUB_WEBHOOK_SECRET=<the-same-secret-you-set-in-github-webhook-ui>
```

The example `.env.example` and `.opsintelligence.yaml.example` both
reference this variable.

## Setting up the webhook in GitHub

1. Repo (or Org) → **Settings → Webhooks → Add webhook**.
2. **Payload URL:** `https://<your-host>/api/webhook/github`
3. **Content type:** `application/json`
4. **Secret:** the value of `OPSINTEL_GITHUB_WEBHOOK_SECRET`
5. **SSL verification:** enabled (recommended).
6. **Which events:** select *Let me select individual events* and
   pick the ones that match your `events:` allowlist (PR, issues,
   workflow_run, check_suite, push, …). Selecting too many is
   fine — the handler will skip unlisted events with 202 Accepted.
7. Save. GitHub will immediately send a `ping` event which the handler
   acknowledges with 204 No Content, confirming reachability.

## How a delivery flows

1. GitHub POSTs the event. The handler reads up to 2 MiB of body.
2. HMAC signature is verified with a constant-time compare. Any
   mismatch returns 401 Unauthorized (and is logged).
3. The `ping` event returns 204 immediately. Other events are matched
   against `events[X-GitHub-Event]` and, if present, its action list.
   Skipped events return 202 Accepted with `{"status":"skipped", …}` so
   GitHub considers them successfully delivered.
4. The configured (or default) Go `text/template` prompt is rendered
   against the full parsed JSON payload. See
   [Prompt templates](#prompt-templates) below for the variables
   exposed.
5. A slot is acquired in a bounded semaphore (`max_concurrent`). If
   full, the handler responds 503 Service Unavailable with a
   `Retry-After: 30` header — GitHub then retries with backoff. This
   is safer than unbounded goroutine fan-out.
6. The handler spawns a background goroutine, detaches from the
   request context (but keeps correlation fields), applies `timeout`,
   and invokes the configured `Runner`. The HTTP response is 202
   Accepted with `{"delivery_id","session_id","event","action"}`.
7. When the agent finishes, the slot is released. Failures are logged
   with `zap.Error`. There is no automatic GitHub callback.

## Prompt templates

Templates are Go `text/template` with `missingkey=zero`. They receive
the full parsed JSON payload as the root context, **plus** these
injected keys:

| Key              | Meaning                                                      |
| ---------------- | ------------------------------------------------------------ |
| `.event`         | Value of `X-GitHub-Event` (e.g. `pull_request`).             |
| `.delivery_id`   | Value of `X-GitHub-Delivery` (GUID; useful for log grepping).|
| `.action`        | `payload.action` when present, else `""`.                    |
| `.payload_keys`  | Comma-separated top-level JSON keys (debug aid).             |

Nested fields work as you'd expect:

```gotemplate
{{.repository.full_name}}                   → acme/widgets
{{.pull_request.number}}                    → 42
{{.pull_request.user.login}}                → alice
{{.workflow_run.conclusion}}                → success | failure | …
{{.check_suite.head_sha}}                   → <40-char SHA>
```

Template resolution order: `prompts[<event>]` → `default_prompt` →
built-in `DefaultGitHubPrompt` (which asks the agent to pick a DevOps
chain based on the event).

## Selecting events

`events:` is a map from event name (the header value) to an
allow-list of `payload.action` strings.

- **Empty map** → every event is allowed through.
- **Event present, empty list** → any action for that event passes
  (useful for events without an action field, like `push`).
- **Event missing** → the delivery is skipped with 202 Accepted.
- String match is **case-insensitive**.

Common configurations:

```yaml
events:
  # Minimal: only PR lifecycle.
  pull_request: [opened, reopened, synchronize, ready_for_review]

  # Plus CI results.
  workflow_run: [completed]
  check_suite: [completed]

  # Plus any push event (no action field).
  push: []
```

## Concurrency & timeouts

`max_concurrent` and `timeout` are **router-level** settings
(`webhooks.max_concurrent`, `webhooks.timeout`) shared by every adapter —
they live on the shared `webhookadapter.Router`, not per-adapter. Each
delivery consumes one slot from the in-memory semaphore (default 10).
When saturated, further deliveries return 503 with `Retry-After: 30` and
the sending platform retries automatically.

`timeout` (default 10m) is the per-delivery agent ceiling. The runner
context is detached from the HTTP request — closing the connection does
not cancel the run — but it *is* cancelled when the timeout elapses.

If you expect high event volumes, combine this with async sub-agent
tools so the master run returns quickly and dispatches long work in
background tasks:

```gotemplate
{{- /* inside prompts.pull_request */ -}}
Dispatch with `subagent_run_parallel` to review in parallel:
- tasks: [
    {id: "reviewer", task: "...review the diff..."},
    {id: "sonar",    task: "...check Sonar regressions..."},
    {id: "ci",       task: "...inspect pipeline run..."}
  ]
Wait for all, then post the combined verdict.
```

This composes the new async sub-agent layer (see
`doc/smart-prompts.md`) with the webhook handler: every PR event can
fan out three specialist runs in parallel, each capped by the
sub-agent TaskManager's own concurrency settings.

## Local testing

Use [smee.io](https://smee.io/) or `ngrok http 7878` to expose the
gateway, then POST to the tunnel URL. For *very* local testing (no
internet), you can set `allow_unverified: true` and hit the endpoint
directly:

```bash
curl -sS -X POST http://localhost:7878/api/webhook/github \
  -H 'Content-Type: application/json' \
  -H 'X-GitHub-Event: pull_request' \
  -H 'X-GitHub-Delivery: test-1' \
  -d '{"action":"opened","repository":{"full_name":"local/repo"},"pull_request":{"number":1,"title":"WIP","user":{"login":"me"}}}'
```

Disable `allow_unverified` before deploying anywhere reachable from the
public internet.

## Observability

Every accepted delivery logs:

```
gateway inbound request   method=POST path=/api/webhook/github
github webhook accepted   event=pull_request action=opened delivery_id=... session_id=webhook:github:pull_request:<uuid>
github webhook agent run completed   event=pull_request ...
```

The `session_id` is also the memory session id — you can cross-reference
with `opsintelligence sessions show <session_id>` to replay the full
agent transcript of a delivery.
