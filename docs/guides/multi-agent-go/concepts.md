# Core Concepts

## The agent loop

Every autonomous agent follows the same cycle:

```
Observe → Plan → Act → Reflect → (repeat)
```

1. **Observe** — read the current state (messages, environment, tool results)
2. **Plan** — decide what to do next (reasoning, goal decomposition)
3. **Act** — call a tool or emit a final answer
4. **Reflect** — evaluate whether the action moved toward the goal

In Go, this is a `for` loop with a hard iteration cap to prevent infinite runs.

## Tools

Tools are functions the model can invoke. Each tool has:

- **Name** — what the model calls
- **Description** — when to use it
- **Schema** — JSON Schema for arguments
- **Handler** — Go function that executes the call

The model emits a `tool_call` message; your code parses it, runs the handler, and feeds the result back as a `tool_result` message.

## Memory tiers

| Tier | Scope | Storage |
|------|-------|---------|
| **Working** | Current conversation | In-memory slice |
| **Episodic** | Past agent runs | SQLite / Postgres |
| **Semantic** | Domain knowledge (code, docs) | Vector DB or in-memory embeddings |

## Planning

Before acting, the agent can generate a plan:

```
Goal: "Review PR #123"
Plan:
1. Fetch PR diff from GitHub
2. Analyze for bugs and style issues
3. Check test coverage
4. Post review comment
```

The plan is stored in working memory and updated as steps complete.

## Reflection

After each tool call, the agent evaluates:

- Did the tool return what I expected?
- Am I closer to the goal?
- Should I revise the plan?

Reflection prevents agents from getting stuck in loops or pursuing dead ends.
