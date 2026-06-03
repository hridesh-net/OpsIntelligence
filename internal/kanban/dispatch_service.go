// Package kanban provides the core kanban board orchestration.
package kanban

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/kanban/cost"
	"github.com/opsintelligence/opsintelligence/internal/kanban/dispatcher"
	"github.com/opsintelligence/opsintelligence/internal/kanban/events"
	"github.com/opsintelligence/opsintelligence/internal/kanban/worktree"
)

// DispatchService orchestrates agent runs on kanban cards.
type DispatchService struct {
	Store     datastore.Store
	Worktrees *worktree.Manager
	Drivers   map[string]dispatcher.AgentDriver
	CostCalc  *cost.Calculator
	// Events is an optional in-process pub/sub the dispatcher publishes
	// every CardRunEvent to alongside the DB write. SSE and webhook
	// subscribers consume from here. A nil bus is safe — Publish is a
	// no-op so existing call sites that never wire the bus continue to
	// work.
	Events *events.Bus
}

// NewDispatchService creates a dispatch service with the given dependencies.
func NewDispatchService(store datastore.Store, wt *worktree.Manager, drivers map[string]dispatcher.AgentDriver, calc *cost.Calculator) *DispatchService {
	if drivers == nil {
		drivers = make(map[string]dispatcher.AgentDriver)
	}
	return &DispatchService{
		Store:     store,
		Worktrees: wt,
		Drivers:   drivers,
		CostCalc:  calc,
	}
}

// RegisterDriver adds an agent driver to the registry.
func (s *DispatchService) RegisterDriver(d dispatcher.AgentDriver) {
	s.Drivers[d.Type()] = d
}

// DispatchOpts configures a dispatch operation.
type DispatchOpts struct {
	RunID     string
	AgentID   string
	PersonaID string
	Model     string
	CreatedBy string

	// PromptOverride, when non-empty, replaces the auto-built
	// "Task: title / Description: ..." prompt. Used by decision-resumption
	// and autopilot continuations.
	PromptOverride string
	// ReuseWorktreePath and ReuseBranch let a follow-up run pick up the
	// previous run's git worktree instead of creating a fresh one. Both
	// must be set together; if either is empty a new worktree is created.
	ReuseWorktreePath string
	ReuseBranch       string
	// ParentRunID, when set, marks this run as a continuation of an earlier
	// run (e.g. after a decision was answered). Useful for grouping runs
	// in autopilot sessions and the UI.
	ParentRunID string

	// SlashCommand, when non-empty, post-processes the prompt with one of
	// the built-in templates ("spec", "review", "split"). See applySlash.
	SlashCommand string
	// SlashArgs is the trailing text after a slash command (e.g. for
	// /split, the desired number of subtasks).
	SlashArgs string
}

