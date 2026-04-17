// Package webhookadapter defines a pluggable contract for inbound action
// webhooks. It mirrors the shape of internal/channels/adapter but for a
// different purpose: where a ChannelAdapter ferries conversational messages
// to and from humans, a webhook Adapter ingests platform events (GitHub PR
// opened, GitLab pipeline failed, Datadog monitor alerted, …) and converts
// them into an agent run.
//
// Each adapter is responsible for:
//
//   - identifying itself via a stable Name (e.g. "github", "gitlab")
//   - declaring the URL path it listens on ("/api/webhook/<Path()>")
//   - verifying the inbound request's authenticity (HMAC, token, mTLS)
//   - parsing the platform's payload into a normalised Event
//   - deciding whether the event should trigger the agent (Filter)
//   - rendering a prompt for the agent run (Render)
//
// The gateway mounts a single Router under /api/webhook/ that dispatches
// to the right Adapter by path, runs Verify → Parse → Filter → Render, and
// hands the resulting prompt to a backgrounded agent run with bounded
// concurrency. No adapter ever blocks the HTTP response on the agent;
// callers always receive a 2xx as soon as validation + acceptance is done.
package webhookadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Event is the normalised inbound action from a webhook adapter. Fields
// that aren't meaningful for a given platform are left zero-valued.
type Event struct {
	// Source is the adapter name (e.g. "github").
	Source string
	// Kind is the event type within that source (e.g. "pull_request",
	// "workflow_run", "pipeline", "alert").
	Kind string
	// Action refines the kind when the platform exposes one (e.g.
	// "opened", "synchronize", "completed"). Empty for events without an
	// action concept.
	Action string
	// DeliveryID is the platform's per-delivery GUID (X-GitHub-Delivery,
	// X-GitLab-Event-UUID, …). Useful for log correlation and dedup.
	DeliveryID string
	// Repository, when the platform has that concept, is the canonical
	// "owner/name" string (e.g. "acme/widgets"). Empty otherwise.
	Repository string
	// Sender is the login / principal that triggered the event, when known.
	Sender string
	// ReceivedAt is when the gateway accepted the event (UTC).
	ReceivedAt time.Time
	// Payload is the fully parsed JSON body. Adapters should preserve the
	// native shape so prompt templates can reach nested fields directly.
	Payload map[string]interface{}
	// RawBody is the unparsed request body (for logging / replay).
	RawBody []byte
	// SessionID is assigned by the Router before Render runs so templates
	// and the runner can reference it.
	SessionID string
}

// FilterResult describes whether an event should trigger the agent.
type FilterResult struct {
	Allowed bool
	// Reason is a short operator-facing note returned in the HTTP response
	// when Allowed == false (so callers can see why their event was a no-op).
	Reason string
}

// Adapter is the contract implemented by every webhook provider.
//
// A typical request flow:
//
//  1. Router matches Path() → calls Enabled(); if false, 404.
//  2. Reads body (2 MiB cap), calls Verify(). Failure → 401.
//  3. Parse(r, body) → Event. Failure → 400.
//  4. Filter(event) → FilterResult. If !Allowed, responds 202 skipped.
//  5. Render(event) → prompt string. Failure → 500.
//  6. Router hands (event, prompt) to the backgrounded Runner. 202 returned.
type Adapter interface {
	// Name returns the stable identifier (e.g. "github"). It must match
	// the config map key.
	Name() string
	// Path is the URL suffix under /api/webhook/ (e.g. "github" for
	// /api/webhook/github). May be overridden by the adapter's own
	// config; the Router reads Path() after the adapter is constructed.
	Path() string
	// Enabled reports whether the adapter should serve requests. When
	// false the Router returns 404 for Path() (so disabling a provider
	// doesn't silently accept traffic).
	Enabled() bool
	// Verify authenticates the inbound request. Most implementations use
	// HMAC over the body; some use a shared token. Returning a
	// non-nil error → the Router sends 401 Unauthorized.
	Verify(r *http.Request, body []byte) error
	// Parse extracts a normalised Event from the request. The Router
	// already holds the (already-verified) body and passes it in; Parse
	// is free to read request headers for the event kind / delivery id.
	Parse(r *http.Request, body []byte) (Event, error)
	// Filter decides whether the event should trigger an agent run. The
	// Router still returns 202 for filtered events (GitHub treats any
	// 2xx as a successful delivery) but the body indicates a skip.
	Filter(event Event) FilterResult
	// Render builds the agent prompt for this event. Templates are free
	// to reference nested payload fields directly.
	Render(event Event) (string, error)
}

// HealthAdapter is an optional extension: adapters that can synchronously
// verify their configuration (secret present, reachable upstream, …) should
// implement it so the `doctor` command can include them in its sweep.
type HealthAdapter interface {
	// CheckHealth returns nil when the adapter is ready to serve.
	CheckHealth(ctx context.Context) error
}

// Registry holds the set of registered adapters, indexed by Path().
//
// It is intentionally dead-simple — we rebuild it on every config reload
// rather than mutating it in place.
type Registry struct {
	byPath map[string]Adapter
	byName map[string]Adapter
	order  []string // preserves registration order for deterministic iteration
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		byPath: make(map[string]Adapter),
		byName: make(map[string]Adapter),
	}
}

// Register adds an adapter. Duplicates by Name() or Path() return an error —
// misconfiguration should fail loudly at startup.
func (r *Registry) Register(a Adapter) error {
	if a == nil {
		return errors.New("webhookadapter: nil adapter")
	}
	name := strings.TrimSpace(a.Name())
	if name == "" {
		return errors.New("webhookadapter: adapter has empty Name()")
	}
	path := strings.Trim(a.Path(), "/")
	if path == "" {
		return fmt.Errorf("webhookadapter: adapter %q has empty Path()", name)
	}
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("webhookadapter: duplicate adapter name %q", name)
	}
	if _, dup := r.byPath[path]; dup {
		return fmt.Errorf("webhookadapter: duplicate adapter path %q (adapter %q)", path, name)
	}
	r.byName[name] = a
	r.byPath[path] = a
	r.order = append(r.order, name)
	return nil
}

// Lookup returns the adapter registered for the given URL suffix (the part
// after /api/webhook/), or nil if none matches.
func (r *Registry) Lookup(path string) Adapter {
	return r.byPath[strings.Trim(path, "/")]
}

// ByName returns the adapter registered under name, or nil.
func (r *Registry) ByName(name string) Adapter {
	return r.byName[strings.TrimSpace(name)]
}

// List returns all registered adapters in registration order.
func (r *Registry) List() []Adapter {
	out := make([]Adapter, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n])
	}
	return out
}
