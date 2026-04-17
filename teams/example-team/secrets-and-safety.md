# Secrets & safety — example team

- **Never** paste secrets, tokens, cookies, or `.env` contents into chat. If a
  user does, truncate to the first 4 chars and advise rotation.
- Tokens live only in environment variables referenced via `token_env:` in
  `opsintelligence.yaml`. Do not embed raw tokens in YAML.
- The agent is **read-only by default** on all DevOps surfaces in this team:
  PRs, pipelines, and Sonar. Any write action (approve, merge, retry,
  redeploy) requires explicit human confirmation in the same turn, e.g.
  `approve PR 123`, `retry pipeline 456`.
- PII: never include user emails, account numbers, or customer identifiers
  in agent output beyond what the source ticket already contains.
- Data handling: logs fetched from Jenkins/Actions/GitLab CI may contain
  customer data — summarize, do not quote verbatim, and strip obvious PII.