// Dispatch starts an agent run on a card.
func (s *DispatchService) Dispatch(ctx context.Context, cardID string, opts DispatchOpts) (*datastore.CardRun, error) {
	card, err := s.Store.BoardCards().Get(ctx, cardID)
	if err != nil {
		return nil, fmt.Errorf("dispatch: get card: %w", err)
	}

	board, err := s.Store.Boards().Get(ctx, card.BoardID)
	if err != nil {
		return nil, fmt.Errorf("dispatch: get board: %w", err)
	}

	agent, err := s.resolveAgent(ctx, card, opts.AgentID)
	if err != nil {
		return nil, fmt.Errorf("dispatch: resolve agent: %w", err)
	}

	driver, ok := s.Drivers[agent.AgentType]
	if !ok {
		return nil, fmt.Errorf("dispatch: no driver for type %q", agent.AgentType)
	}

	// Build system prompt with persona
	systemPrompt, err := s.buildSystemPrompt(ctx, opts.PersonaID, card)
	if err != nil {
		return nil, fmt.Errorf("dispatch: build system prompt: %w", err)
	}

	if opts.RunID == "" {
		opts.RunID = uuid.NewString()
	}

	// Budget cap: bail before allocating any agent resources.
	if err := s.enforceBudget(ctx, card, board); err != nil {
		return nil, err
	}

	// Create worktree if repo_path is configured. Reuse an existing
	// worktree if the caller passed one (decision resumption, autopilot
	// continuation) so the follow-up agent sees the partial work.
	var wt *worktree.Worktree
	if board.RepoPath != "" {
		if opts.ReuseWorktreePath != "" && opts.ReuseBranch != "" {
			wt = &worktree.Worktree{
				Path:       opts.ReuseWorktreePath,
				Branch:     opts.ReuseBranch,
				BaseBranch: "main",
			}
		} else {
			wt, err = s.Worktrees.Create(cardID, opts.RunID, board.RepoPath, "main")
			if err != nil {
				return nil, fmt.Errorf("dispatch: create worktree: %w", err)
			}
		}
	}

	// Insert run record
	run := &datastore.CardRun{
		ID:           opts.RunID,
		CardID:       cardID,
		AgentID:      agent.ID,
		AgentType:    agent.AgentType,
		Model:        opts.Model,
		PersonaID:    opts.PersonaID,
		Status:       "running",
		BaseBranch:   "main",
		RepoPath:     board.RepoPath,
		CreatedBy:    opts.CreatedBy,
	}
	if wt != nil {
		run.WorktreePath = wt.Path
		run.Branch = wt.Branch
		run.BaseBranch = wt.BaseBranch
	}
	if err := s.Store.CardRuns().Create(ctx, run); err != nil {
		if wt != nil {
			_ = s.Worktrees.Remove(board.RepoPath, wt)
		}
		return nil, fmt.Errorf("dispatch: create run: %w", err)
	}

	// Update card status atomically
	card.Status = "running"
	card.StartedAt = ptr(time.Now().UTC())
	_ = s.Store.BoardCards().Update(ctx, card)

	// Capture per-run options (prompt override, slash command, reuse flag)
	// so the goroutine doesn't reach back into the caller's value.
	runOpts := opts

	// Start agent in background with a detached context.
	// The HTTP request context will be cancelled when the client receives
	// the 202 Accepted response, so we must not forward it.
	bgCtx, bgCancel := context.WithTimeout(context.Background(), 2*time.Hour)
	go func() {
		defer bgCancel()
		s.runAgent(bgCtx, run, driver, card, wt, systemPrompt, runOpts)
	}()

	return run, nil
}

