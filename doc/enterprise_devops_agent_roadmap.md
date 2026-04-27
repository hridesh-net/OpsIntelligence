# Enterprise DevOps agent roadmap

This document records the **prioritized pillar** from the autonomous enterprise gap analysis and maps each gap category to **current OpsIntelligence surfaces** or **follow-up work**.

## Chosen pillar (first depth)

**Governance, audit, and bounded autonomy** — strengthens trust and blast-radius control before expanding runtime integrations (Kubernetes, ServiceNow, etc.). This ordering matches the gap analysis recommendation: it unlocks wider rollout under security review.

## Implemented in this iteration

| Gap theme | What shipped | Where |
|-----------|----------------|------|
| Immutable audit trail (correlation) | Tool audit rows may include `model_id` and `policy_bundle_hash` (still hash-chained; legacy rows unchanged). | [`internal/security/audit.go`](../internal/security/audit.go), [`internal/security/policy_fingerprint.go`](../internal/security/policy_fingerprint.go) |
| Policy snapshot for audits | `PolicyBundleFingerprint(stateDir)` hashes `POLICIES.md` + `teams/**/*.md` in stable path order. | [`internal/security/policy_fingerprint.go`](../internal/security/policy_fingerprint.go) |
| Bounded autonomy | Optional per–user-message cap on tool executions via YAML. | `agent.autonomy.max_tool_calls_per_turn` in [`internal/config/config.go`](../internal/config/config.go); enforcement in [`internal/agent/runner.go`](../internal/agent/runner.go) |

## Gap-to-surface mapping

### Governance, accountability, and trust

| Enterprise expectation | Today in OpsIntelligence | Next steps |
|------------------------|--------------------------|------------|
| Tamper-evident tool audit | NDJSON chain + `opsintelligence security verify` | Extend event types for admin/config (see sprint story STORY-063 under `doc/Sprints/`) |
| Who approved writes | Human-in-loop prompts; channel actor on some paths | Thread explicit `actor` into audit on all gateways |
| Machine-checkable policy (OPA) | Markdown under `teams/*` + `POLICIES.md` | Optional Rego bundles + CI gate |
| Risk tiers / blast radius | Read-only-by-default; **new** optional `max_tool_calls_per_turn` | Tier tools by risk class; per-tool daily quotas |
| Break-glass | Not first-class | Reason codes + time-boxed elevated tokens |

### Evidence and correctness

| Enterprise expectation | Today | Next steps |
|------------------------|-------|------------|
| Incident bundle artifact | Episodic memory + `devops.*` tools + optional run trace NDJSON | Named “bundle” export with stable id + linked sources |
| Time alignment / causality discipline | Correlation IDs in observability | Clock-skew notes + stricter “cite or abstain” in prompts |
| Deterministic replay | Run trace + audit hashes | Store frozen tool payloads for replay (privacy-sensitive) |

### Platform depth

| Enterprise expectation | Today | Next steps |
|------------------------|-------|------------|
| K8s / cloud runtime | DevOps tools: GitHub/GitLab/Jenkins/Sonar | Optional cluster read-only integrations |
| Change management (PagerDuty, Jira Ops) | Slack/Telegram/Discord channels | Webhooks + state machine for escalation |
| Secrets lifecycle | Env-based tokens; docs in README | Vault / OIDC workload identity |

### Multi-tenant and fleet

| Enterprise expectation | Today | Next steps |
|------------------------|-------|------------|
| Tenant isolation | Single `state_dir` deployment | Partitioned state + per-tenant credentials |
| Fleet rollout / kill switch | `agent.enterprise` posture flag | Canary autonomy modes + org-wide policy distribution |

### Human partnership

| Enterprise expectation | Today | Next steps |
|------------------------|-------|------------|
| Structured options A/B/C | Model-dependent | Prompt + chain templates for incident handoff |
| Approved “lessons learned” | Semantic memory + reflection tags | Human approval queue before promotion |

## Related code pointers

- Enterprise system prompt posture: `enterprisePosturePrompt` in [`internal/agent/runner.go`](../internal/agent/runner.go) when `agent.enterprise: true`.
- Agent config defaults: [`internal/config/config.go`](../internal/config/config.go) `applyDefaults`.
- SQL audit (gateway): [`internal/datastore/types.go`](../internal/datastore/types.go) vs file-based security audit (tool calls): keep both concepts distinct in compliance docs.

## Configuration snippet

```yaml
agent:
  enterprise: true
  autonomy:
    max_tool_calls_per_turn: 40   # 0 = unlimited (default)
```

Sub-agents inherit the same cap from the parent process configuration.
