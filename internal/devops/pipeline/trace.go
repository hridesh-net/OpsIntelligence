// Package pipeline implements the enterprise PR review pipeline:
// parallel stage execution, token-bucket LLM rate limiting, complexity-based
// routing to local intel, and async trace persistence.
package pipeline

import (
	"context"
	"encoding/json"
	"time"
)

// ── Token usage ───────────────────────────────────────────────────────────────

// ProviderTokenUsage records token consumption for one LLM call.
type ProviderTokenUsage struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
}

func (u ProviderTokenUsage) Total() int {
	return u.Prompt + u.Completion
}

// ── Stage record ─────────────────────────────────────────────────────────────

// StageName constants identify each pipeline stage.
const (
	StageFetchName   = "fetch"
	StageAnalyseName = "analyse"
	StageSandboxName = "sandbox"
	StageReviewName  = "review"
	StagePostName    = "post"
)

// stageOrder defines the canonical sequence used to detect pipeline completion.
var stageOrder = []string{
	StageFetchName,
	StageAnalyseName,
	StageSandboxName,
	StageReviewName,
	StagePostName,
}

// StageRecord is the persisted result for one completed pipeline stage.
type StageRecord struct {
	Name       string          `json:"name"`
	StartedAt  time.Time       `json:"started_at"`
	DurationMs int64           `json:"duration_ms"`
	Success    bool            `json:"success"`
	Output     json.RawMessage `json:"output,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// ── Pipeline trace ───────────────────────────────────────────────────────────

// PipelineTrace is the complete persisted record of one PR pipeline run.
type PipelineTrace struct {
	RunID    string `json:"run_id"`
	PRURL    string `json:"pr_url"`
	PRNumber int    `json:"pr_number"`
	Repo     string `json:"repo"` // "owner/repo"

	// CommitSHA is the head commit reviewed; used for deduplication.
	CommitSHA string `json:"commit_sha"`

	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	DurationMs  int64     `json:"duration_ms"`

	Stages []StageRecord `json:"stages"`

	// LLM metadata
	ModelUsed    string             `json:"model_used"`    // e.g. "claude-sonnet-4-6" or "local_intel/gemma"
	LocalIntel   bool               `json:"local_intel"`   // true = local Gemma was used
	ToolsInvoked []string           `json:"tools_invoked"` // tool names called during review
	SkillsUsed   []string           `json:"skills_used"`   // skill names activated
	Tokens       ProviderTokenUsage `json:"tokens"`

	// Review outcome
	Verdict     string `json:"verdict"`      // APPROVE | REQUEST_CHANGES | COMMENT | skipped
	InlineCount int    `json:"inline_count"` // number of inline comments posted
	SandboxPass bool   `json:"sandbox_pass"`

	// Error is non-empty when the pipeline failed before completion.
	Error string `json:"error,omitempty"`
}

// ── Stage event ───────────────────────────────────────────────────────────────

// StageEvent is emitted by each stage function to the TraceAgent via a channel.
// The channel is non-blocking from the caller's perspective (buffered, drop-if-full).
type StageEvent struct {
	RunID     string    `json:"run_id"`
	Stage     string    `json:"stage"`
	StartedAt time.Time `json:"started_at"`
	Success   bool      `json:"success"`
	DurationMs int64    `json:"duration_ms"`

	// Output is stage-specific JSON (FetchOutput, SandboxOutput, etc.)
	Output json.RawMessage `json:"output,omitempty"`

	// LLM-stage fields (only populated by StageReview)
	ModelUsed  string             `json:"model_used,omitempty"`
	LocalIntel bool               `json:"local_intel,omitempty"`
	Tools      []string           `json:"tools,omitempty"`
	Skills     []string           `json:"skills,omitempty"`
	Tokens     ProviderTokenUsage `json:"tokens,omitempty"`

	// Review outcome fields (only populated by StagePost)
	Verdict     string `json:"verdict,omitempty"`
	InlineCount int    `json:"inline_count,omitempty"`
	SandboxPass bool   `json:"sandbox_pass,omitempty"`

	// CommitSHA and Repo are included on the first event so the agent can
	// initialise the trace record without knowing them in advance.
	CommitSHA string `json:"commit_sha,omitempty"`
	Repo      string `json:"repo,omitempty"`
	PRURL     string `json:"pr_url,omitempty"`
	PRNumber  int    `json:"pr_number,omitempty"`

	Error string `json:"error,omitempty"`
}

// ── TraceStore interface ──────────────────────────────────────────────────────

// TraceStore persists completed pipeline traces.
type TraceStore interface {
	Save(ctx context.Context, trace *PipelineTrace) error
	List(ctx context.Context, limit int) ([]*PipelineTrace, error)
	Get(ctx context.Context, runID string) (*PipelineTrace, error)
}
