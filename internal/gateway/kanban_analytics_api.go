package gateway

import (
	"net/http"
	"sort"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// boardAnalyticsResponse is the JSON body for GET /api/v1/boards/{id}/analytics.
// Every series is computed from persisted cards/runs — no synthetic data — so
// a fresh board legitimately returns zeros.
type boardAnalyticsResponse struct {
	KPIs         analyticsKPIs           `json:"kpis"`
	StatusCounts map[string]int          `json:"status_counts"`
	Throughput   []throughputDay         `json:"throughput"`
	SpendTrend   []spendDay              `json:"spend_trend"`
	StageHours   []stageHoursRow         `json:"stage_hours"`
	Leaderboard  []leaderboardRow        `json:"leaderboard"`
}

type analyticsKPIs struct {
	TasksShipped  int     `json:"tasks_shipped"`
	ShippedLast7  int     `json:"shipped_last7"`
	AvgCycleHours float64 `json:"avg_cycle_hours"`
	SuccessRate   float64 `json:"success_rate"` // % of finished runs that completed cleanly
	SpendTodayUSD float64 `json:"spend_today_usd"`
	SpendTotalUSD float64 `json:"spend_total_usd"`
}

type throughputDay struct {
	Date    string `json:"date"` // YYYY-MM-DD
	Day     string `json:"day"`  // Mon..Sun
	Shipped int    `json:"shipped"`
	Started int    `json:"started"`
}

type spendDay struct {
	Date string  `json:"date"`
	USD  float64 `json:"usd"`
}

type stageHoursRow struct {
	ColumnID string  `json:"column_id"`
	Name     string  `json:"name"`
	AvgHours float64 `json:"avg_hours"` // mean time since last card update, per column
	Cards    int     `json:"cards"`
}

type leaderboardRow struct {
	AgentID    string  `json:"agent_id"`
	Name       string  `json:"name"`
	AgentType  string  `json:"agent_type"`
	Model      string  `json:"model,omitempty"`
	Tasks      int     `json:"tasks"`
	SuccessPct float64 `json:"success_pct"`
	SpendUSD   float64 `json:"spend_usd"`
	Active     int     `json:"active"`
}

func (s *AuthService) handleBoardAnalytics(w http.ResponseWriter, r *http.Request, boardID string) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p := auth.PrincipalFrom(r.Context())
	if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	ctx := r.Context()
	if _, err := s.Store.Boards().Get(ctx, boardID); err != nil {
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "board not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	cards, err := s.Store.BoardCards().List(ctx, datastore.BoardCardFilter{BoardID: boardID, Limit: 1000})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	columns, _ := s.Store.BoardColumns().ListByBoard(ctx, boardID)
	agents, _ := s.Store.BoardAgents().ListByBoard(ctx, boardID)

	now := time.Now()
	today := now.Format("2006-01-02")
	resp := boardAnalyticsResponse{
		StatusCounts: map[string]int{},
		Throughput:   make([]throughputDay, 7),
		SpendTrend:   make([]spendDay, 14),
		StageHours:   []stageHoursRow{},
		Leaderboard:  []leaderboardRow{},
	}
	for i := 0; i < 7; i++ {
		d := now.AddDate(0, 0, i-6)
		resp.Throughput[i] = throughputDay{Date: d.Format("2006-01-02"), Day: d.Format("Mon")}
	}
	for i := 0; i < 14; i++ {
		resp.SpendTrend[i] = spendDay{Date: now.AddDate(0, 0, i-13).Format("2006-01-02")}
	}
	throughputIdx := func(t time.Time) int {
		days := int(now.Truncate(24*time.Hour).Sub(t.Truncate(24*time.Hour)).Hours() / 24)
		if days < 0 || days > 6 {
			return -1
		}
		return 6 - days
	}
	spendIdx := func(t time.Time) int {
		days := int(now.Truncate(24*time.Hour).Sub(t.Truncate(24*time.Hour)).Hours() / 24)
		if days < 0 || days > 13 {
			return -1
		}
		return 13 - days
	}

	// ── Cards: status counts, cycle time, throughput ──────────────────
	var cycleSum time.Duration
	var cycleN int
	cardsByColumn := map[string][]datastore.BoardCard{}
	for _, c := range cards {
		resp.StatusCounts[c.Status]++
		cardsByColumn[c.ColumnID] = append(cardsByColumn[c.ColumnID], c)
		resp.KPIs.SpendTotalUSD += c.CostUSD
		if c.Status == "completed" {
			resp.KPIs.TasksShipped++
			if c.CompletedAt != nil {
				if now.Sub(*c.CompletedAt) <= 7*24*time.Hour {
					resp.KPIs.ShippedLast7++
				}
				if i := throughputIdx(*c.CompletedAt); i >= 0 {
					resp.Throughput[i].Shipped++
				}
				if c.StartedAt != nil && c.CompletedAt.After(*c.StartedAt) {
					cycleSum += c.CompletedAt.Sub(*c.StartedAt)
					cycleN++
				}
			}
		}
		if c.StartedAt != nil {
			if i := throughputIdx(*c.StartedAt); i >= 0 {
				resp.Throughput[i].Started++
			}
		}
	}
	if cycleN > 0 {
		resp.KPIs.AvgCycleHours = cycleSum.Hours() / float64(cycleN)
	}

	// ── Stage hours: mean time since last update for cards sitting in
	// each column (approximation — column transitions are not journaled). ──
	for _, col := range columns {
		row := stageHoursRow{ColumnID: col.ID, Name: col.Name}
		var sum float64
		for _, c := range cardsByColumn[col.ID] {
			sum += now.Sub(c.UpdatedAt).Hours()
			row.Cards++
		}
		if row.Cards > 0 {
			row.AvgHours = sum / float64(row.Cards)
		}
		resp.StageHours = append(resp.StageHours, row)
	}

	// ── Runs: success rate, spend trend, per-agent leaderboard ────────
	type agentAgg struct {
		tasks, completed, finished, active int
		spend                              float64
		model                              string
		lastRun                            time.Time
	}
	aggs := map[string]*agentAgg{}
	var runsCompleted, runsFinished int
	for _, c := range cards {
		runs, err := s.Store.CardRuns().List(ctx, datastore.CardRunFilter{CardID: c.ID, Limit: 200})
		if err != nil {
			continue
		}
		for _, run := range runs {
			a := aggs[run.AgentID]
			if a == nil {
				a = &agentAgg{}
				aggs[run.AgentID] = a
			}
			a.tasks++
			a.spend += run.CostUSD
			if run.CreatedAt.After(a.lastRun) && run.Model != "" {
				a.model = run.Model
				a.lastRun = run.CreatedAt
			}
			spendAt := run.CreatedAt
			if run.CompletedAt != nil {
				spendAt = *run.CompletedAt
			}
			if i := spendIdx(spendAt); i >= 0 {
				resp.SpendTrend[i].USD += run.CostUSD
			}
			if spendAt.Format("2006-01-02") == today {
				resp.KPIs.SpendTodayUSD += run.CostUSD
			}
			switch run.Status {
			case "completed":
				a.completed++
				a.finished++
				runsCompleted++
				runsFinished++
			case "failed", "stopped":
				a.finished++
				runsFinished++
			case "running", "awaiting":
				a.active++
			}
		}
	}
	if runsFinished > 0 {
		resp.KPIs.SuccessRate = 100 * float64(runsCompleted) / float64(runsFinished)
	}

	agentName := map[string]datastore.BoardAgent{}
	for _, a := range agents {
		agentName[a.ID] = a
	}
	for id, a := range aggs {
		row := leaderboardRow{
			AgentID:  id,
			Name:     id,
			Tasks:    a.tasks,
			SpendUSD: a.spend,
			Model:    a.model,
			Active:   a.active,
		}
		if meta, ok := agentName[id]; ok {
			row.Name = meta.Name
			row.AgentType = meta.AgentType
		}
		if a.finished > 0 {
			row.SuccessPct = 100 * float64(a.completed) / float64(a.finished)
		}
		resp.Leaderboard = append(resp.Leaderboard, row)
	}
	sort.Slice(resp.Leaderboard, func(i, j int) bool {
		return resp.Leaderboard[i].Tasks > resp.Leaderboard[j].Tasks
	})

	writeJSON(w, http.StatusOK, resp)
}
