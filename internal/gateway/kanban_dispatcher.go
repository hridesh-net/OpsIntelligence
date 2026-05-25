package gateway

import (
	"context"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/kanban"
)

// KanbanDispatcher is the subset of kanban.Service used by gateway handlers.
type KanbanDispatcher interface {
	Dispatch(ctx context.Context, boardID, cardID string, req kanban.DispatchRequest) (*datastore.CardRun, error)
	Stop(ctx context.Context, runID string) error
	AnswerDecision(ctx context.Context, decisionID, answer string) error
}
