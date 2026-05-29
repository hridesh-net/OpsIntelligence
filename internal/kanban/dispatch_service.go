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
	"github.com/opsintelligence/opsintelligence/internal/kanban/worktree"
)

// DispatchService orchestrates agent runs on kanban cards.
type DispatchService struct {
	Store     datastore.Store
	Worktrees *worktree.Manager
	Drivers   map[string]dispatcher.AgentDriver
	CostCalc  *cost.Calculator
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

	// Create worktree if repo_path is configured
	var wt *worktree.Worktree
	if board.RepoPath != "" {
		wt, err = s.Worktrees.Create(cardID, opts.RunID, board.RepoPath, "main")
		if err != nil {
			return nil, fmt.Errorf("dispatch: create worktree: %w", err)
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

	// Start agent in background with a detached context.
	// The HTTP request context will be cancelled when the client receives
	// the 202 Accepted response, so we must not forward it.
	bgCtx, bgCancel := context.WithTimeout(context.Background(), 2*time.Hour)
	go func() {
		defer bgCancel()
		s.runAgent(bgCtx, run, driver, card, wt, systemPrompt)
	}()

	return run, nil
}

func (s *DispatchService) runAgent(ctx context.Context, run *datastore.CardRun, driver dispatcher.AgentDriver, card *datastore.BoardCard, wt *worktree.Worktree, systemPrompt string) {
	// Ensure worktree is cleaned up when the run reaches a terminal state.
	defer s.cleanupWorktree(run, wt)

	prompt := fmt.Sprintf("Task: %s\n\nDescription: %s\n\nCard type: %s | Priority: %s | Effort: %s",
		card.Title, card.Description, card.CardType, card.Priority, card.Effort)

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

			batch = append(batch, &datastore.CardRunEvent{
				RunID:    run.ID,
				Kind:     ev.Kind,
				Phase:    ev.Phase,
				Message:  ev.Message,
				Metadata: ev.Metadata,
			})
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

// AnswerDecision resolves a pending decision and optionally resumes the run.
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

	// Append answer as event
	_ = s.Store.CardRunEvents().Append(ctx, &datastore.CardRunEvent{
		RunID:   decision.RunID,
		Kind:    "text",
		Message: "User answered: " + answer,
		Metadata: map[string]any{
			"decision_id": decisionID,
			"answer":      answer,
		},
	})

	// For now, mark run as completed after decision. In Phase 3, we will
	// inject the answer back into the agent context and continue.
	run, err := s.Store.CardRuns().Get(ctx, decision.RunID)
	if err != nil {
		return err
	}
	run.Status = "running"
	_ = s.Store.CardRuns().Update(ctx, run)

	card, _ := s.Store.BoardCards().Get(ctx, run.CardID)
	if card != nil {
		card.Status = "running"
		_ = s.Store.BoardCards().Update(ctx, card)
	}

	return nil
}

func ptr[T any](v T) *T { return &v }
