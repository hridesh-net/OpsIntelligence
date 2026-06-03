package events_test

import (
	"sync"
	"testing"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/kanban/events"
)

func TestBus_PublishSubscribe_DeliversToMatchingRunID(t *testing.T) {
	bus := events.NewBus()
	ch, cancel := bus.Subscribe("run-1")
	defer cancel()

	bus.Publish(datastore.CardRunEvent{RunID: "run-1", Kind: "text", Message: "hi"})

	select {
	case got := <-ch:
		if got.Message != "hi" {
			t.Fatalf("wrong message: %q", got.Message)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive published event")
	}
}

func TestBus_Publish_IgnoresUnrelatedRunIDs(t *testing.T) {
	bus := events.NewBus()
	ch, cancel := bus.Subscribe("run-1")
	defer cancel()

	bus.Publish(datastore.CardRunEvent{RunID: "run-2", Kind: "text", Message: "x"})

	select {
	case got := <-ch:
		t.Fatalf("should not have received cross-run event; got %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBus_Cancel_StopsDelivery(t *testing.T) {
	bus := events.NewBus()
	ch, cancel := bus.Subscribe("run-1")
	cancel()

	bus.Publish(datastore.CardRunEvent{RunID: "run-1"})

	// After cancel the channel is closed; range/recv should yield the
	// zero value and ok=false.
	if _, ok := <-ch; ok {
		t.Fatal("cancelled subscription should be closed")
	}
}

func TestBus_NilBus_IsSafe(t *testing.T) {
	var bus *events.Bus // nil

	bus.Publish(datastore.CardRunEvent{RunID: "run-1"}) // must not panic
	ch, cancel := bus.Subscribe("run-1")
	defer cancel()
	if _, ok := <-ch; ok {
		t.Fatal("nil-bus subscription should yield a closed channel")
	}
}

func TestBus_PublishConcurrentSubscribers(t *testing.T) {
	bus := events.NewBus()
	const n = 50
	var wg sync.WaitGroup
	chans := make([]<-chan datastore.CardRunEvent, n)
	cancels := make([]func(), n)
	for i := 0; i < n; i++ {
		chans[i], cancels[i] = bus.Subscribe("run-x")
	}
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()

	bus.Publish(datastore.CardRunEvent{RunID: "run-x", Message: "fan-out"})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(ch <-chan datastore.CardRunEvent) {
			defer wg.Done()
			select {
			case ev := <-ch:
				if ev.Message != "fan-out" {
					t.Errorf("wrong message: %q", ev.Message)
				}
			case <-time.After(time.Second):
				t.Error("subscriber missed fan-out")
			}
		}(chans[i])
	}
	wg.Wait()
}
