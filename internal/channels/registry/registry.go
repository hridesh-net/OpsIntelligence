// Package registry provides a plug-in point for channel integrations so that adding a new
// channel requires only one new registration block — not changes scattered across main,
// doctor, and runner (Open/Closed Principle).
package registry

import (
	"context"

	"github.com/opsintelligence/opsintelligence/internal/channels"
)

// Channel is a type alias for [channels.Channel], re-exported so callers of this package
// do not need a separate import of the channels package for StartAll callbacks.
type Channel = channels.Channel

// Entry holds stable, read-only metadata for one channel integration.
type Entry struct {
	// ID is the stable lowercase key used in logs, metrics, and config (e.g. "slack").
	ID string
	// DisplayName is the human-readable label shown in UI and startup logs (e.g. "Slack").
	DisplayName string
}

// Registration ties an Entry to its availability predicate and lifecycle factories.
type Registration struct {
	Entry
	// Configured returns true when cfg contains sufficient data to start this channel.
	// Must be cheap and side-effect-free. Required; panics if nil.
	Configured func() bool
	// Build constructs the live channel. Returns (nil, nil) to skip silently.
	// May be nil; StartAll then skips this channel even when Configured is true.
	Build func() (channels.Channel, error)
	// DoctorPing performs a lightweight connectivity check for the doctor command.
	// If nil, the channel is omitted from DoctorChecks output when configured.
	DoctorPing func(ctx context.Context) error
}

// Registry collects channel registrations and drives lifecycle operations.
// Build a single Registry at startup and pass it to any subsystem that needs
// channel-list information.
type Registry struct {
	regs []Registration
}

// New returns an empty Registry.
func New() *Registry { return &Registry{} }

// Add appends a registration. Panics if reg.Configured is nil.
func (r *Registry) Add(reg Registration) {
	if reg.Configured == nil {
		panic("channels/registry: Registration.Configured must be non-nil for " + reg.ID)
	}
	r.regs = append(r.regs, reg)
}

// ConfiguredDisplayNames returns the DisplayName of every channel whose Configured() is true.
func (r *Registry) ConfiguredDisplayNames() []string {
	var out []string
	for _, reg := range r.regs {
		if reg.Configured() {
			out = append(out, reg.DisplayName)
		}
	}
	return out
}

// ConfiguredIDs returns the ID of every channel whose Configured() is true.
func (r *Registry) ConfiguredIDs() []string {
	var out []string
	for _, reg := range r.regs {
		if reg.Configured() {
			out = append(out, reg.ID)
		}
	}
	return out
}

// StartAll constructs and starts all configured channels.
// For each configured channel whose Build returns a non-nil instance, start is called.
// Returns the count of successfully started channels.
func (r *Registry) StartAll(ctx context.Context, start func(ctx context.Context, ch channels.Channel)) int {
	count := 0
	for _, reg := range r.regs {
		if !reg.Configured() || reg.Build == nil {
			continue
		}
		ch, err := reg.Build()
		if err != nil || ch == nil {
			continue
		}
		start(ctx, ch)
		count++
	}
	return count
}

// DoctorCheck is a single health-check result for one channel.
type DoctorCheck struct {
	ID       string // e.g. "channel.slack"
	Severity string // "ok", "error", or "skipped"
	Message  string
}

// DoctorChecks runs DoctorPing for all registered channels and returns one result per channel.
// Channels without DoctorPing are omitted when configured. Not-configured channels get "skipped".
func (r *Registry) DoctorChecks(ctx context.Context) []DoctorCheck {
	var checks []DoctorCheck
	for _, reg := range r.regs {
		id := "channel." + reg.ID
		if !reg.Configured() {
			checks = append(checks, DoctorCheck{
				ID:       id,
				Severity: "skipped",
				Message:  reg.DisplayName + ": not configured.",
			})
			continue
		}
		if reg.DoctorPing == nil {
			continue
		}
		if err := reg.DoctorPing(ctx); err != nil {
			checks = append(checks, DoctorCheck{
				ID:       id,
				Severity: "error",
				Message:  err.Error(),
			})
		} else {
			checks = append(checks, DoctorCheck{
				ID:       id,
				Severity: "ok",
				Message:  reg.DisplayName + ": connectivity OK.",
			})
		}
	}
	return checks
}
