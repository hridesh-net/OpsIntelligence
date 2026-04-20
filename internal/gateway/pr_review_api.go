package gateway

// pr_review_api.go — HTTP handlers for the PR review pool.
//
// Endpoints (all auth-gated by the gateway's Bearer check):
//
//   GET  /api/v1/pr-reviews
//        Returns the full task list as JSON (status, elapsed, last event).
//
//   GET  /api/v1/pr-reviews/{task_id}/events?since=N
//        Returns the full event log for a single task (paginated by since).
//
//   POST /api/v1/pr-reviews/{task_id}/cancel
//        Requests cancellation of a pending or running task.
//
// The prReviewAdapter is the concrete type stored in Server.PRReview.
// It is constructed via NewPRReviewAdapter in main.go after the
// PRReviewCmdHandler is initialised.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/subagents"
	"github.com/opsintelligence/opsintelligence/internal/tools"
)

// prReviewAdapter wraps *tools.PRReviewCmdHandler and exposes its monitoring
// surface as HTTP handlers. A pointer to this type is stored on Server.PRReview
// so the gateway does not import the tools package directly (avoiding cycles).
type prReviewAdapter struct {
	h *tools.PRReviewCmdHandler
}

// NewPRReviewAdapter wraps a handler. Returns nil when h is nil so callers
// can safely assign the result to Server.PRReview without a nil-check.
func NewPRReviewAdapter(h *tools.PRReviewCmdHandler) *prReviewAdapter {
	if h == nil {
		return nil
	}
	return &prReviewAdapter{h: h}
}

// ── /api/v1/pr-reviews ───────────────────────────────────────────────────────

type prReviewListResponse struct {
	MaxConcurrent int              `json:"max_concurrent"`
	Tasks         []prReviewTaskJSON `json:"tasks"`
}

type prReviewTaskJSON struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Name       string `json:"name"`
	Elapsed    string `json:"elapsed"`
	LastPhase  string `json:"last_phase,omitempty"`
	LastEvent  string `json:"last_event,omitempty"`
	Error      string `json:"error,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

func taskToJSON(t subagents.Task) prReviewTaskJSON {
	j := prReviewTaskJSON{
		ID:      t.ID,
		Status:  string(t.Status),
		Name:    t.SubAgentNm,
		Elapsed: t.Elapsed().Round(time.Millisecond).String(),
		Error:   t.Error,
	}
	if !t.StartedAt.IsZero() {
		j.StartedAt = t.StartedAt.UTC().Format(time.RFC3339)
	}
	if !t.CompletedAt.IsZero() {
		j.FinishedAt = t.CompletedAt.UTC().Format(time.RFC3339)
	}
	if last := t.LastEvent(); last.Message != "" {
		j.LastPhase = last.Phase
		j.LastEvent = last.Message
	}
	return j
}

func (a *prReviewAdapter) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	mgr := a.h.Manager()
	tasks := mgr.List()
	out := prReviewListResponse{
		MaxConcurrent: mgr.MaxConcurrent,
		Tasks:         make([]prReviewTaskJSON, 0, len(tasks)),
	}
	for _, t := range tasks {
		out.Tasks = append(out.Tasks, taskToJSON(t))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// ── /api/v1/pr-reviews/{task_id}/events  and  /…/{task_id}/cancel ──────────

type prReviewEventsResponse struct {
	TaskID string              `json:"task_id"`
	Status string              `json:"status"`
	Since  int                 `json:"since"`
	Events []prReviewEventJSON `json:"events"`
}

type prReviewEventJSON struct {
	At      string `json:"at"`
	Kind    string `json:"kind"`
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message"`
}

func (a *prReviewAdapter) handleDetail(w http.ResponseWriter, r *http.Request) {
	// Path: /api/v1/pr-reviews/{task_id}[/events | /cancel]
	tail := strings.TrimPrefix(r.URL.Path, "/api/v1/pr-reviews/")
	parts := strings.SplitN(strings.Trim(tail, "/"), "/", 2)
	taskID := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	switch action {
	case "events":
		a.handleEvents(w, r, taskID)
	case "cancel":
		a.handleCancel(w, r, taskID)
	default:
		// bare /api/v1/pr-reviews/{id} → single task summary
		a.handleSingle(w, r, taskID)
	}
}

func (a *prReviewAdapter) handleSingle(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	t, ok := a.h.Manager().Get(taskID)
	if !ok {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(taskToJSON(t))
}

func (a *prReviewAdapter) handleEvents(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	sinceStr := r.URL.Query().Get("since")
	since, _ := strconv.Atoi(sinceStr)
	if since < 0 {
		since = 0
	}

	t, ok := a.h.Manager().Get(taskID)
	if !ok {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}
	events := a.h.Manager().Events(taskID, since)
	out := prReviewEventsResponse{
		TaskID: taskID,
		Status: string(t.Status),
		Since:  since,
		Events: make([]prReviewEventJSON, 0, len(events)),
	}
	for _, ev := range events {
		out.Events = append(out.Events, prReviewEventJSON{
			At:      ev.At.UTC().Format(time.RFC3339Nano),
			Kind:    string(ev.Kind),
			Phase:   ev.Phase,
			Message: ev.Message,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (a *prReviewAdapter) handleCancel(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"use POST to cancel"}`, http.StatusMethodNotAllowed)
		return
	}
	if taskID == "" {
		http.Error(w, `{"error":"task_id is required"}`, http.StatusBadRequest)
		return
	}
	ok := a.h.Manager().Cancel(taskID)
	if !ok {
		http.Error(w, `{"error":"task not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "cancellation_requested", "task_id": taskID})
}
