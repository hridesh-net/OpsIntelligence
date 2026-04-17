// Package datastore is OpsIntelligence's "ops-plane" persistence layer:
// users, roles, permissions, API keys, sessions, audit log, OIDC state,
// and task history. It is strictly separate from agent memory (see
// internal/memory / internal/mempalace) — different tables, different
// DSN, different lifecycle.
//
// The package defines:
//
//   - Domain types (User, Role, APIKey, Session, AuditEntry,
//     TaskHistory, TaskHistoryEvent, OIDCState)
//   - Repository interfaces (UserRepo, RoleRepo, APIKeyRepo,
//     SessionRepo, AuditRepo, TaskHistoryRepo, OIDCStateRepo)
//   - A Store aggregator that exposes every repo
//   - A driver-agnostic Config + Open() factory, switching between the
//     bundled SQLite driver (default, local) and Postgres driver (cloud)
//   - An embedded migration runner with per-driver SQL under
//     migrations/{sqlite,postgres}/NNNN_*.sql
//
// See doc/rbac.md for schema rationale once authN/RBAC lands on top.
package datastore

import "errors"

// Sentinel errors. Driver implementations MUST return these (possibly
// via %w wrapping) so callers can use errors.Is for clean control flow.
var (
	// ErrNotFound means the requested row does not exist.
	ErrNotFound = errors.New("datastore: not found")

	// ErrConflict signals a uniqueness / constraint violation
	// (duplicate username, duplicate api-key id, etc.).
	ErrConflict = errors.New("datastore: conflict")

	// ErrExpired is returned when a session / oidc-state / api-key has
	// passed its expiry timestamp.
	ErrExpired = errors.New("datastore: expired")

	// ErrInvalidConfig signals a malformed Config passed to Open(),
	// e.g. unknown driver or empty DSN.
	ErrInvalidConfig = errors.New("datastore: invalid config")
)
