# Local intel (Gemma)

Optional **on-device advisory** and **smart routing** via embedded Gemma when built with the local engine tags.

## Configuration

[`LocalIntelConfig`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/config/config.go): `enabled`, `gguf_path` (or `OPSINTELLIGENCE_LOCAL_GEMMA_GGUF`), `cache_dir` (default `data/localintel`), `smart_routing`, token limits, optional `system_prompt`.

## Build tags

[`internal/localintel/doc.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/localintel/doc.go): default binaries **omit** the engine (`CompiledWithLocalGemma()` false). **`opsintelligence_localgemma`** links llama.cpp/gollama; **`opsintelligence_embedlocalgemma`** embeds weights at build time. Stub CLI message: [`localgemma_stub.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/cmd/opsintelligence/localgemma_stub.go).

## Advisory path

[`runner_localintel.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/agent/runner_localintel.go): when `localIntelPresent()`, Gemma fills `localIntelScratch`, merged into the system prompt as “On-device advisory” (`buildSystemPrompt` in `runner.go`). If the engine fails to open, features **no-op** with logged warnings.

## Smart routing path

[`runner_localintel_routing.go`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/agent/runner_localintel_routing.go): optional second Gemma completion proposing `TOOLS:` and `SKILLS_FOCUS:` lines; validated tool ids feed routing hints. **ToolGraph + catalog still apply**; Gemma hints are merged, not a replacement.

## Observability

Runtrace records `local_intel_enabled`, `local_advisory_applied`, and backend classification via [`runtrace.InferBackend`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/observability/runtrace/runtrace.go).

Operator-facing notes on installer toggles and mirrors: [Install](../install.md).
