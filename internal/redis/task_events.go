package redis

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/subagents"
)

// TaskEvents publishes sub-agent task lifecycle events over Redis so that
// any instance in a cluster can observe task progress without polling.
type TaskEvents struct {
	pubsub *PubSub
	log    *zap.Logger
}



// NewTaskEvents returns a task event publisher. If Redis is disabled,
// all methods become no-ops.
func NewTaskEvents(pubsub *PubSub, log *zap.Logger) *TaskEvents {
	if log == nil {
		log = zap.NewNop()
	}
	return &TaskEvents{pubsub: pubsub, log: log}
}

// Enabled reports whether task event publishing is active.
func (t *TaskEvents) Enabled() bool { return t.pubsub != nil && t.pubsub.Enabled() }

// Publish emits a task event to all subscribers.
func (t *TaskEvents) Publish(ctx context.Context, ev subagents.TaskEvent) {
	if !t.Enabled() {
		return
	}
	if err := t.pubsub.PublishTyped(ctx, "task.event", ev); err != nil {
		t.log.Warn("task event publish failed", zap.String("task_id", ev.TaskID), zap.Error(err))
	}
}

// Subscribe returns a channel of task events from other instances.
// The channel is closed when the context is cancelled or cancel() is called.
func (t *TaskEvents) Subscribe(ctx context.Context) (<-chan subagents.TaskEvent, func()) {
	if !t.Enabled() {
		return nil, func() {}
	}

	events, cancel := t.pubsub.Subscribe(ctx)
	if events == nil {
		return nil, cancel
	}

	out := make(chan subagents.TaskEvent, 64)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				if ev.Type != "task.event" {
					continue
				}
				var te subagents.TaskEvent
				if err := json.Unmarshal(ev.Data, &te); err != nil {
					t.log.Warn("task event unmarshal failed", zap.Error(err))
					continue
				}
				select {
				case out <- te:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, cancel
}