func (s *DispatchService) runAgent(ctx context.Context, run *datastore.CardRun, driver dispatcher.AgentDriver, card *datastore.BoardCard, wt *worktree.Worktree, systemPrompt string, dispatchOpts DispatchOpts) {
	// Ensure worktree is cleaned up when the run reaches a terminal state —
	// but ONLY when this dispatch actually created the worktree. Follow-up
	// runs that reuse a parent's worktree must not delete it on completion;
	// the autopilot loop or promote-to-commit step owns its lifecycle.
	if dispatchOpts.ReuseWorktreePath == "" {
		defer s.cleanupWorktree(run, wt)
	}

	prompt := dispatchOpts.PromptOverride
	if prompt == "" {
		prompt = fmt.Sprintf("Task: %s\n\nDescription: %s\n\nCard type: %s | Priority: %s | Effort: %s",
			card.Title, card.Description, card.CardType, card.Priority, card.Effort)
	}
	// Slash commands wrap the prompt with a directive template before the
	// agent sees it. Idempotent: empty SlashCommand is a no-op.
	prompt = applySlashCommand(prompt, dispatchOpts.SlashCommand, dispatchOpts.SlashArgs, card)

	opts := dispatcher.RunOpts{
		RunID:        run.ID,
		CardID:       run.CardID,
		WorktreePath: run.WorktreePath,
		Branch:       run.Branch,
		BaseBranch:   run.BaseBranch,
		Prompt:       prompt,
		SystemPrompt: systemPrompt,
		Model:        run.Model,
	}

	events, cancel, err := driver.Run(ctx, opts)
	if err != nil {
		s.failRun(ctx, run, err.Error())
		return
	}
	defer cancel()

	// Per-run budget cap. 0 = no cap (the common case).
	board, _ := s.Store.Boards().Get(ctx, card.BoardID)
	perRunCap := 0.0
	if board != nil {
		perRunCap = s.perRunCap(board)
	}

	start := time.Now().UTC()
	var totalTokensIn, totalTokensOut int64

	// Batch events to reduce DB write amplification.
	const batchSize = 50
	const flushInterval = time.Second
	batch := make([]*datastore.CardRunEvent, 0, batchSize)
	flushTick := time.NewTicker(flushInterval)
	defer flushTick.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		_ = s.Store.CardRunEvents().AppendBatch(ctx, batch)
		batch = batch[:0]
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			s.failRun(ctx, run, "context cancelled")
			return

		case <-flushTick.C:
			flush()

		case ev, ok := <-events:
			if !ok {
				flush()
				s.finalizeRun(ctx, run, card, start, totalTokensIn, totalTokensOut, "")
				return
			}

			rec := datastore.CardRunEvent{
				RunID:    run.ID,
				Kind:     ev.Kind,
				Phase:    ev.Phase,
				Message:  ev.Message,
				Metadata: ev.Metadata,
			}
			// Publish to the in-process bus first so SSE subscribers see
			// the event at the source-of-truth latency, not the DB
			// batch-flush latency (~1s). The DB write below is still the
			// authoritative replay surface on reconnect.
			s.Events.Publish(rec)
			batch = append(batch, &rec)
			if len(batch) >= batchSize {
				flush()
			}

			// Track tokens from metadata
			if usage, ok := ev.Metadata["usage"].(map[string]any); ok {
				if p, ok := usage["prompt_tokens"].(float64); ok {
					totalTokensIn += int64(p)
				}
				if c, ok := usage["completion_tokens"].(float64); ok {
					totalTokensOut += int64(c)
				}
			}

			// Per-run budget enforcement: if we've already spent more than
			// the cap, cancel the agent process and finalize as
			// "stopped_budget". The cost calculator is cheap enough to call
			// on every event.
			if perRunCap > 0 {
				if cost := s.CostCalc.Calculate(run.Model, totalTokensIn, totalTokensOut); cost >= perRunCap {
					flush()
					budgetEv := datastore.CardRunEvent{
						RunID:   run.ID,
						Kind:    "error",
						Message: fmt.Sprintf("Per-run budget cap reached ($%.4f >= $%.4f). Cancelling agent.", cost, perRunCap),
					}
					s.Events.Publish(budgetEv)
					_ = s.Store.CardRunEvents().Append(ctx, &budgetEv)
					cancel()
					s.finalizeRun(ctx, run, card, start, totalTokensIn, totalTokensOut, "per-run budget cap exceeded")
					return
				}
			}

			// Handle decision
			if ev.IsDecision() {
				flush()
				s.pauseForDecision(ctx, run, ev)
				return
			}

			// Handle completion
			if ev.IsDone() {
				flush()
				var errMsg string
				if ev.Kind == "error" {
					errMsg = ev.Message
				}
				s.finalizeRun(ctx, run, card, start, totalTokensIn, totalTokensOut, errMsg)
				return
			}
		}
	}
}

