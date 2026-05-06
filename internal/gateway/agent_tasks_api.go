package gateway

import (
	"net/http"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
	"github.com/opsintelligence/opsintelligence/internal/subagents"
)

// agentTasksResponse is the JSON body for GET /api/v1/agent-tasks.
type agentTasksResponse struct {
	Tasks []agentTaskRow `json:"tasks"`
}

type agentTaskRow struct {
	ID                   string                   `json:"id"`
	SubAgentID           string                   `json:"sub_agent_id"`
	SubAgentName         string                   `json:"sub_agent_name"`
	Status               string                   `json:"status"`
	TaskPreview          string                   `json:"task_preview"`
	StartedAt            time.Time                `json:"started_at,omitempty"`
	CompletedAt          time.Time                `json:"completed_at,omitempty"`
	ElapsedMs            int64                    `json:"elapsed_ms"`
	Iterations           int                      `json:"iterations"`
	ResultPreview        string                   `json:"result_preview,omitempty"`
	Error                string                   `json:"error,omitempty"`
	LastEvent            *subagents.ProgressEvent `json:"last_event,omitempty"`
	EventCount           int                      `json:"event_count"`
	PendingInterventions int                      `json:"pending_interventions"`
}

func (s *AuthService) handleAgentTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p := auth.PrincipalFrom(r.Context())
	if err := rbac.Enforce(r.Context(), p, rbac.PermRunTraceRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if s.Tasks == nil {
		writeJSON(w, http.StatusOK, agentTasksResponse{Tasks: []agentTaskRow{}})
		return
	}

	tasks := s.Tasks.List()
	out := make([]agentTaskRow, 0, len(tasks))
	for _, t := range tasks {
		row := agentTaskRow{
			ID:                   t.ID,
			SubAgentID:           t.SubAgentID,
			SubAgentName:         t.SubAgentNm,
			Status:               string(t.Status),
			StartedAt:            t.StartedAt,
			CompletedAt:          t.CompletedAt,
			ElapsedMs:            t.Elapsed().Milliseconds(),
			Iterations:           t.Iterations,
			ResultPreview:        truncateRunTraceStr(t.Result, 200),
			Error:                t.Error,
			EventCount:           len(t.Events),
			PendingInterventions: len(t.PendingInterventions),
		}
		row.TaskPreview = truncateRunTraceStr(t.Task, 240)
		if ev := t.LastEvent(); ev.Message != "" || ev.Kind != "" {
			evCopy := ev
			row.LastEvent = &evCopy
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, agentTasksResponse{Tasks: out})
}

func truncateRunTraceStr(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
