# Repo intelligence internals

Implementation lives in [`internal/repointel`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/repointel). Operator-facing summary: [Repo intelligence](../repo-intelligence.md).

## Components

| Type | File | Role |
| ---- | ---- | ---- |
| **Registry** | [`registry.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/repointel/registry.go) | YAML-backed repo list and status fields (memory/scan/call graph paths). |
| **Indexer** | [`indexer.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/repointel/indexer.go) | Fetches snapshot (API and/or [`cloner.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/repointel/cloner.go) shallow clone/pull under `ClonesDir`), produces `RepoMemory` and raw files. |
| **Scanner** | [`scanner.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/repointel/scanner.go) | Risk/CVE-style scan via configured LLM router. |
| **Manager** | [`manager.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/repointel/manager.go) | Serial pipeline per repo: index → scan → markdown refs → **call graph** → hybrid index → full-tree index → semantic RAG mirror when embedder + semantic store are wired. Queue + polling intervals from `ManagerConfig.applyDefaults`. |

## Artifacts

Under configured `memory_dir`, expect filenames such as `*-memory.json`, `*-scan.json`, `*-callgraph.json`, `*-symbols.json`, `*-callgraph.html`, `repointel.db`, `progress.json`. Paths are relative filenames on each `RepoEntry`.

## Clones

[`cloner.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/repointel/cloner.go): depth-1 clone/pull when clone-based fetching is needed; default clone root `layout.RepoClones` → **`data/repointel/clones`** (wired from [`cmd/opsintelligence/main.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/cmd/opsintelligence/main.go)).

## Call graph

- **Build** — [`BuildCallGraph`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/repointel/callgraph.go) reads `RawFile` contents from indexing; regex/language-aware defs and call edges; JSON via `SaveCallGraph`, optional HTML via `ExportGraphHTML`.
- **Registry** — Manager sets `CallGraphFile` on the repo entry (`buildCallGraph` in [`manager.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/repointel/manager.go)).
- **HTTP** — `GET /api/v1/repos/{id}/callgraph` and `/symbols` ([`repos_api.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/gateway/repos_api.go)).
- **TUI** — Graph tab loads JSON from `memory_dir` ([`repos_tui.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/cmd/opsintelligence/tui/repos_tui.go)).
- **Dashboard** — same APIs; optional `repo_intel.show_callgraph_library_packages` toggles library/package visibility in JSON responses.
