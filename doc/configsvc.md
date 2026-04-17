# configsvc (Phase 3a)

`internal/configsvc` is the shared configuration mutation layer for OpsIntelligence.

It exists so both surfaces:

- CLI commands (`opsintelligence mcp ...`, `opsintelligence skills ...`, etc.)
- Dashboard/API handlers (`/api/v1/*` in phase 3b)

write through one consistent path with the same validation and file semantics.

## Responsibilities

- Resolve config path (`--config` or default `~/.opsintelligence/opsintelligence.yaml`).
- Load validated config via `internal/config.Load`.
- Apply mutators on typed `*config.Config`.
- Persist atomically (`write temp` + `rename`) with `0600` permissions.
- Provide revision tokens (`mtime:size`) for optimistic concurrency in API handlers.

## Current Operations

- Generic:
  - `Read(ctx)` -> `Snapshot{Config, Revision}`
  - `Update(ctx, mutate)`
  - `UpdateWithRevision(ctx, expectedRevision, mutate)` -> `ErrRevisionConflict`
- Typed helpers (initial set):
  - `SetSkillEnabled`
  - `AddMCPClient`, `RemoveMCPClient`
  - `SetGateway`, `SetAuth`, `SetDatastore`
  - `SetProviders`, `SetChannels`, `SetWebhooks`, `SetMCP`, `SetAgent`, `SetDevOps`

## Integration Status

- `opsintelligence mcp add/remove` now uses `configsvc`.
- `opsintelligence skills enable/disable` (and install/add/remove paths that toggle skills) now use `configsvc`.

Next phase (3b) will wrap these methods behind RBAC-gated REST endpoints.
