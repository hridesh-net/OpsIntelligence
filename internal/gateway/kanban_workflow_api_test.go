package gateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// TestWorkflowSave_UpdatesRenamesAndInserts — happy path covering the
// three diff cases at once: rename an existing column, insert a new
// one, and reorder via the position field.
func TestWorkflowSave_UpdatesRenamesAndInserts(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "wf-a", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default", "agents": []map[string]any{}}, cookies)
	boardID, _ := board["id"].(string)

	cols, _ := svc.Store.BoardColumns().ListByBoard(context.Background(), boardID)
	if len(cols) < 3 {
		t.Fatalf("expected preset columns; got %d", len(cols))
	}

	// Rename the first column, swap positions of the next two, add a new
	// one at the end. Leave the remaining columns unchanged.
	payload := map[string]any{
		"columns": []map[string]any{
			{"id": cols[0].ID, "name": "Inbox-renamed", "position": 0, "color": cols[0].Color},
			{"id": cols[2].ID, "name": cols[2].Name, "position": 1, "color": cols[2].Color},
			{"id": cols[1].ID, "name": cols[1].Name, "position": 2, "color": cols[1].Color},
			{"name": "Brand New", "position": len(cols), "color": "#ff00ff", "gate": "human"},
		},
	}
	res := doReq(t, svc, mux, http.MethodPut,
		"/api/v1/boards/"+boardID+"/workflow", payload, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT workflow: status=%d body=%s", res.StatusCode, readBody(res))
	}
	var body struct {
		Columns []datastore.BoardColumn `json:"columns"`
		Deleted []string                `json:"deleted"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Columns) != 4 {
		t.Fatalf("expected 4 saved columns; got %d", len(body.Columns))
	}
	if body.Columns[0].Name != "Inbox-renamed" {
		t.Fatalf("rename did not stick: %q", body.Columns[0].Name)
	}
	if body.Columns[3].Name != "Brand New" || body.Columns[3].ID == "" {
		t.Fatalf("insert did not stick or got no id: %+v", body.Columns[3])
	}

	// Reload the board and verify column_overrides carried the gate.
	got := doReqDecode(t, svc, mux, http.MethodGet,
		"/api/v1/boards/"+boardID, nil, cookies)
	br, _ := got["board"].(map[string]any)
	cfg, _ := br["config"].(map[string]any)
	overrides, _ := cfg["column_overrides"].(map[string]any)
	newID := body.Columns[3].ID
	ov, _ := overrides[newID].(map[string]any)
	if ov["gate"] != "human" {
		t.Fatalf("new column's gate did not persist in column_overrides; got %+v", overrides)
	}
}

// TestWorkflowSave_DeletesEmptyColumn — DELETE for a column with no
// cards succeeds and the row disappears.
func TestWorkflowSave_DeletesEmptyColumn(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "wf-b", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default", "agents": []map[string]any{}}, cookies)
	boardID, _ := board["id"].(string)

	cols, _ := svc.Store.BoardColumns().ListByBoard(context.Background(), boardID)
	doomed := cols[len(cols)-1].ID

	// Keep all other columns; ask the server to delete the last one.
	keep := make([]map[string]any, 0, len(cols)-1)
	for i, c := range cols {
		if c.ID == doomed {
			continue
		}
		keep = append(keep, map[string]any{
			"id": c.ID, "name": c.Name, "position": i, "color": c.Color,
		})
	}
	res := doReq(t, svc, mux, http.MethodPut,
		"/api/v1/boards/"+boardID+"/workflow",
		map[string]any{"columns": keep, "deleted": []string{doomed}}, cookies)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT workflow: status=%d body=%s", res.StatusCode, readBody(res))
	}

	after, _ := svc.Store.BoardColumns().ListByBoard(context.Background(), boardID)
	if len(after) != len(cols)-1 {
		t.Fatalf("expected %d cols after delete; got %d", len(cols)-1, len(after))
	}
	for _, c := range after {
		if c.ID == doomed {
			t.Fatalf("deleted column still present")
		}
	}
}

// TestWorkflowSave_BlocksDeleteWithCards — DELETE for a column that
// holds cards returns 409 and the response body names the blocked
// column. Nothing else is applied.
func TestWorkflowSave_BlocksDeleteWithCards(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	mountKanban(svc, mux)
	cookies := loginAs(t, svc, mux, "wf-c", "owner-password-long", "role-owner")

	board := doReqDecode(t, svc, mux, http.MethodPost, "/api/v1/boards",
		map[string]any{"name": "T", "preset": "default", "agents": []map[string]any{}}, cookies)
	boardID, _ := board["id"].(string)

	ctx := context.Background()
	cols, _ := svc.Store.BoardColumns().ListByBoard(ctx, boardID)
	stuck := cols[0]
	if err := svc.Store.BoardCards().Create(ctx, &datastore.BoardCard{
		BoardID: boardID, ColumnID: stuck.ID, Title: "trapped", Status: "queued",
	}); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	// Try to delete the column even though it has a card.
	keep := make([]map[string]any, 0)
	for i, c := range cols {
		if c.ID == stuck.ID {
			continue
		}
		keep = append(keep, map[string]any{
			"id": c.ID, "name": c.Name, "position": i, "color": c.Color,
		})
	}
	res := doReq(t, svc, mux, http.MethodPut,
		"/api/v1/boards/"+boardID+"/workflow",
		map[string]any{"columns": keep, "deleted": []string{stuck.ID}}, cookies)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409; got %d body=%s", res.StatusCode, readBody(res))
	}
	body := readBody(res)
	if !strings.Contains(body, stuck.ID) {
		t.Fatalf("error body should name the blocked column id; got %s", body)
	}

	// The column must still be present — the transaction was abandoned.
	after, _ := svc.Store.BoardColumns().ListByBoard(ctx, boardID)
	if len(after) != len(cols) {
		t.Fatalf("expected column count unchanged; got %d (was %d)", len(after), len(cols))
	}
}
