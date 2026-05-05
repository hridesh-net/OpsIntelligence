# Cloud architecture review (read-only)

Use this flow when the operator asks for an **architecture review**, **cost posture**, or **audit trail** assessment across AWS, Azure, or GCP.

## Inputs to gather

1. **Inventory** — `devops.cloud.inventory` with `provider` and optional `regions`, `tag_filters`, `resource_group` (Azure), `max_resources`.
2. **Cost** — `devops.cloud.cost_summary` with `start` / `end` (YYYY-MM-DD) and `granularity` (`DAILY` or `MONTHLY` where supported).
3. **Audit** — `devops.cloud.audit_events` with a **narrow** time window (hours to a few days) to avoid huge payloads.

Always respect configured **scopes** in `devops.cloud.*` YAML; do not broaden scope in tool calls unless the user explicitly asks.

## Analysis checklist (evidence-based)

- **Data plane vs control plane**: cite audit events for risky changes (IAM, security groups/firewalls, public exposure, key rotation).
- **Blast radius**: highlight shared networks, open ingress, cross-region dependencies from inventory tags and types.
- **Cost**: call out top services or spikes; note GCP linkage-only output when detailed billing is not available.
- **Gaps**: if APIs return empty or permission errors, say what role is missing (see `doc/cloud-devops-iam.md`) instead of guessing.

## Output format

1. **Executive summary** (3–6 bullets).
2. **Findings** — severity (high/medium/low), evidence (resource id or event), recommendation (operational, not auto-applied).
3. **Next steps** — optional deeper checks (e.g. narrow tag, single region, shorter audit window).

Stay **read-only**: never instruct the agent to mutate cloud resources without explicit human confirmation in policy.
