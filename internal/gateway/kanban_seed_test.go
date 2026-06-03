package gateway_test

import (
	"net/http"
	"testing"
)

// TestBoardSeed_DefaultAgents — a board POST without an `agents` field
// at all gets the preset's starter pool seeded server-side. The Scrun
// fallback in scrun/app.js is the second line of defence; this test
// makes sure the *server* never returns a board with zero agents
// unless the operator explicitly opts out.
func TestBoardSeed_DefaultAgents(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "owner-seed-a", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default"}, cookies)
	boardID, _ := board["id"].(string)
	if boardID == "" {
		t.Fatalf("create board: no id")
	}

	got := doReqDecode(t, svc, mux, http.MethodGet,
		"/api/v1/boards/"+boardID+"/agents", nil, cookies)
	agents, _ := got["agents"].([]any)
	if len(agents) < 2 {
		t.Fatalf("preset 'default' should seed >=2 agents; got %d", len(agents))
	}
}

// TestBoardSeed_ExplicitEmptyAgentsArrayOptsOut — sending `agents: []`
// (an explicit non-nil empty slice) is an opt-out and must result in
// a board with zero agents.
func TestBoardSeed_ExplicitEmptyAgentsArrayOptsOut(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "owner-seed-b", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{
			"name":   "T",
			"preset": "default",
			"agents": []map[string]any{}, // explicit empty
		}, cookies)
	boardID, _ := board["id"].(string)

	got := doReqDecode(t, svc, mux, http.MethodGet,
		"/api/v1/boards/"+boardID+"/agents", nil, cookies)
	agents, _ := got["agents"].([]any)
	if len(agents) != 0 {
		t.Fatalf("explicit empty agents should opt out; got %d agents", len(agents))
	}
}

// TestBoardSeed_PresetVariations — the preset-specific pools shape the
// seed. Research / support / ops all seed at least one Claude-Code
// agent (per presetAgents).
func TestBoardSeed_PresetVariations(t *testing.T) {
	for _, preset := range []string{"research", "support", "ops"} {
		t.Run(preset, func(t *testing.T) {
			svc, mux := newTestAuthService(t, nil)
			mountKanban(svc, mux)
			cookies := loginAs(t, svc, mux, "owner-"+preset, "owner-password-long", "role-owner")

			board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
				map[string]any{"name": "T", "preset": preset}, cookies)
			boardID, _ := board["id"].(string)

			got := doReqDecode(t, svc, mux, http.MethodGet,
				"/api/v1/boards/"+boardID+"/agents", nil, cookies)
			agents, _ := got["agents"].([]any)
			if len(agents) == 0 {
				t.Fatalf("preset %q seeded zero agents", preset)
			}
			foundClaude := false
			for _, a := range agents {
				m, _ := a.(map[string]any)
				if t, _ := m["agent_type"].(string); t == "claude-code" {
					foundClaude = true
				}
			}
			if !foundClaude {
				t.Fatalf("preset %q should include a claude-code agent; got %+v",
					preset, agents)
			}
		})
	}
}
