package datastore

import "time"

// ─────────────────────────────────────────────────────────────────────
// Identity
// ─────────────────────────────────────────────────────────────────────

// UserStatus enumerates the lifecycle state of a user row.
type UserStatus string

const (
	// UserActive is the normal post-onboarding state.
	UserActive UserStatus = "active"
	// UserDisabled means login + API-key creation are blocked. Audit
	// history is retained.
	UserDisabled UserStatus = "disabled"
	// UserInvited is a placeholder row created by an admin that has not
	// been claimed yet.
	UserInvited UserStatus = "invited"
)

// User is a principal in the ops plane. Exactly one of PasswordHash or
// OIDCSubject+OIDCIssuer must be populated (or both, if the operator
// explicitly enables local+OIDC for the same account).
type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	Email        string     `json:"email,omitempty"`
	DisplayName  string     `json:"display_name,omitempty"`
	PasswordHash string     `json:"-"` // never serialised
	TOTPSecret   string     `json:"-"` // never serialised
	Status       UserStatus `json:"status"`
	OIDCSubject  string     `json:"oidc_subject,omitempty"`
	OIDCIssuer   string     `json:"oidc_issuer,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────
// RBAC
// ─────────────────────────────────────────────────────────────────────

// Role is a named bundle of permissions. Built-in roles have
// IsBuiltIn = true and are recreated by migrations on every boot.
type Role struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	IsBuiltIn   bool      `json:"is_builtin"`
	CreatedAt   time.Time `json:"created_at"`
}

// Permission is a dotted, lowercase key like "tasks.read" or
// "users.manage". The authoritative list lives in internal/rbac.
type Permission string

// ─────────────────────────────────────────────────────────────────────
// API keys
// ─────────────────────────────────────────────────────────────────────

// APIKey is a long-lived bearer credential for service accounts
// (webhooks, CI, external dashboards). The wire format is
// "opi_<key_id>_<secret>". Only KeyID and Hash are persisted; Secret
// is returned exactly once at creation time and never stored.
type APIKey struct {
	ID         string     `json:"id"`
	KeyID      string     `json:"key_id"`
	Hash       string     `json:"-"` // argon2id(secret)
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"` // subset of the owner's roles / perms
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────
// Sessions
// ─────────────────────────────────────────────────────────────────────

// Session is a server-side record backing the dashboard's HttpOnly
// cookie. Deleting the row revokes every browser holding that cookie.
type Session struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	UserAgent  string     `json:"user_agent,omitempty"`
	RemoteAddr string     `json:"remote_addr,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────
// Audit
// ─────────────────────────────────────────────────────────────────────

// ActorType distinguishes who performed an audited action.
type ActorType string

const (
	ActorUser   ActorType = "user"
	ActorAPIKey ActorType = "apikey"
	ActorSystem ActorType = "system"
)

// AuditEntry is one immutable row in the audit log. Metadata carries
// arbitrary JSON — callers should keep it small and PII-free.
type AuditEntry struct {
	ID           int64          `json:"id"`
	Timestamp    time.Time      `json:"timestamp"`
	ActorType    ActorType      `json:"actor_type"`
	ActorID      string         `json:"actor_id,omitempty"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type,omitempty"`
	ResourceID   string         `json:"resource_id,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	RemoteAddr   string         `json:"remote_addr,omitempty"`
	UserAgent    string         `json:"user_agent,omitempty"`
	Success      bool           `json:"success"`
	ErrorMessage string         `json:"error_message,omitempty"`
}

// AuditFilter parameterises AuditRepo.List.
type AuditFilter struct {
	Since     *time.Time
	Until     *time.Time
	Actor     string // actor_id exact match
	ActorType ActorType
	Action    string // action prefix match ("tasks." matches "tasks.cancel")
	Resource  string // resource_type exact
	Success   *bool
	Limit     int // 0 -> 100
	Offset    int
}

// ─────────────────────────────────────────────────────────────────────
// Task history (durable mirror of subagents.TaskManager)
// ─────────────────────────────────────────────────────────────────────

// TaskHistory is the long-lived record of a sub-agent task run. Live
// state is still the source of truth in TaskManager; this table powers
// the dashboard's "past runs" view and retention.
type TaskHistory struct {
	ID          string     `json:"id"`
	TaskID      string     `json:"task_id"`
	SessionID   string     `json:"session_id,omitempty"`
	SubAgentID  string     `json:"subagent_id,omitempty"`
	Goal        string     `json:"goal,omitempty"`
	Prompt      string     `json:"prompt,omitempty"`
	Response    string     `json:"response,omitempty"`
	Status      string     `json:"status"` // pending|running|completed|failed|cancelled
	Iterations  int        `json:"iterations"`
	Error       string     `json:"error,omitempty"`
	ActorID     string     `json:"actor_id,omitempty"` // principal that launched it
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// TaskHistoryEvent is one row of the per-task progress stream persisted
// for later inspection (mirrors subagents.ProgressEvent).
type TaskHistoryEvent struct {
	TaskID    string         `json:"task_id"`
	Index     int            `json:"index"`
	Kind      string         `json:"kind"`             // progress|blocked|error|lifecycle
	Phase     string         `json:"phase,omitempty"`  // planning|tool|verify|...
	Source    string         `json:"source,omitempty"` // child|master|system
	Message   string         `json:"message"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// TaskFilter parameterises TaskHistoryRepo.ListTasks.
type TaskFilter struct {
	Status    string // exact match; empty -> any
	ActorID   string
	SessionID string
	Since     *time.Time
	Until     *time.Time
	Limit     int
	Offset    int
}

// ─────────────────────────────────────────────────────────────────────
// OIDC state (short-lived anti-CSRF bucket)
// ─────────────────────────────────────────────────────────────────────

// OIDCState holds an in-flight authorization-code flow so the callback
// handler can validate the state/nonce pair and resume where the user
// left off.
type OIDCState struct {
	State         string
	Nonce         string
	PKCEVerifier  string
	RedirectAfter string
	CreatedAt     time.Time
	ExpiresAt     time.Time
}
