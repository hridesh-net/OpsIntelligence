// Package events is a tiny in-process pub/sub for kanban run events.
//
// The DB stays authoritative: every event still lands in card_run_events
// via the existing dispatch service writes. The bus is just a fan-out
// so SSE subscribers and webhook deliveries don't have to poll the
// database.
//
// Design choices kept deliberately small:
//
//   - Per-runID topic. Subscribers express interest in one run; the
//     bus only fans out to matching channels. There is no global
//     "all events" channel — webhook delivery (Release E) builds one
//     by subscribing once per active run.
//   - Buffered, non-blocking publish. A slow subscriber loses the
//     oldest event in its buffer rather than blocking the producer.
//     Subscribers that care about exactness reconcile against the DB
//     via card_run_events.List(SinceID=...).
//   - No singleton. The bus is constructed once in
//     gateway_auth.go::attachKanbanToGateway and passed by pointer.
//     A nil *Bus is safe to call — Publish / Subscribe become no-ops
//     so older test paths that don't wire the bus continue to work.
package events

import (
	"sync"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// Bus fans run events out to per-runID subscribers, plus an optional
// "all-runs" channel used by global consumers like the webhook
// delivery worker.
type Bus struct {
	mu      sync.RWMutex
	subs    map[string]map[*subscription]struct{}
	allSubs map[*subscription]struct{}
}

// subscription holds the channel a subscriber receives on plus its
// per-subscription buffer size.
type subscription struct {
	ch chan datastore.CardRunEvent
}

// NewBus creates an empty bus.
func NewBus() *Bus {
	return &Bus{
		subs:    make(map[string]map[*subscription]struct{}),
		allSubs: make(map[*subscription]struct{}),
	}
}

// SubscribeAll registers interest in every event regardless of runID.
// Used by global consumers like the webhook delivery worker. Returns a
// channel + cancel func with the same semantics as Subscribe.
func (b *Bus) SubscribeAll() (<-chan datastore.CardRunEvent, func()) {
	if b == nil {
		ch := make(chan datastore.CardRunEvent)
		close(ch)
		return ch, func() {}
	}
	sub := &subscription{ch: make(chan datastore.CardRunEvent, 64)}
	b.mu.Lock()
	b.allSubs[sub] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		delete(b.allSubs, sub)
		b.mu.Unlock()
		close(sub.ch)
	}
	return sub.ch, cancel
}

// Subscribe registers interest in events for runID. Returns the channel
// to receive on and a cancel func the caller MUST invoke (typically in
// a defer) to release the subscription. The channel is buffered;
// publish-side overflow drops the oldest event rather than blocking,
// so subscribers that need exactness should reconcile against the DB.
func (b *Bus) Subscribe(runID string) (<-chan datastore.CardRunEvent, func()) {
	if b == nil || runID == "" {
		ch := make(chan datastore.CardRunEvent)
		close(ch)
		return ch, func() {}
	}
	sub := &subscription{ch: make(chan datastore.CardRunEvent, 32)}

	b.mu.Lock()
	if b.subs[runID] == nil {
		b.subs[runID] = make(map[*subscription]struct{})
	}
	b.subs[runID][sub] = struct{}{}
	b.mu.Unlock()

	cancel := func() {
		b.mu.Lock()
		if m, ok := b.subs[runID]; ok {
			delete(m, sub)
			if len(m) == 0 {
				delete(b.subs, runID)
			}
		}
		b.mu.Unlock()
		close(sub.ch)
	}
	return sub.ch, cancel
}

// Publish fans an event out to every current subscriber for ev.RunID.
// Non-blocking: a full subscriber buffer drops the oldest event to make
// room for the new one. Safe to call concurrently and on a nil *Bus.
func (b *Bus) Publish(ev datastore.CardRunEvent) {
	if b == nil || ev.RunID == "" {
		return
	}
	b.mu.RLock()
	subs := b.subs[ev.RunID]
	// Snapshot the subscriptions so we can release the read lock before
	// we (potentially) block on a channel send.
	snapshot := make([]*subscription, 0, len(subs)+len(b.allSubs))
	for s := range subs {
		snapshot = append(snapshot, s)
	}
	for s := range b.allSubs {
		snapshot = append(snapshot, s)
	}
	b.mu.RUnlock()
	if len(snapshot) == 0 {
		return
	}

	for _, s := range snapshot {
		select {
		case s.ch <- ev:
		default:
			// Buffer full — drop oldest and retry once.
			select {
			case <-s.ch:
			default:
			}
			select {
			case s.ch <- ev:
			default:
			}
		}
	}
}
