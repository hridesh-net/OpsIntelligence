---
name: runbooks
summary: "Safely execute or author an operational runbook; narrate each step and stop on uncertainty."
---

# Runbooks

Use this node when the user says "run the X runbook", "walk me through
the on-call checklist", or "document this recovery". Runbooks are
markdown files under `teams/<active>/runbooks/` — one file per
procedure.

## Executing a runbook

1. **Resolve the file.** If the user named one, match it
   case-insensitively under `teams/<active>/runbooks/`. If ambiguous,
   list candidates and ask.
2. **Print the whole runbook first**, then pause for a human "go". The
   human must see what you are about to do before you do it.
3. **Step through one step at a time.**
   - Read the step out loud (quote it verbatim).
   - State what the agent will do (tool name + arguments).
   - Wait for human "ok" unless the step is marked `auto: true` in its
     own frontmatter fence — and even then, only auto-proceed if the
     step is read-only (no write tools).
4. **Log every action** to the session log: step number, command,
   tool, stdout snippet (truncated), exit/status, human confirmation.
5. **Stop on any of:**
   - A step fails or returns an unexpected status.
   - The human says "wait" / "stop" / "hold on".
   - A step requires a credential or environment the agent cannot see.
   - The runbook asks for a judgment call (e.g. "decide whether to
     failover").

## Authoring a new runbook

When the user asks the agent to **document** a procedure:

1. Propose a filename: `teams/<active>/runbooks/<verb>-<subject>.md`.
2. Use this template and fill from the conversation + tool evidence:

```
---
title: <human name>
owner: <team or @handle>
severity: <SEV1 | SEV2 | SEV3 | routine>
last-verified: <YYYY-MM-DD>
---

# <Title>

## When to use this
<Single paragraph — the trigger.>

## Preconditions
- <env, access, tool X installed, …>

## Steps
1. **<verb + object>** — what and why.
   - Command: `…`
   - Expected output: `…`
   - If this fails: <fallback or escalation>.
2. …

## Verification
- <how you know the system is healthy again>

## Rollback
- <how to undo the steps above safely>

## Links
- Dashboards, tickets, prior incidents.
```

3. Do not overwrite an existing runbook without explicit approval.
   If the file exists, write the new version next to it as
   `<name>.draft.md` and ask the owner to review.

## Safety

- Never execute a **destructive** runbook step (drop table, delete
  branch, force-push, kill pod in prod) without the human typing a
  confirmation phrase in the same turn — the runbook itself should
  specify the phrase.
- When in doubt, stop and ask. Runbooks exist to eliminate ambiguity;
  if a step is ambiguous, the runbook is wrong, not the context.

---

Related: [[cicd]], [[incidents]], back to [[SKILL]].
