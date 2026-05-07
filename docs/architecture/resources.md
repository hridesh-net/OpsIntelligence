# Resources & disk layout

## Disk (under `state_dir`)

[`dirs.Layout`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/dirs/layout.go) defines stable subdirectory names.

| Location | Purpose |
| -------- | ------- |
| `data/ops.db` | SQLite datastore (auth/RBAC/session/task history); default DSN from `applyDefaults`. |
| `data/memory/episodic.db`, `data/memory/semantic.db` | Agent episodic + semantic tiers; mining state `mining_state.json`. |
| `data/repointel/` | Repo intel working store: `repos.yaml`, `memory/` (artifacts + `repointel.db` hybrid index), `clones/` for shallow Git clones when used. |
| `data/localintel/` | Local Gemma cache (embedded GGUF materialization); configurable via `agent.local_intel.cache_dir`. |
| `logs/agent/runtrace.ndjson`, `logs/subagents/runtrace.ndjson` | Run traces when `run_trace_mode` is auto/on. |
| `logs/repointel/<sanitized-repo-id>/runtrace.ndjson` | Per-repo indexing traces when repo intel is enabled. |
| `logs/pipeline/` | Pipeline traces. |
| `logs/audit/audit.ndjson` | Security audit log default when `security.log_path` is empty. |
| `runtime/`, `runtime/channels/` | PID, cron persistence, DLQ. |
| `workspace/public/` | Agent-generated static assets served at `/workspace/` by the gateway. |

## CPU and RAM

| Workload | Notes |
| -------- | ----- |
| Cloud / remote LLM | Dominant cost during `Run`/`RunStream`: prompt size × iterations; tool rounds add passes. |
| Local APIs (Ollama, LM Studio, vLLM, …) | Same loop; inference runs on the serving host. |
| `opsintelligence_localgemma` build | In-process GGUF inference for advisory + optional smart routing: extra RAM for weights and decode; bounded by `agent.local_intel.max_tokens` and smart-routing defaults in code. |

## Network

Provider APIs; GitHub/GitLab/Jenkins/Sonar HTTP clients; MCP servers; Slack/Teams webhooks; embedding HTTP APIs (`internal/embeddings`); Git fetch/clone for repointel; optional Tailscale in [`internal/gateway/server.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/gateway/server.go).

## Embedding and hybrid index

Hybrid repo search uses SQLite FTS + vectors in **`memory_dir/repointel.db`** when indexing succeeds ([`internal/repointel/manager.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/repointel/manager.go)). Embedding calls occur during indexing and search when an embedder is configured.
