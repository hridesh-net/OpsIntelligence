package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// TestCardComments_PostListUpdateDelete walks the full lifecycle and
// verifies @mention resolution against the board's agents.
func TestCardComments_PostListUpdateDelete(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "cm-owner", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default", "agents": []map[string]any{}}, cookies)
	boardID, _ := board["id"].(string)

	// Seed an agent named "Code Reviewer" so we can mention it.
	agent := doReqDecode(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/agents",
		map[string]any{"name": "Code Reviewer", "agent_type": "claude-code"}, cookies)
	agentID, _ := agent["id"].(string)

	// Seed a card on the first column so we have a target.
	ctx := context.Background()
	cols, _ := svc.Store.BoardColumns().ListByBoard(ctx, boardID)
	card := &datastore.BoardCard{
		BoardID: boardID, ColumnID: cols[0].ID, Title: "review pls", Status: "queued",
	}
	if err := svc.Store.BoardCards().Create(ctx, card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	// POST a comment with an @mention.
	created := doReqDecode(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/cards/"+card.ID+"/comments",
		map[string]any{"body": "Looks good @code-reviewer, ship it"}, cookies)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("create comment: no id; got %+v", created)
	}
	if got := created["mentions"]; got == nil || !strings.Contains(got.(string), agentID) {
		t.Fatalf("expected mentions to include agent id %q; got %v", agentID, got)
	}

	// LIST returns the comment.
	list := doReqDecode(t, svc, mux, http.MethodGet,
		"/api/v1/boards/"+boardID+"/cards/"+card.ID+"/comments", nil, cookies)
	got, _ := list["comments"].([]any)
	if len(got) != 1 {
		t.Fatalf("expected 1 comment in list; got %d", len(got))
	}

	// PUT edits the body.
	res := doReq(t, svc, mux, http.MethodPut,
		"/api/v1/boards/"+boardID+"/cards/"+card.ID+"/comments/"+id,
		map[string]any{"body": "Edited"}, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT: status=%d body=%s", res.StatusCode, readBody(res))
	}
	var edited datastore.CardComment
	_ = json.NewDecoder(res.Body).Decode(&edited)
	if edited.Body != "Edited" || edited.EditedAt == nil {
		t.Fatalf("edit did not stick: %+v", edited)
	}

	// DELETE soft-deletes; LIST excludes by default.
	res = doReq(t, svc, mux, http.MethodDelete,
		"/api/v1/boards/"+boardID+"/cards/"+card.ID+"/comments/"+id, nil, cookies)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: status=%d", res.StatusCode)
	}
	list2 := doReqDecode(t, svc, mux, http.MethodGet,
		"/api/v1/boards/"+boardID+"/cards/"+card.ID+"/comments", nil, cookies)
	got2, _ := list2["comments"].([]any)
	if len(got2) != 0 {
		t.Fatalf("soft-deleted comment should not appear in default list; got %d", len(got2))
	}
}
