// Package kanban implements autopilot — a multi-persona round-robin loop
// that continuously dispatches queued cards to different personas.
package kanban

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// Autopilot runs a background loop that dispatches queued cards to personas.
type Autopilot struct {
	service   *Service
	store     datastore.Store
	interval  time.Duration
	log       *zap.Logger
	stop      chan struct{}
	stopped   chan struct{}
}

// NewAutopilot creates an autopilot runner.
func NewAutopilot(svc *Service, store datastore.Store, interval time.Duration, log *zap.Logger) *Autopilot {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Autopilot{
		service:  svc,
		store:    store,
		interval: interval,
		log:      log,
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

// Start begins the autopilot loop.
func (a *Autopilot) Start(ctx context.Context) {
	go a.loop(ctx)
}

// Stop halts the autopilot loop.
func (a *Autopilot) Stop() {
	close(a.stop)
	<-a.stopped
}

func (a *Autopilot) loop(ctx context.Context) {
	defer close(a.stopped)
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stop:
			return
		case <-ticker.C:
			a.tick(ctx)
		}
	}
}

func (a *Autopilot) tick(ctx context.Context) {
	// Find all boards with queued cards.
	boards, err := a.store.Boards().List(ctx, datastore.BoardFilter{Limit: 100})
	if err != nil {
		a.log.Warn("autopilot: failed to list boards", zap.Error(err))
		return
	}

	for _, board := range boards {
		cards, err := a.store.BoardCards().List(ctx, datastore.BoardCardFilter{
			BoardID: board.ID,
			Status:  "queued",
			Limit:   10,
		})
		if err != nil {
			continue
		}
		if len(cards) == 0 {
			continue
		}

		// Get personas for this board.
		personas, err := a.store.Personas().List(ctx, datastore.PersonaFilter{Limit: 10})
		if err != nil || len(personas) == 0 {
			continue
		}

		// Round-robin dispatch.
		for i, card := range cards {
			persona := personas[i%len(personas)]
			a.log.Info("autopilot dispatch",
				zap.String("board", board.ID),
				zap.String("card", card.ID),
				zap.String("persona", persona.Name),
			)
			_, err := a.service.Dispatch(ctx, board.ID, card.ID, DispatchRequest{
				PersonaID: persona.ID,
			})
			if err != nil {
				a.log.Warn("autopilot: dispatch failed", zap.String("card", card.ID), zap.Error(err))
			}
		}
	}
}
