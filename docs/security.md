# Security

## Posture

- **Read-first** integrations; writes require explicit human confirmation in the same conversational turn when relevant.  
- **Operator-owned policy files** on disk (`POLICIES.md`, `RULES.md`, `policies/`) are not editable by the agent through file tools.  
- **Secrets** live in environment variables referenced from YAML (`token_env:`), not committed values. **`doctor`** checks referenced vars before **`start`**.  
- **PII-aware summaries** — minimize verbatim quoting from CI logs or diffs; never echo secrets seen in content.

OpsIntelligence is **not** an auto-deploy bot. Posting PR comments requires explicit DevOps/GitHub configuration and appropriate PAT scopes.

## Audit logging

Security-relevant tool and skill events can flow through **`Runner.WithSecurity`** ([`internal/security`](https://github.com/hridesh-net/OpsIntelligence/tree/main/internal/security)). Audit path resolution is wired from startup (`cmd/opsintelligence/main.go`); default audit log path aligns with **`logs/audit/audit.ndjson`** when unset in config.

## Tokens and scopes

Configure only integrations your security team approves. Production defaults center on **Slack** and the **REST/WebSocket gateway**; other channel adapters may appear as commented stubs in examples.

## Related material

- Team policy template: [`teams/example-team/secrets-and-safety.md`](https://github.com/hridesh-net/OpsIntelligence/blob/main/teams/example-team/secrets-and-safety.md)  
- Enterprise-oriented stories and checklists remain under [`doc/Sprints/`](https://github.com/hridesh-net/OpsIntelligence/tree/main/doc/Sprints) for historical planning context.
