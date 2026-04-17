# Supervised Sub-Agents

OpsIntelligence is architected as a **master agent with supervised
children**, not a flat single-agent loop. The master runs the main
session (voice / Slack / webhook-initiated); when a task decomposes
into independent pieces — "review 3 PRs in parallel", "check Sonar
AND the pipeline AND the flaky test", "triage these 5 alerts" — the
master spawns **sub-agent tasks** that each run in their own isolated
context, in parallel, and report back.

This page documents how that model works, what guarantees it gives you,
and the tool surface each side exposes.

## Core principles

1. **Isolation by default.** Every sub-agent task runs with its own
   session id, its own workspace, its own working memory, and its own
   toolset (parent tools minus recursion hazards and master-only
   supervisory controls). Running three sub-agents at once does not
   mix their contexts.
2. **Opt-in sharing.** When the master genuinely wants a child to see
   a piece of context that wasn't in the original task prompt, it
   calls `subagent_share_context` (for an audit note) and/or
   `subagent_intervene` (to push authoritative guidance that the
   child *will* read on its next iteration).
3. **Ambient oversight.** The master doesn't have to remember to poll.
   On every master iteration, a compact dashboard of active
   sub-agents is auto-injected into its system prompt: task id,
   current status, elapsed, goal, last progress event, pending
   interventions. If there are no active tasks the block is omitted.
4. **Bounded concurrency.** The `TaskManager` caps concurrent
   sub-agent tasks (default 8) and bounds per-task retention (default
   256) and event-log length (default 128 events per task). There is
   no way to spawn grand-children: the child's tool registry is
   cloned from the parent's without `subagent_*` tools.

## Lifecycle of a task

1. Master calls `subagent_run_async(id, task)` (or the `_parallel`
   fan-out wrapper). The `TaskManager` assigns a `task_id` and
   queues it.
2. The TaskManager picks up the task on a goroutine, spawns a child
   runner with its own session, and invokes `subSvc.runSyncWithTask`.
3. The child runs normally. On each of its iterations:
   - Pending interventions are **drained** from the TaskManager and
     appended to the child's system prompt as a `## SUPERVISOR
     GUIDANCE` block. The child is expected to obey immediately.
   - The child may call `supervisor_report(message, phase?, kind?)`
     to post a `ProgressEvent` back to the TaskManager. The master
     sees this in its dashboard next turn.
4. When the child returns, the TaskManager records the final
   `Status`, `Result`, `Error`, and `Iterations`, then releases its
   concurrency slot and evicts old terminal tasks if over retention.
5. Lifecycle transitions (`dispatched`, `task started`, `task
   completed|failed|cancelled`) are themselves recorded as events of
   kind `lifecycle`, so the event stream is a full audit trail.

## Tool surface

### Master-only

| Tool                    | Purpose                                                                     |
| ----------------------- | --------------------------------------------------------------------------- |
| `subagent_create`       | Define a named specialist (name + instructions written to SOUL.md).         |
| `subagent_list`         | List registered specialists.                                                |
| `subagent_remove`       | Unregister a specialist and (by default) delete its workspace.              |
| `subagent_run`          | Blocking call — use only for one-shot work where parallelism isn't wanted.  |
| `subagent_run_async`    | Fire-and-forget: returns a `task_id`.                                       |
| `subagent_run_parallel` | Fan-out N tasks and wait on all.                                            |
| `subagent_tasks`        | List recent tasks (status + elapsed).                                       |
| `subagent_status`       | Full snapshot of one task (status, result, error, iterations).              |
| `subagent_wait`         | Block until listed ids are terminal (with timeout).                         |
| `subagent_cancel`       | Cancel a pending or running task.                                           |
| `subagent_intervene`    | Push authoritative guidance; child reads it on its next iteration.          |
| `subagent_stream`       | Drain the `ProgressEvent` stream for one task (or all active).              |
| `subagent_share_context`| Record an audit-trail context-share note.                                   |
| `subagent_read_context` | Read back the audit trail of shared notes.                                  |

### Child-only

| Tool                | Purpose                                                              |
| ------------------- | -------------------------------------------------------------------- |
| `supervisor_report` | Post a `ProgressEvent` back to the master (kind = progress, blocked, error). |

The child's tool is pre-bound to its own `task_id`, so there is no way
for one child to report against another's task.

## When to use each

- **One-off synchronous work** (small, quick, no need for parallelism)
  → `subagent_run`.
- **Two or more independent investigations** (review PR + check Sonar
  + check CI) → `subagent_run_parallel`, then read the combined
  summary. The master's prompt will automatically show per-task
  progress while they run.
- **Long-running work you want to supervise** → `subagent_run_async`,
  then watch the dashboard. If you notice a child heading in the wrong
  direction, call `subagent_intervene(task_id, guidance)`. If it
  reports `kind=blocked`, decide and intervene or cancel.
- **Fresh context mid-run** (e.g. you learned something after the
  child started) → `subagent_intervene` is usually the right call;
  `subagent_share_context` alone is purely audit-trail.

## What this is *not*

- It is **not** an event-driven wake-up system — the master sees state
  on its own turns, not via interrupts. This keeps the runtime simple
  and predictable. If you need truly real-time orchestration, open an
  issue.
- Sub-agents **cannot** spawn their own sub-agents. The tool registry
  is cloned without `subagent_*` for children to avoid runaway cost.
- Shared context is **not** automatically propagated. Isolation is the
  default; the master must explicitly opt in per piece of context.
