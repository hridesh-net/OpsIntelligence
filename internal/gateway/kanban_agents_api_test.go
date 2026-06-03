package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/gateway"
)

// mountKanban wires the kanban handler onto the test mux with the same
// Protect / ProtectCSRF policy server.go uses in production. AuthService.Mount
// intentionally does not register kanban (server.go does it separately
// with phase2OrLegacyAuth), so each kanban test calls this once.
func mountKanban(svc *gateway.AuthService, mux *http.ServeMux) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			svc.Protect(http.HandlerFunc(svc.HandleKanban)).ServeHTTP(w, r)
			return
		}
		svc.ProtectCSRF(http.HandlerFunc(svc.HandleKanban)).ServeHTTP(w, r)
	})
	mux.Handle("/api/v1/boards", h)
	mux.Handle("/api/v1/boards/", h)
	mux.Handle("/api/v1/runs/", h)
	mux.Handle("/api/v1/personas", h)
}

// doReqDecode is doReq + decode of the JSON body into a map.
func doReqDecode(t *testing.T, svc *gateway.AuthService, mux http.Handler, method, path string, body any, cookies []*http.Cookie) map[string]any {
	t.Helper()
	res := doReq(t, svc, mux, method, path, body, cookies)
	if res.StatusCode >= 300 {
		t.Fatalf("%s %s: status=%d body=%s", method, path, res.StatusCode, readBody(res))
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

// TestBoardAgents_Update_MergesConfig — PUT with a name + partial
// config patch updates both and leaves config keys not in the patch
// alone.
func TestBoardAgents_Update_MergesConfig(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "owner-a", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards", map[string]any{
		"name":   "T",
		"preset": "default",
	}, cookies)
	boardID, _ := board["id"].(string)
	if boardID == "" {
		t.Fatalf("create board: no id; got %+v", board)
	}

	agent := doReqDecode(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/agents",
		map[string]any{
			"name":       "alpha",
			"agent_type": "claude-code",
			"config": map[string]any{
				"role":  "researcher",
				"model": "claude-opus-4-7",
			},
		}, cookies)
	agentID, _ := agent["id"].(string)
	if agentID == "" {
		t.Fatalf("create agent: no id; got %+v", agent)
	}

	res := doReq(t, svc, mux, http.MethodPut,
		"/api/v1/boards/"+boardID+"/agents/"+agentID,
		map[string]any{
			"name": "alpha-renamed",
			"config": map[string]any{
				"instructions": "be terse",
			},
		}, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT agent: status=%d body=%s", res.StatusCode, readBody(res))
	}
	var updated datastore.BoardAgent
	if err := json.NewDecoder(res.Body).Decode(&updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated.Name != "alpha-renamed" {
		t.Fatalf("name not updated: %q", updated.Name)
	}
	if got := updated.Config["instructions"]; got != "be terse" {
		t.Fatalf("instructions not set: %v", got)
	}
	if got := updated.Config["model"]; got != "claude-opus-4-7" {
		t.Fatalf("pre-existing model key was dropped: %v", got)
	}
	if got := updated.Config["role"]; got != "researcher" {
		t.Fatalf("pre-existing role key was dropped: %v", got)
	}
}

// TestBoardAgents_Update_NilDeletesKey — sending a config key with a
// nil value removes that key from the stored config.
func TestBoardAgents_Update_NilDeletesKey(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "owner-b", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default"}, cookies)
	boardID, _ := board["id"].(string)

	agent := doReqDecode(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/agents",
		map[string]any{
			"name": "beta", "agent_type": "codex",
			"config": map[string]any{
				"role":   "fixer",
				"memory": map[string]any{"mode": "session"},
			},
		}, cookies)
	agentID, _ := agent["id"].(string)

	res := doReq(t, svc, mux, http.MethodPut,
		"/api/v1/boards/"+boardID+"/agents/"+agentID,
		map[string]any{"config": map[string]any{"role": nil}},
		cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT: status=%d body=%s", res.StatusCode, readBody(res))
	}
	var updated datastore.BoardAgent
	_ = json.NewDecoder(res.Body).Decode(&updated)
	if _, ok := updated.Config["role"]; ok {
		t.Fatalf("nil-value should have deleted role; config=%+v", updated.Config)
	}
	if updated.Config["memory"] == nil {
		t.Fatalf("memory key should have survived; config=%+v", updated.Config)
	}
}

// TestBoardAgents_Delete_NoActiveRuns — DELETE succeeds when there are
// no non-terminal runs, then a follow-up GET returns 404.
func TestBoardAgents_Delete_NoActiveRuns(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "owner-c", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default"}, cookies)
	boardID, _ := board["id"].(string)

	agent := doReqDecode(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/agents",
		map[string]any{"name": "gamma", "agent_type": "gemini"}, cookies)
	agentID, _ := agent["id"].(string)

	res := doReq(t, svc, mux, http.MethodDelete,
		"/api/v1/boards/"+boardID+"/agents/"+agentID, nil, cookies)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: status=%d body=%s", res.StatusCode, readBody(res))
	}

	res = doReq(t, svc, mux, http.MethodGet,
		"/api/v1/boards/"+boardID+"/agents/"+agentID, nil, cookies)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("GET after delete: status=%d body=%s", res.StatusCode, readBody(res))
	}
}

// TestBoardAgents_Delete_BlockedByActiveRun — DELETE returns 409 when
// a non-terminal card_run references this agent. Seeds the run via the
// store directly so we don't pull in the dispatcher fixture surface.
func TestBoardAgents_Delete_BlockedByActiveRun(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "owner-d", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default"}, cookies)
	boardID, _ := board["id"].(string)

	agent := doReqDecode(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/agents",
		map[string]any{"name": "delta", "agent_type": "claude-code"}, cookies)
	agentID, _ := agent["id"].(string)

	ctx := context.Background()

	cols, err := svc.Store.BoardColumns().ListByBoard(ctx, boardID)
	if err != nil || len(cols) == 0 {
		t.Fatalf("expected preset columns; got %d (err=%v)", len(cols), err)
	}
	c := &datastore.BoardCard{
		BoardID:  boardID,
		ColumnID: cols[0].ID,
		Title:    "seed",
		Status:   "queued",
	}
	if err := svc.Store.BoardCards().Create(ctx, c); err != nil {
		t.Fatalf("seed card: %v", err)
	}
	cardID := c.ID

	if err := svc.Store.CardRuns().Create(ctx, &datastore.CardRun{
		CardID:    cardID,
		RunNumber: 1,
		AgentID:   agentID,
		AgentType: "claude-code",
		Status:    "running",
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	res := doReq(t, svc, mux, http.MethodDelete,
		"/api/v1/boards/"+boardID+"/agents/"+agentID, nil, cookies)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("DELETE during active run: expected 409, got %d body=%s",
			res.StatusCode, readBody(res))
	}
}
