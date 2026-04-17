package datastore

import "context"

// Store is the aggregator every driver implements. It exposes each
// repo plus lifecycle hooks (Ping / Close / Migrate). Upstream code
// (auth, rbac, gateway) depends on these interfaces, not on the
// concrete driver — making SQLite -> Postgres a config flip.
type Store interface {
	// Driver returns the identifier ("sqlite" | "postgres").
	Driver() string

	// Ping verifies the underlying connection is reachable. Cheap.
	Ping(ctx context.Context) error

	// Close releases the underlying connection pool.
	Close() error

	// Migrate brings the schema up to the latest embedded version.
	// Idempotent; safe to call on every boot when
	// DatastoreConfig.Migrations == "auto".
	Migrate(ctx context.Context) error

	// MigrationStatus returns the currently-applied version and the
	// latest embedded version.
	MigrationStatus(ctx context.Context) (applied, latest int, err error)

	// Repository accessors. Each repo is safe for concurrent use.
	Users() UserRepo
	Roles() RoleRepo
	APIKeys() APIKeyRepo
	Sessions() SessionRepo
	Audit() AuditRepo
	TaskHistory() TaskHistoryRepo
	OIDCState() OIDCStateRepo
}

// UserRepo manages User rows.
type UserRepo interface {
	Create(ctx context.Context, u *User) error
	Get(ctx context.Context, id string) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	GetByOIDCSubject(ctx context.Context, issuer, subject string) (*User, error)
	List(ctx context.Context, limit, offset int) ([]User, error)
	Update(ctx context.Context, u *User) error
	SetPassword(ctx context.Context, id, passwordHash string) error
	SetTOTPSecret(ctx context.Context, id, secret string) error
	SetStatus(ctx context.Context, id string, status UserStatus) error
	RecordLogin(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int, error)
}

// RoleRepo manages Role rows and the role↔permission / user↔role joins.
type RoleRepo interface {
	Create(ctx context.Context, r *Role) error
	Get(ctx context.Context, id string) (*Role, error)
	GetByName(ctx context.Context, name string) (*Role, error)
	List(ctx context.Context) ([]Role, error)
	Update(ctx context.Context, r *Role) error
	Delete(ctx context.Context, id string) error

	SetPermissions(ctx context.Context, roleID string, perms []Permission) error
	ListPermissions(ctx context.Context, roleID string) ([]Permission, error)

	AssignToUser(ctx context.Context, userID, roleID string) error
	RevokeFromUser(ctx context.Context, userID, roleID string) error
	ListRolesForUser(ctx context.Context, userID string) ([]Role, error)
	ListPermissionsForUser(ctx context.Context, userID string) ([]Permission, error)
}

// APIKeyRepo manages APIKey rows. Hashing is the caller's job.
type APIKeyRepo interface {
	Create(ctx context.Context, k *APIKey) error
	GetByKeyID(ctx context.Context, keyID string) (*APIKey, error)
	ListForUser(ctx context.Context, userID string) ([]APIKey, error)
	ListAll(ctx context.Context, limit, offset int) ([]APIKey, error)
	TouchUsage(ctx context.Context, id string) error
	Revoke(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
}

// SessionRepo manages Session rows.
type SessionRepo interface {
	Create(ctx context.Context, s *Session) error
	Get(ctx context.Context, id string) (*Session, error)
	Touch(ctx context.Context, id string) error // updates LastSeenAt
	Revoke(ctx context.Context, id string) error
	DeleteExpired(ctx context.Context) (int, error)
	ListForUser(ctx context.Context, userID string) ([]Session, error)
}

// AuditRepo is append-only. Rows are never updated or deleted via the
// interface; retention is handled by a scheduled sweep outside this
// package.
type AuditRepo interface {
	Append(ctx context.Context, e *AuditEntry) error
	List(ctx context.Context, f AuditFilter) ([]AuditEntry, error)
	Count(ctx context.Context, f AuditFilter) (int, error)
}

// TaskHistoryRepo is the durable mirror of subagents.TaskManager.
// Writes are upsert-style because a task's status changes over time.
type TaskHistoryRepo interface {
	Upsert(ctx context.Context, t *TaskHistory) error
	Get(ctx context.Context, taskID string) (*TaskHistory, error)
	List(ctx context.Context, f TaskFilter) ([]TaskHistory, error)

	AppendEvent(ctx context.Context, e *TaskHistoryEvent) error
	ListEvents(ctx context.Context, taskID string, sinceIndex int, limit int) ([]TaskHistoryEvent, error)

	// DeleteOlderThan prunes terminal tasks (status in
	// completed|failed|cancelled) whose CompletedAt is older than the
	// given cutoff. Returns the number of tasks removed.
	DeleteOlderThan(ctx context.Context, cutoff int64) (int, error)
}

// OIDCStateRepo implements a tiny short-lived KV for the authorization
// code flow. Entries are single-use: Take atomically reads-and-deletes.
type OIDCStateRepo interface {
	Put(ctx context.Context, s *OIDCState) error
	Take(ctx context.Context, state string) (*OIDCState, error)
	DeleteExpired(ctx context.Context) (int, error)
}
