package redis

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"
)

// HubBridge connects a local gateway Hub to Redis pub/sub so WebSocket
// broadcasts flow across all OpsIntelligence instances in a cluster.
//
// When Redis is enabled, any message broadcast locally is also published
// to Redis, and messages from other instances are forwarded into the
// local Hub's broadcast channel.
type HubBridge struct {
	pubsub *PubSub
	log    *zap.Logger
}

// HubMessage is the envelope sent over Redis for WebSocket broadcasts.
type HubMessage struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload"`
}

// NewHubBridge returns a bridge. If Redis is disabled, all methods are no-ops.
func NewHubBridge(pubsub *PubSub, log *zap.Logger) *HubBridge {
	if log == nil {
		log = zap.NewNop()
	}
	return &HubBridge{pubsub: pubsub, log: log}
}

// Enabled reports whether cross-instance broadcast is active.
func (b *HubBridge) Enabled() bool { return b.pubsub != nil && b.pubsub.Enabled() }

// Publish broadcasts a message to other instances via Redis.
func (b *HubBridge) Publish(ctx context.Context, msg HubMessage) error {
	if !b.Enabled() {
		return nil
	}
	return b.pubsub.PublishTyped(ctx, "hub.broadcast", msg)
}

// Subscribe starts forwarding remote broadcasts into the given local broadcast
// function. The returned cancel function stops the subscriber.
func (b *HubBridge) Subscribe(ctx context.Context, broadcast func([]byte)) func() {
	if !b.Enabled() {
		return func() {}
	}

	events, cancel := b.pubsub.Subscribe(ctx)
	if events == nil {
		return cancel
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				if ev.Type != "hub.broadcast" {
					continue
				}
				var msg HubMessage
				if err := json.Unmarshal(ev.Data, &msg); err != nil {
					b.log.Warn("hub bridge: dropped malformed message", zap.Error(err))
					continue
				}
				broadcast(msg.Payload)
			}
		}
	}()

	return cancel
}
