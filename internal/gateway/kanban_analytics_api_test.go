package gateway_test

import (
	"net/http"
	"testing"
)

// TestBoardAnalytics_ShapeAndCounts — GET /api/v1/boards/{id}/analytics on a
// board with one queued card returns the full response shape with honest
// (mostly zero) values: 7 throughput days, 14 spend days, one stage row per
// column, and the queued card counted in status_counts.
func TestBoardAnalytics_ShapeAndCounts(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "owner-an", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards", map[string]any{
		"name":   "Analytics",
		"preset": "default",
	}, cookies)
	boardID, _ := board["id"].(string)
	if boardID == "" {
		t.Fatalf("create board: no id; got %+v", board)
	}

	card := doReqDecode(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/cards",
		map[string]any{"title": "first task"}, cookies)
	if card["id"] == "" {
		t.Fatalf("create card: no id; got %+v", card)
	}

	an := doReqDecode(t, svc, mux, http.MethodGet,
		"/api/v1/boards/"+boardID+"/analytics", nil, cookies)

	if tp, ok := an["throughput"].([]any); !ok || len(tp) != 7 {
		t.Fatalf("throughput: want 7 days, got %v", an["throughput"])
	}
	if sp, ok := an["spend_trend"].([]any); !ok || len(sp) != 14 {
		t.Fatalf("spend_trend: want 14 days, got %v", an["spend_trend"])
	}
	counts, ok := an["status_counts"].(map[string]any)
	if !ok {
		t.Fatalf("status_counts missing: %+v", an)
	}
	if n, _ := counts["queued"].(float64); n != 1 {
		t.Fatalf("status_counts.queued: want 1, got %v", counts["queued"])
	}
	kpis, ok := an["kpis"].(map[string]any)
	if !ok {
		t.Fatalf("kpis missing: %+v", an)
	}
	if shipped, _ := kpis["tasks_shipped"].(float64); shipped != 0 {
		t.Fatalf("tasks_shipped: want 0, got %v", kpis["tasks_shipped"])
	}
	stages, ok := an["stage_hours"].([]any)
	if !ok || len(stages) == 0 {
		t.Fatalf("stage_hours: want one row per column, got %v", an["stage_hours"])
	}
	if lb, ok := an["leaderboard"].([]any); !ok || len(lb) != 0 {
		t.Fatalf("leaderboard: want empty array, got %v", an["leaderboard"])
	}

	// Unknown board → 404, not a 200 full of zeros.
	res := doReq(t, svc, mux, http.MethodGet, "/api/v1/boards/nope/analytics", nil, cookies)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown board: want 404, got %d", res.StatusCode)
	}
}
