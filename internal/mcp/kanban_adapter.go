package mcp

// kanban_adapter.go — registers Kanban tools on the MCP server so any
// MCP client (Claude Desktop, Cursor, Codex, …) can drive the board.
//
// Matches the kanbots-mcp-server surface from leodavinci1/kanbots:
//
//	kanban_list_boards()                          → list every board
//	kanban_get_board(board_id)                    → board + columns + cards
//	kanban_list_cards(board_id, column_id?, status?)
//	kanban_create_card(board_id, title, description?, card_type?, priority?)
//	kanban_move_card(card_id, column_id)
//	kanban_dispatch(card_id, agent_id?, persona_id?, model?, slash?, slash_args?)
//	kanban_get_run(run_id)                        → run + events + decisions
//	kanban_stop_run(run_id)
//	kanban_answer_decision(decision_id, answer)
//
// Tools are pure JSON-in / JSON-out — they reuse the same DispatchService
// the gateway HTTP API uses, so behavior is identical between MCP and
// REST callers.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/kanban"
)

// KanbanService is the subset of kanban.DispatchService the adapter uses.
// Kept as a small interface so the MCP package doesn't need to import the
// concrete service in tests.
type KanbanService interface {
	Dispatch(ctx context.Context, cardID string, opts kanban.DispatchOpts) (*datastore.CardRun, error)
	StopRun(ctx context.Context, runID string) error
	AnswerDecision(ctx context.Context, decisionID, answer string) error
}

