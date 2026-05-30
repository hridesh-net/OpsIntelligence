package gateway

import (
	"context"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/kanban"
)

// KanbanDispatcher is the subset of kanban.DispatchService used by gateway handlers.
type KanbanDispatcher interface {
	Dispatch(ctx context.Context, cardID string, opts kanban.DispatchOpts) (*datastore.CardRun, error)
	StopRun(ctx context.Context, runID string) error
	AnswerDecision(ctx context.Context, decisionID, answer string) error
}

// KanbanAutopilot is the subset of kanban.Autopilot used by gateway handlers.
// Kept separate from KanbanDispatcher so the dispatcher can be wired without
// autopilot when an operator wants the one-shot path only.
type KanbanAutopilot interface {
	StartFeatureDev(ctx context.Context, cardID string, opts kanban.FeatureDevOpts) (*kanban.AutopilotSession, error)
	StartQA(ctx context.Context, cardID string, opts kanban.QAOpts) (*kanban.AutopilotSession, error)
	Stop(sessionID string) error
	Get(sessionID string) *kanban.AutopilotSession
	List() []kanban.AutopilotSession
}
