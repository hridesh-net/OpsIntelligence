// Package webhooks delivers kanban run events to user-registered HTTP
// endpoints (kanbots.dev-style outbound notifications).
//
// The Worker subscribes once to events.Bus.SubscribeAll(), filters
// each event against the active webhook set, and POSTs the matching
// ones with an HMAC-SHA256 signature in X-OpsIntel-Signature. Status
// codes and errors flow back into kanban_webhooks.last_status /
// last_error via UpdateDeliveryStatus so a list endpoint can show the
// per-webhook health.
//
// The worker pool is intentionally small (4 goroutines): kanban event
// volume is per-card, not per-request, so the bottleneck is the
// downstream HTTPS handshake, not CPU.
package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/kanban/events"
)

const (
	defaultTimeout = 8 * time.Second
	maxAttempts    = 3
)

// Worker delivers CardRunEvent messages to every active KanbanWebhook
// whose event-filter matches.
type Worker struct {
	Store   datastore.Store
	Bus     *events.Bus
	Client  *http.Client
	deliver chan job
	stop    chan struct{}
}

type job struct {
	hook datastore.KanbanWebhook
	body []byte
	kind string
}

// NewWorker constructs a Worker but does not start it. Call Start to
// spin up the subscriber + delivery pool; Stop tears them down.
func NewWorker(store datastore.Store, bus *events.Bus) *Worker {
	return &Worker{
		Store:   store,
		Bus:     bus,
		Client:  &http.Client{Timeout: defaultTimeout},
		deliver: make(chan job, 128),
		stop:    make(chan struct{}),
	}
}

// Start subscribes to the bus and starts delivery workers. Safe to
// call once; no-op on subsequent calls (no internal guard — the caller
// owns lifecycle, mirroring NewDispatchService).
func (w *Worker) Start(ctx context.Context) {
	if w == nil || w.Bus == nil {
		return
	}
	ch, cancel := w.Bus.SubscribeAll()

	// Fan-in: one goroutine routes events to delivery jobs. The
	// delivery pool drains `w.deliver`.
	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stop:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				w.route(ctx, ev)
			}
		}
	}()

	for i := 0; i < 4; i++ {
		go w.loop(ctx)
	}
}

// Stop signals the workers to exit. Caller should still respect ctx
// cancellation as the primary stop signal.
func (w *Worker) Stop() {
	if w == nil {
		return
	}
	close(w.stop)
}

// route fans an event out to every matching webhook by reading the
// hook list from the store. The list is small (operators register a
// handful of webhooks per board), so refetching on each event keeps
// the implementation simple and lets `kanban webhooks` mutations take
// effect immediately without an explicit refresh signal.
func (w *Worker) route(ctx context.Context, ev datastore.CardRunEvent) {
	hooks, err := w.Store.KanbanWebhooks().List(ctx)
	if err != nil || len(hooks) == 0 {
		return
	}
	kind := eventKind(ev)
	body, err := buildPayload(ev, kind)
	if err != nil {
		return
	}
	for _, h := range hooks {
		if !h.Active {
			continue
		}
		if !matches(h.Events, kind) {
			continue
		}
		select {
		case w.deliver <- job{hook: h, body: body, kind: kind}:
		default:
			// queue is full; drop. last_error reflects the most
			// recent network outcome, not queueing pressure.
		}
	}
}

func (w *Worker) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case j := <-w.deliver:
			w.send(ctx, j)
		}
	}
}

func (w *Worker) send(ctx context.Context, j job) {
	delivery := uuid.NewString()
	mac := hmac.New(sha256.New, []byte(j.hook.Secret))
	mac.Write(j.body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	var lastStatus int
	var lastErr string
	backoff := 500 * time.Millisecond
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.hook.URL, bytes.NewReader(j.body))
		if err != nil {
			lastErr = err.Error()
			break
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "opsintelligence-kanban-webhook/1")
		req.Header.Set("X-OpsIntel-Event", j.kind)
		req.Header.Set("X-OpsIntel-Delivery", delivery)
		req.Header.Set("X-OpsIntel-Signature", sig)

		resp, err := w.Client.Do(req)
		if err != nil {
			lastErr = err.Error()
		} else {
			lastStatus = resp.StatusCode
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				lastErr = ""
				break
			}
			lastErr = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}
	_ = w.Store.KanbanWebhooks().UpdateDeliveryStatus(ctx, j.hook.ID, lastStatus, lastErr)
}

// eventKind maps a CardRunEvent to a webhook event name.
//
//   - `lifecycle` events become `run.<phase>` (e.g. `run.completed`)
//   - `error` events become `run.error`
//   - everything else becomes `run.event` (generic log line)
func eventKind(ev datastore.CardRunEvent) string {
	switch ev.Kind {
	case "lifecycle":
		if ev.Phase != "" {
			return "run." + ev.Phase
		}
		return "run.lifecycle"
	case "error":
		return "run.error"
	default:
		return "run.event"
	}
}

// matches honours a CSV-style filter or the literal "*" wildcard.
// Empty filter matches nothing — a webhook with no subscriptions sees
// no traffic, which is the kanbots default.
func matches(filter, kind string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return false
	}
	if filter == "*" {
		return true
	}
	for _, name := range strings.Split(filter, ",") {
		if strings.TrimSpace(name) == kind {
			return true
		}
	}
	return false
}

func buildPayload(ev datastore.CardRunEvent, kind string) ([]byte, error) {
	return json.Marshal(map[string]any{
		"event":      kind,
		"run_id":     ev.RunID,
		"kind":       ev.Kind,
		"phase":      ev.Phase,
		"message":    ev.Message,
		"metadata":   ev.Metadata,
		"created_at": ev.CreatedAt,
		"delivered_at": time.Now().UTC(),
	})
}