// RegisterKanbanTools wires the kanban tool set onto an MCP server. The
// store provides the read-side (List/Get); the svc provides the write-side
// (Dispatch/Stop/AnswerDecision). Pass nil for svc to expose read-only
// tools — useful for browsing boards from an LLM without granting
// dispatch authority.
func RegisterKanbanTools(s *Server, store datastore.Store, svc KanbanService) {
	if s == nil || store == nil {
		return
	}

	// Read-only tools — always available.
	s.RegisterTool("kanban_list_boards", func(ctx context.Context, _ map[string]any) (CallToolResult, error) {
		boards, err := store.Boards().List(ctx, datastore.BoardFilter{Limit: 100})
		if err != nil {
			return errResult(err), nil
		}
		return jsonResult(boards), nil
	})

	s.RegisterTool("kanban_get_board", func(ctx context.Context, args map[string]any) (CallToolResult, error) {
		id := stringArg(args, "board_id")
		if id == "" {
			return errResult(fmt.Errorf("board_id is required")), nil
		}
		board, err := store.Boards().Get(ctx, id)
		if err != nil {
			return errResult(err), nil
		}
		cols, _ := store.BoardColumns().ListByBoard(ctx, id)
		cards, _ := store.BoardCards().List(ctx, datastore.BoardCardFilter{BoardID: id, Limit: 500})
		return jsonResult(map[string]any{
			"board":   board,
			"columns": cols,
			"cards":   cards,
		}), nil
	})

	s.RegisterTool("kanban_list_cards", func(ctx context.Context, args map[string]any) (CallToolResult, error) {
		boardID := stringArg(args, "board_id")
		if boardID == "" {
			return errResult(fmt.Errorf("board_id is required")), nil
		}
		filter := datastore.BoardCardFilter{
			BoardID:  boardID,
			ColumnID: stringArg(args, "column_id"),
			Status:   stringArg(args, "status"),
			Limit:    500,
		}
		cards, err := store.BoardCards().List(ctx, filter)
		if err != nil {
			return errResult(err), nil
		}
		return jsonResult(cards), nil
	})

	s.RegisterTool("kanban_get_run", func(ctx context.Context, args map[string]any) (CallToolResult, error) {
		id := stringArg(args, "run_id")
		if id == "" {
			return errResult(fmt.Errorf("run_id is required")), nil
		}
		run, err := store.CardRuns().Get(ctx, id)
		if err != nil {
			return errResult(err), nil
		}
		events, _ := store.CardRunEvents().List(ctx, datastore.CardRunEventFilter{RunID: id, Limit: 500})
		decisions, _ := store.PendingDecisions().ListByRun(ctx, id)
		return jsonResult(map[string]any{
			"run":       run,
			"events":    events,
			"decisions": decisions,
		}), nil
	})

	// Write-side tools — only registered when the service is wired.
	if svc == nil {
		return
	}

	s.RegisterTool("kanban_create_card", func(ctx context.Context, args map[string]any) (CallToolResult, error) {
		boardID := stringArg(args, "board_id")
		title := stringArg(args, "title")
		if boardID == "" || title == "" {
			return errResult(fmt.Errorf("board_id and title are required")), nil
		}
		// Default to the board's first column.
		cols, err := store.BoardColumns().ListByBoard(ctx, boardID)
		if err != nil || len(cols) == 0 {
			return errResult(fmt.Errorf("board has no columns")), nil
		}
		card := &datastore.BoardCard{
			BoardID:     boardID,
			ColumnID:    cols[0].ID,
			Title:       title,
			Description: stringArg(args, "description"),
			CardType:    stringOr(args, "card_type", "feature"),
			Priority:    stringOr(args, "priority", "p2"),
			Effort:      stringArg(args, "effort"),
		}
		if err := store.BoardCards().Create(ctx, card); err != nil {
			return errResult(err), nil
		}
		return jsonResult(card), nil
	})

	s.RegisterTool("kanban_move_card", func(ctx context.Context, args map[string]any) (CallToolResult, error) {
		cardID := stringArg(args, "card_id")
		columnID := stringArg(args, "column_id")
		if cardID == "" || columnID == "" {
			return errResult(fmt.Errorf("card_id and column_id are required")), nil
		}
		if err := store.BoardCards().Move(ctx, cardID, columnID); err != nil {
			return errResult(err), nil
		}
		return jsonResult(map[string]any{"moved": true}), nil
	})

	s.RegisterTool("kanban_dispatch", func(ctx context.Context, args map[string]any) (CallToolResult, error) {
		cardID := stringArg(args, "card_id")
		if cardID == "" {
			return errResult(fmt.Errorf("card_id is required")), nil
		}
		run, err := svc.Dispatch(ctx, cardID, kanban.DispatchOpts{
			AgentID:      stringArg(args, "agent_id"),
			PersonaID:    stringArg(args, "persona_id"),
			Model:        stringArg(args, "model"),
			SlashCommand: stringArg(args, "slash"),
			SlashArgs:    stringArg(args, "slash_args"),
		})
		if err != nil {
			return errResult(err), nil
		}
		return jsonResult(run), nil
	})

	s.RegisterTool("kanban_stop_run", func(ctx context.Context, args map[string]any) (CallToolResult, error) {
		runID := stringArg(args, "run_id")
		if runID == "" {
			return errResult(fmt.Errorf("run_id is required")), nil
		}
		if err := svc.StopRun(ctx, runID); err != nil {
			return errResult(err), nil
		}
		return jsonResult(map[string]any{"stopped": true}), nil
	})

	s.RegisterTool("kanban_answer_decision", func(ctx context.Context, args map[string]any) (CallToolResult, error) {
		id := stringArg(args, "decision_id")
		answer := stringArg(args, "answer")
		if id == "" || answer == "" {
			return errResult(fmt.Errorf("decision_id and answer are required")), nil
		}
		if err := svc.AnswerDecision(ctx, id, answer); err != nil {
			return errResult(err), nil
		}
		return jsonResult(map[string]any{"answered": true}), nil
	})
}

// ── helpers ────────────────────────────────────────────────────────────────

func stringArg(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func stringOr(m map[string]any, k, fallback string) string {
	if s := stringArg(m, k); s != "" {
		return s
	}
	return fallback
}

func jsonResult(v any) CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(err)
	}
	return CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: string(b)}},
	}
}

func errResult(err error) CallToolResult {
	return CallToolResult{
		IsError: true,
		Content: []ContentBlock{{Type: "text", Text: err.Error()}},
	}
}
