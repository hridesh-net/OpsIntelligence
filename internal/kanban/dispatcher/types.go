// Package dispatcher defines the AgentDriver interface and shared event types
// used by the kanban dispatch service to run agents on cards.
package dispatcher

import (
	"context"
	"time"
)

// EventKind classifies events emitted during an agent run.
type EventKind string

const (
	EventKindToolStart    EventKind = "tool_start"
	EventKindToolEnd      EventKind = "tool_end"
	EventKindText         EventKind = "text"
	EventKindDecision     EventKind = "decision"
	EventKindError        EventKind = "error"
	EventKindProgress     EventKind = "progress"
	EventKindLifecycle    EventKind = "lifecycle"
)

// Event is one row in the agent event stream for a run.
type Event struct {
	Kind     EventKind
	Phase    string
	Message  string
	Metadata map[string]any
}

// Result is the outcome of a single agent run.
type Result struct {
	Status        string
	ResultSummary string
	Error         string
	TokenIn       int64
	TokenOut      int64
	CostUSD       float64
	ElapsedMs     int64
}

// AgentDriver is the interface for all agent backends (Go provider, CLI adapters).
type AgentDriver interface {
	// Name returns a unique identifier for this driver, e.g. "go", "claude-code".
	Name() string

	// Run executes the agent for the given card/run context.
	// It emits events on the channel and returns a Result when finished.
	// ctx carries cancellation from the dispatch service (stop run).
	Run(ctx context.Context, req RunRequest, events chan<- Event) Result
}

// RunRequest holds everything a driver needs to execute one run.
type RunRequest struct {
	RunID       string
	CardID      string
	BoardID     string
	AgentID     string
	PersonaID   string
	Model       string
	WorktreePath string
	Branch      string
	BaseBranch  string
	CardTitle   string
	CardDescription string
	SystemPrompt string
	Tools       []string // tool names to register
}

// Registry holds all registered drivers.
type Registry struct {
	drivers map[string]AgentDriver
}

// NewRegistry creates an empty driver registry.
func NewRegistry() *Registry {
	return &Registry{drivers: make(map[string]AgentDriver)}
}

// Register adds a driver. Panics if the name is already taken.
func (r *Registry) Register(d AgentDriver) {
	if _, ok := r.drivers[d.Name()]; ok {
		panic("dispatcher: driver already registered: " + d.Name())
	}
	r.drivers[d.Name()] = d
}

// Get returns the driver for the given agent type, or nil.
func (r *Registry) Get(agentType string) (AgentDriver, bool) {
	d, ok := r.drivers[agentType]
	return d, ok
}

// Len returns the number of registered drivers.
func (r *Registry) Len() int {
	return len(r.drivers)
}

// DurationMS returns milliseconds since start.
func DurationMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}