func (s *DispatchService) finalizeRun(ctx context.Context, run *datastore.CardRun, card *datastore.BoardCard, start time.Time, tokensIn, tokensOut int64, errMsg string) {
	elapsed := time.Since(start).Milliseconds()
	costUSD := s.CostCalc.Calculate(run.Model, tokensIn, tokensOut)

	if errMsg != "" {
		run.Status = "failed"
		run.Error = errMsg
	} else {
		run.Status = "completed"
	}
	run.ElapsedMs = elapsed
	run.CostUSD = costUSD
	run.TokenIn = tokensIn
	run.TokenOut = tokensOut
	now := time.Now().UTC()
	run.CompletedAt = &now
	_ = s.Store.CardRuns().Update(ctx, run)

	// Atomic cost update to avoid lost-update race conditions.
	_ = s.Store.BoardCards().AddCost(ctx, card.ID, costUSD, tokensIn, tokensOut)

	// Update card status
	card.Status = run.Status
	card.CompletedAt = &now
	_ = s.Store.BoardCards().Update(ctx, card)

	// Publish a terminal lifecycle event so SSE subscribers know the
	// run finished without having to poll /runs/{id} themselves. The
	// event is not persisted to card_run_events — finalization status
	// is already reflected in card_runs.status, so SSE replay can
	// resynthesize it from the run row on reconnect.
	s.Events.Publish(datastore.CardRunEvent{
		RunID:   run.ID,
		Kind:    "lifecycle",
		Phase:   run.Status,
		Message: "run " + run.Status,
		Metadata: map[string]any{
			"elapsed_ms": elapsed,
			"cost_usd":   costUSD,
			"token_in":   tokensIn,
			"token_out":  tokensOut,
		},
	})
}

func (s *DispatchService) cleanupWorktree(run *datastore.CardRun, wt *worktree.Worktree) {
	if wt == nil || run.RepoPath == "" {
		return
	}
	_ = s.Worktrees.Remove(run.RepoPath, wt)
}

func (s *DispatchService) pauseForDecision(ctx context.Context, run *datastore.CardRun, ev dispatcher.Event) {
	run.Status = "awaiting"
	_ = s.Store.CardRuns().Update(ctx, run)

	options := []string{}
	if opts, ok := ev.Metadata["options"].([]any); ok {
		for _, o := range opts {
			if str, ok := o.(string); ok {
				options = append(options, str)
			}
		}
	}

	decision := &datastore.PendingDecision{
		ID:       uuid.NewString(),
		RunID:    run.ID,
		CardID:   run.CardID,
		Question: ev.Message,
		Options:  options,
		Status:   "pending",
	}
	_ = s.Store.PendingDecisions().Create(ctx, decision)

	// Update card
	card, _ := s.Store.BoardCards().Get(ctx, run.CardID)
	if card != nil {
		card.Status = "awaiting"
		_ = s.Store.BoardCards().Update(ctx, card)
	}
}

func (s *DispatchService) failRun(ctx context.Context, run *datastore.CardRun, errMsg string) {
	run.Status = "failed"
	run.Error = errMsg
	now := time.Now().UTC()
	run.CompletedAt = &now
	_ = s.Store.CardRuns().Update(ctx, run)

	card, _ := s.Store.BoardCards().Get(ctx, run.CardID)
	if card != nil {
		card.Status = "failed"
		_ = s.Store.BoardCards().Update(ctx, card)
	}
}

func (s *DispatchService) resolveAgent(ctx context.Context, card *datastore.BoardCard, explicitID string) (*datastore.BoardAgent, error) {
	if explicitID != "" {
		return s.Store.BoardAgents().Get(ctx, explicitID)
	}
	if card.Assignee != "" && card.AssigneeType == "agent" {
		return s.Store.BoardAgents().Get(ctx, card.Assignee)
	}
	agents, err := s.Store.BoardAgents().ListByBoard(ctx, card.BoardID)
	if err != nil {
		return nil, err
	}
	for _, a := range agents {
		if a.IsDefault && a.IsActive {
			return &a, nil
		}
	}
	if len(agents) > 0 {
		return &agents[0], nil
	}
	return nil, fmt.Errorf("no agent available for board %s", card.BoardID)
}

func (s *DispatchService) buildSystemPrompt(ctx context.Context, personaID string, card *datastore.BoardCard) (string, error) {
	base := "You are an AI software engineer. Implement the task described by the user. " +
		"Use available tools to read files, write code, run tests, and inspect the repository. " +
		"When you need clarification, ask a specific question with numbered options."

	if personaID == "" {
		return base, nil
	}

	persona, err := s.Store.Personas().Get(ctx, personaID)
	if err != nil {
		return base, nil // fallback to base if persona not found
	}

	return base + "\n\n" + persona.SystemPrompt, nil
}

