package gateway_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// TestCardRelationships_PostListDelete covers the happy path: create a
// blocks edge, list it, delete it.
func TestCardRelationships_PostListDelete(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "rel-owner", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default", "agents": []map[string]any{}}, cookies)
	boardID, _ := board["id"].(string)

	ctx := context.Background()
	cols, _ := svc.Store.BoardColumns().ListByBoard(ctx, boardID)
	a := &datastore.BoardCard{BoardID: boardID, ColumnID: cols[0].ID, Title: "A", Status: "queued"}
	b := &datastore.BoardCard{BoardID: boardID, ColumnID: cols[0].ID, Title: "B", Status: "queued"}
	_ = svc.Store.BoardCards().Create(ctx, a)
	_ = svc.Store.BoardCards().Create(ctx, b)

	created := doReqDecode(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/cards/"+a.ID+"/relationships",
		map[string]any{"dst_card_id": b.ID, "kind": "blocks"}, cookies)
	relID, _ := created["id"].(string)
	if relID == "" {
		t.Fatalf("create: no id; got %+v", created)
	}

	list := doReqDecode(t, svc, mux, http.MethodGet,
		"/api/v1/boards/"+boardID+"/cards/"+a.ID+"/relationships", nil, cookies)
	rels, _ := list["relationships"].([]any)
	if len(rels) != 1 {
		t.Fatalf("expected 1 edge; got %d", len(rels))
	}

	res := doReq(t, svc, mux, http.MethodDelete,
		"/api/v1/boards/"+boardID+"/cards/"+a.ID+"/relationships/"+relID,
		nil, cookies)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: status=%d", res.StatusCode)
	}
}

// TestCardRelationships_ParentCycleRejected verifies the parent cycle
// guard.
func TestCardRelationships_ParentCycleRejected(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "rel-cycle", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default", "agents": []map[string]any{}}, cookies)
	boardID, _ := board["id"].(string)

	ctx := context.Background()
	cols, _ := svc.Store.BoardColumns().ListByBoard(ctx, boardID)
	a := &datastore.BoardCard{BoardID: boardID, ColumnID: cols[0].ID, Title: "A", Status: "queued"}
	b := &datastore.BoardCard{BoardID: boardID, ColumnID: cols[0].ID, Title: "B", Status: "queued"}
	_ = svc.Store.BoardCards().Create(ctx, a)
	_ = svc.Store.BoardCards().Create(ctx, b)

	// A.parent = B; then attempt B.parent = A → should be rejected.
	doReqDecode(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/cards/"+a.ID+"/relationships",
		map[string]any{"dst_card_id": b.ID, "kind": "parent"}, cookies)

	res := doReq(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/cards/"+b.ID+"/relationships",
		map[string]any{"dst_card_id": a.ID, "kind": "parent"}, cookies)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("cycle should be rejected with 409; got %d body=%s", res.StatusCode, readBody(res))
	}
}

// TestCardMove_BlockedByOpenDependency verifies move-to-done refuses
// while a blocking dependency is still open and accepts once it closes.
func TestCardMove_BlockedByOpenDependency(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "rel-mv", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default", "agents": []map[string]any{}}, cookies)
	boardID, _ := board["id"].(string)

	ctx := context.Background()
	cols, _ := svc.Store.BoardColumns().ListByBoard(ctx, boardID)
	// Find Done.
	var doneCol *datastore.BoardColumn
	for i, c := range cols {
		if c.Name == "Done" {
			doneCol = &cols[i]
			break
		}
	}
	if doneCol == nil {
		t.Fatalf("preset must include a Done column")
	}

	a := &datastore.BoardCard{BoardID: boardID, ColumnID: cols[0].ID, Title: "A", Status: "queued"}
	b := &datastore.BoardCard{BoardID: boardID, ColumnID: cols[0].ID, Title: "B", Status: "queued"}
	_ = svc.Store.BoardCards().Create(ctx, a)
	_ = svc.Store.BoardCards().Create(ctx, b)

	// A blocks B; moving A to Done should fail until B closes.
	doReqDecode(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/cards/"+a.ID+"/relationships",
		map[string]any{"dst_card_id": b.ID, "kind": "blocks"}, cookies)

	res := doReq(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/cards/"+a.ID+"/move",
		map[string]any{"column_id": doneCol.ID}, cookies)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("blocked move should 409; got %d body=%s", res.StatusCode, readBody(res))
	}

	// Close B; re-try the move on A.
	b.Status = "completed"
	_ = svc.Store.BoardCards().Update(ctx, b)
	res = doReq(t, svc, mux, http.MethodPost,
		"/api/v1/boards/"+boardID+"/cards/"+a.ID+"/move",
		map[string]any{"column_id": doneCol.ID}, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("move after closing dependency: status=%d body=%s", res.StatusCode, readBody(res))
	}
}
