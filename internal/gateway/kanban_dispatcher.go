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
