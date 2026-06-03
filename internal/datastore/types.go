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

// ─────────────────────────────────────────────────────────────────────
// Kanban — boards, columns, cards, runs, events, decisions, agents, personas
// ─────────────────────────────────────────────────────────────────────

// Board is a kanban board (one per workspace / repo).
type Board struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	TeamID     string    `json:"team_id,omitempty"`
	RepoURL    string    `json:"repo_url,omitempty"`
	RepoPath   string    `json:"repo_path,omitempty"`
	Mode       string    `json:"mode"` // local | github
	Config     map[string]any `json:"config,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// BoardColumn is a column on a kanban board.
type BoardColumn struct {
	ID        string    `json:"id"`
	BoardID   string    `json:"board_id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	Color     string    `json:"color,omitempty"`
	WIPLimit  *int      `json:"wip_limit,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// BoardCard is a task / issue card on a board.
type BoardCard struct {
	ID           string         `json:"id"`
	BoardID      string         `json:"board_id"`
	ColumnID     string         `json:"column_id"`
	IssueNumber  *int           `json:"issue_number,omitempty"`
	Title        string         `json:"title"`
	Description  string         `json:"description,omitempty"`
	CardType     string         `json:"card_type"` // bug | feature | refactor | review | spike | chore
	Priority     string         `json:"priority"`  // p0 | p1 | p2 | p3
	Effort       string         `json:"effort,omitempty"` // small | medium | large
	Status       string         `json:"status"`    // queued | running | awaiting | completed | failed | stopped
	Assignee     string         `json:"assignee,omitempty"`
	AssigneeType string         `json:"assignee_type,omitempty"` // agent | user | unassigned
	Branch       string         `json:"branch,omitempty"`
	WorktreePath string         `json:"worktree_path,omitempty"`
	CostUSD      float64        `json:"cost_usd"`
	TokenIn      int64          `json:"token_in"`
	TokenOut     int64          `json:"token_out"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	StartedAt    *time.Time     `json:"started_at,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
}

// CardRun is one agent execution on a card.
type CardRun struct {
	ID           string    `json:"id"`
	CardID       string    `json:"card_id"`
	RunNumber    int       `json:"run_number"`
	AgentID      string    `json:"agent_id"`
	AgentType    string    `json:"agent_type"` // go | claude-code | codex | kimi-code | mcp
	Model        string    `json:"model,omitempty"`
	PersonaID    string    `json:"persona_id,omitempty"`
	Status       string    `json:"status"` // running | awaiting | completed | failed | stopped
	CostUSD      float64   `json:"cost_usd"`
	TokenIn      int64     `json:"token_in"`
	TokenOut     int64     `json:"token_out"`
	ElapsedMs    int64     `json:"elapsed_ms"`
	WorktreePath string    `json:"worktree_path,omitempty"`
	Branch       string    `json:"branch,omitempty"`
	BaseBranch    string    `json:"base_branch,omitempty"`
	RepoPath      string    `json:"repo_path,omitempty"` // for worktree cleanup
	ResultSummary string    `json:"result_summary,omitempty"`
	Error        string    `json:"error,omitempty"`
	CreatedBy    string    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// CardRunEvent is one row in the agent event stream for a run.
type CardRunEvent struct {
	ID       int64          `json:"id"`
	RunID    string         `json:"run_id"`
	Kind     string         `json:"kind"` // tool_start | tool_end | text | decision | error | progress | lifecycle
	Phase    string         `json:"phase,omitempty"`
	Message  string         `json:"message"`
	Metadata map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

// PendingDecision is a question posed by an agent that needs human input.
type PendingDecision struct {
	ID         string     `json:"id"`
	RunID      string     `json:"run_id"`
	CardID     string     `json:"card_id"`
	Question   string     `json:"question"`
	Options    []string   `json:"options"`
	Status     string     `json:"status"` // pending | answered | dismissed
	Answer     string     `json:"answer,omitempty"`
	AnsweredAt *time.Time `json:"answered_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// BoardAgent is a registered agent / provider that can be assigned to cards.
type BoardAgent struct {
	ID         string         `json:"id"`
	BoardID    string         `json:"board_id"`
	Name       string         `json:"name"`
	AgentType  string         `json:"agent_type"` // go | claude-code | codex | kimi-code | mcp
	ProviderID string         `json:"provider_id,omitempty"`
	Config     map[string]any `json:"config,omitempty"`
	IsDefault  bool           `json:"is_default"`
	IsActive   bool           `json:"is_active"`
	CreatedAt  time.Time      `json:"created_at"`
}

// CardAttachment is one file attached to a kanban card. The file bytes
// live on disk under <state_dir>/workspace/kanban/attachments/<card_id>/;
// this row carries the metadata so the UI and API can list / download /
// delete without scanning the filesystem.
type CardAttachment struct {
	ID        string    `json:"id"`
	CardID    string    `json:"card_id"`
	Filename  string    `json:"filename"`
	MimeType  string    `json:"mime_type"`
	SizeBytes int64     `json:"size_bytes"`
	Path      string    `json:"path"`
	CreatedBy string    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Persona is a named system prompt lens.
type Persona struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Icon         string    `json:"icon,omitempty"`
	Description  string    `json:"description,omitempty"`
	SystemPrompt string    `json:"system_prompt"`
	IsBuiltIn    bool      `json:"is_builtin"`
	CreatedBy    string    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// ── Filters ──

// BoardFilter parameterises BoardRepo.List.
type BoardFilter struct {
	TeamID string
	Limit  int
	Offset int
}

// BoardCardFilter parameterises BoardCardRepo.List.
type BoardCardFilter struct {
	BoardID  string
	ColumnID string
	Status   string
	Assignee string
	Limit    int
	Offset   int
}

// CardRunFilter parameterises CardRunRepo.List.
type CardRunFilter struct {
	CardID  string
	AgentID string
	Status  string
	Limit   int
	Offset  int
}

// CardRunEventFilter parameterises CardRunEventRepo.List.
type CardRunEventFilter struct {
	RunID  string
	SinceID int64
	Limit  int
}

// PersonaFilter parameterises PersonaRepo.List.
type PersonaFilter struct {
	BuiltInOnly bool
	Limit       int
	Offset      int
}
