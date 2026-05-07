# Observability

## Run traces

When **`run_trace_mode`** is auto or on, the runtime writes append-only **NDJSON** run traces (paths resolved under the state directory). Defaults are applied in [`applyAgentRunTrace`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/config/config.go).

Typical locations include:

| Trace | Location (conceptual) |
| ----- | --------------------- |
| Agent | `logs/agent/runtrace.ndjson` |
| Sub-agents | `logs/subagents/runtrace.ndjson` |
| Repo intel (per repo) | `logs/repointel/<sanitized-repo-id>/runtrace.ndjson` |

Implementation detail: [`internal/observability/runtrace`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/observability/runtrace).

Local Gemma / advisory fields (`local_intel_enabled`, `local_advisory_applied`, backend classification) are recorded via [`runtrace.InferBackend`](https://github.com/hridesh-net/OpsIntelligence/blob/main/internal/observability/runtrace/runtrace.go).

## OpenTelemetry tracing

Optional OTLP tracing covers gateway, channel, and runner paths — **off by default**.

Minimal configuration:

```yaml
tracing:
  enabled: true
  otlp_endpoint: "localhost:4317"
  service_name: "opsintelligence"
  sample_ratio: 0.01
```

Full operator runbook (sampling, span names, Jaeger quickstart, failure behavior): [`doc/runbooks/opentelemetry-tracing.md`](https://github.com/hridesh-net/OpsIntelligence/blob/main/doc/runbooks/opentelemetry-tracing.md).

## Logs and audit

Pipeline traces may appear under **`logs/pipeline/`**. Security audit logging defaults to **`logs/audit/audit.ndjson`** when `security.log_path` is empty — see [Security](security.md).

## Related docs

- Load-test / baseline notes: [`doc/observability/`](https://github.com/hridesh-net/OpsIntelligence/tree/main/doc/observability)  
- Doctor validation runbook: [`doc/runbooks/doctor-config-validation.md`](https://github.com/hridesh-net/OpsIntelligence/blob/main/doc/runbooks/doctor-config-validation.md)