// StopRun marks a run as stopped.
func (s *DispatchService) StopRun(ctx context.Context, runID string) error {
	run, err := s.Store.CardRuns().Get(ctx, runID)
	if err != nil {
		return err
	}
	run.Status = "stopped"
	now := time.Now().UTC()
	run.CompletedAt = &now
	if err := s.Store.CardRuns().Update(ctx, run); err != nil {
		return err
	}

	card, _ := s.Store.BoardCards().Get(ctx, run.CardID)
	if card != nil {
		card.Status = "stopped"
		_ = s.Store.BoardCards().Update(ctx, card)
	}
	return nil
}

// AnswerDecision resolves a pending decision and resumes the run by
// dispatching a continuation against the same card with the answer
// injected into the prompt. The original worktree is preserved so the
// follow-up agent picks up the in-progress branch.
func (s *DispatchService) AnswerDecision(ctx context.Context, decisionID, answer string) error {
	decision, err := s.Store.PendingDecisions().Get(ctx, decisionID)
	if err != nil {
		return err
	}
	if decision.Status != "pending" {
		return fmt.Errorf("decision already %s", decision.Status)
	}

	if err := s.Store.PendingDecisions().Answer(ctx, decisionID, answer); err != nil {
		return err
	}

	// Append answer as event so the run timeline shows the user's reply.
	answerEv := datastore.CardRunEvent{
		RunID:   decision.RunID,
		Kind:    "text",
		Message: "User answered: " + answer,
		Metadata: map[string]any{
			"decision_id": decisionID,
			"answer":      answer,
		},
	}
	s.Events.Publish(answerEv)
	_ = s.Store.CardRunEvents().Append(ctx, &answerEv)

	// Close the paused run (it can't be resumed in-process for CLI drivers —
	// the underlying CLI has already exited or is blocked on a TTY we don't
	// own). Finalize it as "completed_paused" so cost / token totals stick.
	pausedRun, err := s.Store.CardRuns().Get(ctx, decision.RunID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	pausedRun.Status = "completed_paused"
	pausedRun.CompletedAt = &now
	_ = s.Store.CardRuns().Update(ctx, pausedRun)

	card, err := s.Store.BoardCards().Get(ctx, pausedRun.CardID)
	if err != nil {
		return err
	}

	// Build the continuation prompt: include the original question, the
	// answer the user chose, and a directive to continue from where the
	// previous run left off. The follow-up dispatch goes through the
	// normal Dispatch path (worktree, driver, event stream) so the user
	// sees a fresh run in the UI tied to the same card.
	continuation := fmt.Sprintf(
		"Continuing the previous run. The decision-prompt question was:\n\n%s\n\nThe operator answered: %s\n\nProceed with that choice. The git worktree from the previous run is at %q on branch %q; pick up from there.",
		decision.Question, answer, pausedRun.WorktreePath, pausedRun.Branch,
	)

	followUp := DispatchOpts{
		AgentID:   pausedRun.AgentID,
		PersonaID: pausedRun.PersonaID,
		Model:     pausedRun.Model,
		CreatedBy: pausedRun.CreatedBy,
		// PromptOverride bypasses the default "Task: ..." prompt and uses
		// the continuation message verbatim. See runAgent + Dispatch.
		PromptOverride:     continuation,
		ReuseWorktreePath:  pausedRun.WorktreePath,
		ReuseBranch:        pausedRun.Branch,
		ParentRunID:        pausedRun.ID,
	}
	if _, err := s.Dispatch(ctx, card.ID, followUp); err != nil {
		// Surface the failure as a card-run event so the user sees why
		// the continuation never started; the paused run is already
		// closed so we don't need to roll back.
		failEv := datastore.CardRunEvent{
			RunID:   pausedRun.ID,
			Kind:    "error",
			Message: "Failed to dispatch continuation: " + err.Error(),
		}
		s.Events.Publish(failEv)
		_ = s.Store.CardRunEvents().Append(ctx, &failEv)
		return err
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
