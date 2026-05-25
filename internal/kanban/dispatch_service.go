// Package kanban implements the kanban dispatch service that orchestrates
// agent runs on board cards.
package kanban

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/kanban/cost"
	"github.com/opsintelligence/opsintelligence/internal/kanban/dispatcher"
	"github.com/opsintelligence/opsintelligence/internal/kanban/worktree"
)

// Service orchestrates card → agent → run → events.
type Service struct {
	store      datastore.Store
	wtMgr      *worktree.Manager
	registry   *dispatcher.Registry
	pricing    *cost.PricingTable
	decisions  *DecisionResume
	log        *zap.Logger

	mu      sync.RWMutex
	runs    map[string]*RunHandle // runID -> handle
}

// RunHandle tracks an in-flight run.
type RunHandle struct {
	RunID  string
	Cancel context.CancelFunc
	Done   chan struct{}
}

// NewService creates the dispatch service.
func NewService(store datastore.Store, wtMgr *worktree.Manager, registry *dispatcher.Registry, log *zap.Logger) *Service {
	if log == nil {
		log = zap.NewNop()
	}
	return &Service{
		store:     store,
		wtMgr:     wtMgr,
		registry:  registry,
		pricing:   cost.NewPricingTable(),
		decisions: NewDecisionResume(store, log),
		log:       log,
		runs:      make(map[string]*RunHandle),
	}
}

// Dispatch starts a new agent run for a card.
func (s *Service) Dispatch(ctx context.Context, boardID, cardID string, req DispatchRequest) (*datastore.CardRun, error) {
	// Load card.
	card, err := s.store.BoardCards().Get(ctx, cardID)
	if err != nil {
		return nil, fmt.Errorf("card not found: %w", err)
	}
	if card.BoardID != boardID {
		return nil, fmt.Errorf("card does not belong to board")
	}

	// Load board for repo settings.
	board, err := s.store.Boards().Get(ctx, boardID)
	if err != nil {
		return nil, fmt.Errorf("board not found: %w", err)
	}

	// Resolve agent.
	agentID := req.AgentID
	agentType := req.AgentType
	if agentID == "" {
		agents, err := s.store.BoardAgents().ListByBoard(ctx, boardID)
		if err == nil && len(agents) > 0 {
			for _, a := range agents {
				if a.IsDefault && a.IsActive {
					agentID = a.ID
					agentType = a.AgentType
					break
				}
			}
			if agentID == "" {
				agentID = agents[0].ID
				agentType = agents[0].AgentType
			}
		}
	}
	if agentType == "" {
		agentType = "go"
	}

	// Load persona system prompt.
	var systemPrompt string
	if req.PersonaID != "" {
		p, err := s.store.Personas().Get(ctx, req.PersonaID)
		if err == nil {
			systemPrompt = p.SystemPrompt
		}
	}
	if systemPrompt == "" && req.PersonaID == "" {
		// Use board's default persona if any.
		personas, _ := s.store.Personas().List(ctx, datastore.PersonaFilter{BuiltInOnly: true, Limit: 10})
		for _, p := range personas {
			if p.Name == "Senior Engineer" {
				systemPrompt = p.SystemPrompt
				req.PersonaID = p.ID
				break
			}
		}
	}

	// Create run record.
	run := &datastore.CardRun{
		ID:        uuid.NewString(),
		CardID:    cardID,
		RunNumber: 1, // TODO: increment from last run
		AgentID:   agentID,
		AgentType: agentType,
		Model:     req.Model,
		PersonaID: req.PersonaID,
		Status:    "running",
	}
	if err := s.store.CardRuns().Create(ctx, run); err != nil {
		return nil, fmt.Errorf("create run: %w", err)
	}

	// Update card status.
	card.Status = "running"
	card.Assignee = agentID
	card.AssigneeType = "agent"
	if err := s.store.BoardCards().Update(ctx, card); err != nil {
		s.log.Warn("failed to update card status", zap.Error(err))
	}

	// Create worktree.
	wtEnt, err := s.wtMgr.Create(ctx, run.ID, cardID, board.RepoURL, board.RepoPath, card.Branch, run.BaseBranch)
	if err != nil {
		s.log.Warn("worktree creation failed, continuing with local path",
			zap.Error(err),
			zap.String("repo_url", board.RepoURL),
		)
		wtEnt = &worktree.Entry{Path: board.RepoPath, Branch: card.Branch}
	}

	run.WorktreePath = wtEnt.Path
	run.Branch = wtEnt.Branch
	if err := s.store.CardRuns().Update(ctx, run); err != nil {
		s.log.Warn("failed to update run worktree", zap.Error(err))
	}

	// Start the run in a goroutine.
	runCtx, cancel := context.WithCancel(context.Background())
	handle := &RunHandle{
		RunID:  run.ID,
		Cancel: cancel,
		Done:   make(chan struct{}),
	}
	s.mu.Lock()
	s.runs[run.ID] = handle
	s.mu.Unlock()

	go s.execute(runCtx, run, card, systemPrompt, handle)

	return run, nil
}

