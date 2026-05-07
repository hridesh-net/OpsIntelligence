# Repo intelligence

Repo Intelligence is **optional**. Enable **`repo_intel`** in config (GitHub PAT, memory dir, optional embedder), then register repositories via **`opsintelligence repos add …`** or the dashboard.

## What each sync does

Each sync typically:

1. Fetches a bounded snapshot of the tree for LLM analysis and artifacts.  
2. Builds a **call graph** and symbol index.  
3. Optionally indexes a large slice of the repo into a **hybrid store** (FTS + vectors) for scoped search and agent RAG.

The dashboard exposes **Scan**, **Index memory**, **Call graph**, and **Ask repo** (natural language / keyword search over that index).

## Large trees

Very large GitHub trees may return `truncated: true`; the UI and API surface a warning so search may be partial.

## CLI

```bash
opsintelligence repos list | add | sync | status | users | tui
```

## Internals

Pipeline stages, artifact filenames, and clone behavior are documented for contributors in [Architecture — Repo intelligence](architecture/repo-intelligence.md).
