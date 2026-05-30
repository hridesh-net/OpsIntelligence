package kanban

import (
	"fmt"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// Slash commands wrap the user-visible prompt before it reaches the agent.
// They're intentionally minimal templates — the actual work is left to the
// model. Match kanbots.dev's `/spec`, `/review`, `/split` semantics.
//
// Future commands can be added by extending the switch in applySlashCommand.

// applySlashCommand returns the prompt with the slash directive applied.
// An unknown or empty command is a no-op (prompt is returned unchanged).
func applySlashCommand(prompt, cmd, args string, card *datastore.BoardCard) string {
	cmd = strings.TrimSpace(strings.TrimPrefix(cmd, "/"))
	if cmd == "" {
		return prompt
	}
	switch strings.ToLower(cmd) {
	case "spec":
		return slashSpec(prompt, card)
	case "review":
		return slashReview(prompt, card)
	case "split":
		return slashSplit(prompt, args, card)
	default:
		// Unknown command: pass through unchanged so the agent can still
		// see what the user typed.
		return prompt
	}
}

func slashSpec(prompt string, card *datastore.BoardCard) string {
	return fmt.Sprintf(
		`SLASH COMMAND: /spec

Produce a concise implementation spec for the following card BEFORE writing any code. The spec must include:
  1. A one-paragraph problem statement.
  2. The user-visible behavior change.
  3. The files / modules you'll touch (with rationale).
  4. Trade-offs you considered and rejected.
  5. The verification plan (what command or test confirms the change).

Stop after writing the spec. Do not edit files. The operator will review and explicitly approve before you start coding.

— Card —
%s`,
		prompt,
	)
}

func slashReview(prompt string, card *datastore.BoardCard) string {
	return fmt.Sprintf(
		`SLASH COMMAND: /review

Review the existing work on this card's branch (NOT the main branch). Output:
  1. A summary of what the previous run(s) changed.
  2. Concrete defects you found (with file:line citations).
  3. Suggested follow-ups in priority order.
  4. A go/no-go recommendation for merge.

Do not make further code changes. The operator will dispatch a fix run if needed.

— Card —
%s`,
		prompt,
	)
}

func slashSplit(prompt, args string, card *datastore.BoardCard) string {
	target := strings.TrimSpace(args)
	if target == "" {
		target = "3-5"
	}
	return fmt.Sprintf(
		`SLASH COMMAND: /split

Split the following card into %s independently-dispatchable subtasks. For each subtask output:
  - title (under 60 chars)
  - description (2-3 sentences)
  - effort estimate (xs / s / m / l / xl)
  - any prerequisite subtask IDs from this list

Output strict JSON in the form:
  {"subtasks": [{"title": "...", "description": "...", "effort": "m", "deps": []}, ...]}

Do not write any code. Do not commit. The orchestrator will dispatch the subtasks as child cards.

— Parent card —
%s`,
		target, prompt,
	)
}