// Stop cancels an in-flight run.
func (s *Service) Stop(ctx context.Context, runID string) error {
	s.mu.RLock()
	handle, ok := s.runs[runID]
	s.mu.RUnlock()
	if ok && handle.Cancel != nil {
		handle.Cancel()
		select {
		case <-handle.Done:
		case <-time.After(5 * time.Second):
		}
	}

	run, err := s.store.CardRuns().Get(ctx, runID)
	if err != nil {
		return err
	}
	run.Status = "stopped"
	now := time.Now().UTC()
	run.CompletedAt = &now
	return s.store.CardRuns().Update(ctx, run)
}

// execute is the goroutine that drives the agent run.
func (s *Service) execute(ctx context.Context, run *datastore.CardRun, card *datastore.BoardCard, systemPrompt string, handle *RunHandle) {
	defer close(handle.Done)

	driver, ok := s.registry.Get(run.AgentType)
	if !ok {
		s.failRun(ctx, run, fmt.Sprintf("no driver registered for agent type %q", run.AgentType))
		return
	}

	events := make(chan dispatcher.Event, 64)
	resultCh := make(chan dispatcher.Result, 1)

	go func() {
		resultCh <- driver.Run(ctx, dispatcher.RunRequest{
			RunID:           run.ID,
			CardID:          run.CardID,
			AgentID:         run.AgentID,
			PersonaID:       run.PersonaID,
			Model:           run.Model,
			WorktreePath:    run.WorktreePath,
			Branch:          run.Branch,
			BaseBranch:      run.BaseBranch,
			CardTitle:       card.Title,
			CardDescription: card.Description,
			SystemPrompt:    systemPrompt,
		}, events)
	}()

	// Drain events and persist them.
	for {
		select {
		case <-ctx.Done():
			s.finalizeRun(ctx, run, dispatcher.Result{Status: "stopped"})
			return
		case ev, ok := <-events:
			if !ok {
				// Events closed, wait for result.
				result := <-resultCh
				s.finalizeRun(ctx, run, result)
				return
			}
			s.persistEvent(ctx, run, ev)
			if ev.Kind == dispatcher.EventKindDecision {
				s.handleDecision(ctx, run, ev)
			}
		}
	}
}

func (s *Service) persistEvent(ctx context.Context, run *datastore.CardRun, ev dispatcher.Event) {
	if ctx.Err() != nil {
		return
	}
	ce := &datastore.CardRunEvent{
		RunID:    run.ID,
		Kind:     string(ev.Kind),
		Phase:    ev.Phase,
		Message:  ev.Message,
		Metadata: ev.Metadata,
	}
	if err := s.store.CardRunEvents().Append(ctx, ce); err != nil {
		s.log.Warn("failed to append event", zap.Error(err))
	}
}

func (s *Service) handleDecision(ctx context.Context, run *datastore.CardRun, ev dispatcher.Event) {
	d := NewDecisionDetector(s.log).Detect(run.ID, run.CardID, ev.Message)
	if d == nil {
		return
	}
	if err := s.store.PendingDecisions().Create(ctx, d); err != nil {
		s.log.Warn("failed to create pending decision", zap.Error(err))
		return
	}
	// Update run to awaiting status.
	run.Status = "awaiting"
	if err := s.store.CardRuns().Update(ctx, run); err != nil {
		s.log.Warn("failed to update run status", zap.Error(err))
	}
	// Update card too.
	card, _ := s.store.BoardCards().Get(ctx, run.CardID)
	if card != nil {
		card.Status = "awaiting"
		_ = s.store.BoardCards().Update(ctx, card)
	}
}

func (s *Service) finalizeRun(ctx context.Context, run *datastore.CardRun, result dispatcher.Result) {
	if ctx.Err() != nil {
		result.Status = "stopped"
	}
	run.Status = result.Status
	run.ResultSummary = result.ResultSummary
	run.Error = result.Error
	run.TokenIn = result.TokenIn
	run.TokenOut = result.TokenOut
	run.CostUSD = s.pricing.CostUSD(run.Model, result.TokenIn, result.TokenOut)
	run.ElapsedMs = result.ElapsedMs
	now := time.Now().UTC()
	run.CompletedAt = &now

	if err := s.store.CardRuns().Update(ctx, run); err != nil {
		s.log.Warn("failed to finalize run", zap.Error(err))
	}

	// Update card status and cost rollup.
	card, err := s.store.BoardCards().Get(ctx, run.CardID)
	if err == nil && card != nil {
		if result.Status == "completed" || result.Status == "failed" || result.Status == "stopped" {
			card.Status = result.Status
		}
		card.CostUSD += run.CostUSD
		card.TokenIn += run.TokenIn
		card.TokenOut += run.TokenOut
		card.WorktreePath = run.WorktreePath
		card.Branch = run.Branch
		if err := s.store.BoardCards().Update(ctx, card); err != nil {
			s.log.Warn("failed to update card final state", zap.Error(err))
		}
	}

	s.mu.Lock()
	delete(s.runs, run.ID)
	s.mu.Unlock()
}

func (s *Service) failRun(ctx context.Context, run *datastore.CardRun, reason string) {
	s.finalizeRun(ctx, run, dispatcher.Result{Status: "failed", Error: reason})
}

// DispatchRequest is the payload for Dispatch.
type DispatchRequest struct {
	AgentID   string
	AgentType string
	PersonaID string
	Model     string
}

// AnswerDecision forwards a human answer to a pending decision.
func (s *Service) AnswerDecision(ctx context.Context, decisionID, answer string) error {
	return s.decisions.Answer(ctx, decisionID, answer)
}
