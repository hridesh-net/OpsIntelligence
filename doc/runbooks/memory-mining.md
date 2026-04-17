# Memory Mining Runbook

This runbook covers OpsIntelligence’s **built-in** markdown → sqlite-vec indexing (`opsintelligence memory mine …`). That is separate from the **[MemPalace](https://github.com/MemPalace/mempalace)** project, which has its own CLI (`mempalace mine`, ChromaDB, MCP server). To use MemPalace inside OpsIntelligence, see [mempalace-rollout-rollback.md](mempalace-rollout-rollback.md).

## Commands

- Validate config and embedder readiness:
  - `opsintelligence memory mine validate`
- Run incremental mining:
  - `opsintelligence memory mine run`
- Run full backfill (explicit confirmation required):
  - `opsintelligence memory mine backfill --yes`
- Check last run status:
  - `opsintelligence memory mine status`

## Recommended Rollout

1. Keep `agent.palace.enabled: false`.
2. Run `opsintelligence memory mine validate`.
3. Run `opsintelligence memory mine run --dry-run`.
4. Run `opsintelligence memory mine run`.
5. Confirm `opsintelligence memory mine status` shows low/zero errors.

## Troubleshooting

- `no embedding provider available for mining`:
  - Configure at least one `embeddings.priority` provider and credentials.
- Frequent indexing errors:
  - Reduce `memory.mining.max_files_per_run` and rerun.
- Large files skipped:
  - Increase `memory.mining.max_file_size_kb` or split files.
