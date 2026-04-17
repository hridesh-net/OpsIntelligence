# Webhook Adapters

OpsIntelligence exposes a pluggable, typed contract for inbound action
webhooks. It lives in `internal/webhookadapter/` and mirrors the shape
of the channel-adapter contract in `internal/channels/adapter/` — but
where a channel adapter ferries conversational messages to and from
humans, a webhook adapter ingests *platform events* (GitHub PR opened,
GitLab pipeline failed, Datadog monitor alerted, …) and turns each one
into an agent run.

This document describes the framework itself. For configuring the
shipped GitHub adapter, see [github-webhooks.md](./github-webhooks.md).

## Why adapters, not a single generic handler?

The previous generic `webhooks.mappings` only substitutes top-level JSON
keys into a template, checks a shared secret header, and serves one
path per entry. That works for Zapier/n8n-style fire-and-forget
receivers but is a poor fit for platforms like GitHub, GitLab, or
Datadog, which all need:

- Provider-specific authentication (HMAC-SHA256, signed JWT, mTLS).
- Deeply nested payloads (`pull_request.user.login`,
  `workflow_run.conclusion`, `event.monitor.tags.…`).
- Event metadata in HTTP headers (`X-GitHub-Event`, `X-GitLab-Event`).
- Bounded concurrency so a burst of deliveries can't spawn unbounded
  goroutines.
- An async response so the platform's short delivery timeout never
  clashes with agent runs that take minutes.

An adapter encapsulates all of that per provider, and the shared
`webhookadapter.Router` handles the pieces that are always the same
(body cap, acceptance logging, semaphore, detached context, 202
response).

## Shape of an Adapter

```go
type Adapter interface {
    Name() string                                 // "github", "gitlab", …
    Path() string                                 // URL suffix under /api/webhook/
    Enabled() bool
    Verify(r *http.Request, body []byte) error    // HMAC/token/mTLS
    Parse(r *http.Request, body []byte) (Event, error)
    Filter(event Event) FilterResult
    Render(event Event) (prompt string, err error)
}
```

Every adapter goes through the same lifecycle on every request:

1. Router matches `Path()` → calls `Enabled()`.
2. Reads body (2 MiB cap), calls `Verify()`. Failure → 401.
3. `Parse(r, body)` → `Event`. Failure → 400.
4. `Filter(event)` → `FilterResult`.
   - `Allowed: true` → proceed.
   - `Allowed: false, Reason: "healthcheck:…"` → 204 No Content
     (platform liveness pings like GitHub's `ping`).
   - otherwise → 202 Accepted with `{"status":"skipped", …}`.
5. `Render(event)` → prompt. Failure → 500.
6. Acquire semaphore slot. Saturated → 503 + `Retry-After: 30`.
7. Respond 202 Accepted immediately; agent runs in a detached
   goroutine with the router-level timeout.

## Adding a new adapter

1. Create `internal/webhookadapter/<name>/` with a package that defines:
   - A typed `Config` struct.
   - An `Adapter` type with a `New(Config)` constructor.
   - Implementations of the seven `Adapter` methods.
   - A `_test.go` file exercising at minimum: `Verify` happy/sad path,
     `Parse` header extraction, `Filter` allowlist, and a nested-field
     `Render` test.
2. Add the typed config to `internal/config/config.go` under
   `WebhookAdapters` (so YAML parsing stays strict).
3. Wire it in `cmd/opsintelligence/main.go#buildWebhookAdapterRegistry`
   — read the typed config, skip if `!Enabled`, call `reg.Register`.
4. Point the adapter at `/api/webhook/<Path()>` in the platform's
   webhook configuration UI.

The router picks up the new adapter with zero gateway changes.

## Current adapters

| Adapter | Path                    | Verification        | Notes                                             |
| ------- | ----------------------- | ------------------- | ------------------------------------------------- |
| github  | `/api/webhook/github`   | HMAC-SHA256         | See [github-webhooks.md](./github-webhooks.md).   |

Planned: `gitlab` (token header + optional HMAC), `bitbucket`
(HMAC-SHA256), `datadog` (API-key header), `pagerduty` (signature
header). PRs welcome.

## Router behaviour (shared by all adapters)

Config lives under `webhooks:`:

```yaml
webhooks:
  enabled: true
  max_concurrent: 10   # shared across ALL adapters
  timeout: "10m"       # agent run ceiling per delivery
  adapters:
    github: { enabled: true, secret: "${…}", … }
```

- Requests that don't match any registered adapter path fall through
  to the legacy generic `webhooks.mappings` handler (which keeps the
  Bearer-token gate).
- Every adapter does its own request authentication; the generic token
  is **not** checked for adapter routes.
- Every accepted delivery logs `adapter`, `kind`, `action`,
  `delivery_id`, and `session_id` fields for tracing.
