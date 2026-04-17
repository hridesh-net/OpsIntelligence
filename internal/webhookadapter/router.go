package webhookadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// MaxBodyBytes caps inbound webhook payloads. GitHub's own hard cap is 25 MB
// but typical deliveries are under 500 KB; the smaller cap keeps the gateway
// memory footprint predictable under load.
const MaxBodyBytes = 2 * 1024 * 1024

// RunnerFn is the backgrounded agent runner. The Router invokes it in a
// goroutine after sending the HTTP response; implementations own the full
// run lifecycle (context, audit, memory, any outbound notification).
//
// The context supplied is already detached from the HTTP request — the
// agent will survive the connection closing — and has an adapter-owned
// timeout applied.
type RunnerFn func(ctx context.Context, event Event, prompt string)

// Router serves inbound webhook deliveries for every registered Adapter.
//
// It is mounted under /api/webhook/ and keys on the next path segment.
// Adapters handle their own authentication (HMAC / token / mTLS) via
// Adapter.Verify. The Router's only shared responsibility is the common
// lifecycle: body cap, 2xx status selection, acceptance logging, bounded
// concurrency, and detached context propagation to the RunnerFn.
type Router struct {
	Registry *Registry
	Runner   RunnerFn
	Log      *zap.Logger
	// Timeout bounds each backgrounded agent run. Zero → 10 minutes.
	Timeout time.Duration
	// MaxConcurrent caps in-flight agent runs across ALL adapters. Zero →
	// 10. When full, the Router responds 503 Service Unavailable with a
	// Retry-After hint so senders (GitHub, GitLab, …) retry with backoff
	// rather than queueing unbounded goroutines.
	MaxConcurrent int
	// NowFn is injectable for tests.
	NowFn func() time.Time

	initOnce sync.Once
	sem      chan struct{}
}

func (rt *Router) logger() *zap.Logger {
	if rt.Log != nil {
		return rt.Log
	}
	return zap.NewNop()
}

func (rt *Router) now() time.Time {
	if rt.NowFn != nil {
		return rt.NowFn()
	}
	return time.Now()
}

func (rt *Router) init() {
	rt.initOnce.Do(func() {
		n := rt.MaxConcurrent
		if n <= 0 {
			n = 10
		}
		rt.sem = make(chan struct{}, n)
		if rt.Timeout <= 0 {
			rt.Timeout = 10 * time.Minute
		}
	})
}

// ServeHTTP implements http.Handler.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.init()
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	suffix := strings.TrimPrefix(r.URL.Path, "/api/webhook/")
	suffix = strings.Trim(suffix, "/")
	if suffix == "" {
		writeJSONError(w, http.StatusNotFound, "adapter not specified")
		return
	}
	if rt.Registry == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "no webhook adapter registry configured")
		return
	}
	a := rt.Registry.Lookup(suffix)
	if a == nil {
		// Intentional 404 — lets the gateway fall through to legacy
		// webhook mappings or serve a normal not-found.
		writeJSONError(w, http.StatusNotFound, "no webhook adapter at /api/webhook/"+suffix)
		return
	}
	if !a.Enabled() {
		writeJSONError(w, http.StatusForbidden, "webhook adapter "+a.Name()+" is disabled")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	_ = r.Body.Close()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to read body: "+err.Error())
		return
	}
	if len(body) > MaxBodyBytes {
		writeJSONError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("payload exceeds %d bytes", MaxBodyBytes))
		return
	}

	if err := a.Verify(r, body); err != nil {
		rt.logger().Warn("webhook adapter verify failed",
			zap.String("adapter", a.Name()), zap.Error(err))
		writeJSONError(w, http.StatusUnauthorized, "verification failed")
		return
	}

	event, err := a.Parse(r, body)
	if err != nil {
		rt.logger().Warn("webhook adapter parse failed",
			zap.String("adapter", a.Name()), zap.Error(err))
		writeJSONError(w, http.StatusBadRequest, "parse failed: "+err.Error())
		return
	}
	if event.Source == "" {
		event.Source = a.Name()
	}
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = rt.now()
	}
	event.SessionID = "webhook:" + event.Source + ":" + strings.ToLower(event.Kind) + ":" + uuid.New().String()

	// Special short-circuit for liveness-style events. Adapters can
	// signal "acknowledge but do nothing" by returning a FilterResult
	// with a reason starting with "healthcheck:" — see github.Adapter
	// for the ping event.
	fr := a.Filter(event)
	if !fr.Allowed {
		if strings.HasPrefix(fr.Reason, "healthcheck:") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      "skipped",
			"adapter":     a.Name(),
			"kind":        event.Kind,
			"action":      event.Action,
			"delivery_id": event.DeliveryID,
			"reason":      fr.Reason,
		})
		return
	}

	prompt, err := a.Render(event)
	if err != nil {
		rt.logger().Error("webhook adapter render failed",
			zap.String("adapter", a.Name()), zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "render failed: "+err.Error())
		return
	}

	// Concurrency: non-blocking acquire. Saturated → 503 + Retry-After
	// so the sending platform retries with its own backoff instead of
	// this process fanning goroutines unbounded.
	select {
	case rt.sem <- struct{}{}:
	default:
		rt.logger().Warn("webhook router saturated",
			zap.String("adapter", a.Name()), zap.String("kind", event.Kind),
			zap.Int("max_concurrent", cap(rt.sem)))
		w.Header().Set("Retry-After", "30")
		writeJSONError(w, http.StatusServiceUnavailable, "agent at capacity; please retry")
		return
	}

	// Detach from the request context so closing the HTTP connection
	// doesn't cancel the agent; apply the adapter/router timeout.
	bgCtx := context.Background()
	runCtx, cancel := context.WithTimeout(bgCtx, rt.Timeout)

	rt.logger().Info("webhook accepted",
		zap.String("adapter", a.Name()),
		zap.String("kind", event.Kind),
		zap.String("action", event.Action),
		zap.String("delivery_id", event.DeliveryID),
		zap.String("session_id", event.SessionID),
	)

	if rt.Runner != nil {
		go func() {
			defer cancel()
			defer func() { <-rt.sem }()
			rt.Runner(runCtx, event, prompt)
		}()
	} else {
		// No runner wired (early startup or test): release the slot.
		cancel()
		<-rt.sem
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":      "accepted",
		"adapter":     a.Name(),
		"kind":        event.Kind,
		"action":      event.Action,
		"delivery_id": event.DeliveryID,
		"session_id":  event.SessionID,
		"received_at": event.ReceivedAt.UTC().Format(time.RFC3339),
	})
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, msg)
}
