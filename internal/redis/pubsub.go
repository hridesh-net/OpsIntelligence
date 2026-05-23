package redis

import (
	"context"
	"encoding/json"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// PubSub provides Redis publish/subscribe for cross-instance event broadcasting.
// It is used by the gateway hub to sync WebSocket messages across nodes and by
// the sub-agent manager to broadcast task lifecycle events.
type PubSub struct {
	client  *Client
	channel string
	log     *zap.Logger
}

// Event is the envelope published over Redis pub/sub.
type Event struct {
	Type string          `json:"type"`
	From string          `json:"from,omitempty"`
	Data json.RawMessage `json:"data"`
}

// NewPubSub returns a PubSub wrapper. If client is nil/disabled, all methods
// become no-ops and Subscribe returns a nil channel.
func NewPubSub(client *Client, log *zap.Logger) *PubSub {
	if log == nil {
		log = zap.NewNop()
	}
	p := &PubSub{client: client, log: log}
	if client != nil && client.Enabled() {
		p.channel = client.Config().PubSubChannel
	}
	return p
}

// Enabled reports whether pub/sub is functional.
func (p *PubSub) Enabled() bool { return p.client != nil && p.client.Enabled() }

// Channel returns the Redis pub/sub channel name.
func (p *PubSub) Channel() string { return p.channel }

// Publish sends an event to all subscribed instances.
func (p *PubSub) Publish(ctx context.Context, ev Event) error {
	if !p.Enabled() {
		return nil
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("redis pubsub: marshal: %w", err)
	}
	if err := p.client.Client().Publish(ctx, p.channel, data).Err(); err != nil {
		p.log.Warn("redis pubsub publish failed", zap.String("type", ev.Type), zap.Error(err))
		return err
	}
	return nil
}

// PublishTyped is a convenience wrapper that marshals a typed payload.
func (p *PubSub) PublishTyped(ctx context.Context, typ string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("redis pubsub: marshal payload: %w", err)
	}
	return p.Publish(ctx, Event{Type: typ, Data: data})
}

// Subscribe starts listening for events on the configured channel. It returns
// a receive-only channel of Events and a cancel function. The channel is closed
// when the context is cancelled or the cancel function is called.
//
// If Redis is disabled, returns (nil, func(){}).
func (p *PubSub) Subscribe(ctx context.Context) (<-chan Event, func()) {
	if !p.Enabled() {
		return nil, func() {}
	}

	pubsub := p.client.Client().Subscribe(ctx, p.channel)
	out := make(chan Event, 64)
	innerCtx, cancel := context.WithCancel(ctx)

	go func() {
		defer close(out)
		defer pubsub.Close()
		ch := pubsub.Channel()
		for {
			select {
			case <-innerCtx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				var ev Event
				if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
					p.log.Warn("redis pubsub: dropped malformed message", zap.Error(err))
					continue
				}
				select {
				case out <- ev:
				case <-innerCtx.Done():
					return
				}
			}
		}
	}()

	return out, func() {
		cancel()
		_ = pubsub.Close()
	}
}

// IsNil returns true if err is the Redis nil response.
func IsNil(err error) bool {
	return err == goredis.Nil
}
