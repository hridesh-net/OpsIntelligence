// Package kanban implements an MCP (Model Context Protocol) server that exposes
// the kanban board as queryable tools for AI agents.
package kanban

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// MCPServer exposes kanban data over MCP.
type MCPServer struct {
	store datastore.Store
}

// NewMCPServer creates an MCP server.
func NewMCPServer(store datastore.Store) *MCPServer {
	return &MCPServer{store: store}
}

// ToolDef is a simplified MCP tool definition.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ToolResult is the result of an MCP tool call.
type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Content is a content item in a tool result.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ListTools returns all available kanban MCP tools.
func (m *MCPServer) ListTools() []ToolDef {
	return []ToolDef{
		{
			Name:        "kanban_list_boards",
			Description: "List all kanban boards",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "kanban_get_board",
			Description: "Get a board with columns and cards",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":{"type":"string"}},"required":["board_id"]}`),
		},
		{
			Name:        "kanban_list_cards",
			Description: "List cards on a board",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":{"type":"string"},"column_id":{"type":"string"}},"required":["board_id"]}`),
		},
		{
			Name:        "kanban_get_card",
			Description: "Get card details with runs",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"card_id":{"type":"string"}},"required":["card_id"]}`),
		},
		{
			Name:        "kanban_dispatch_card",
			Description: "Dispatch an agent to work on a card",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"board_id":{"type":"string"},"card_id":{"type":"string"},"agent_id":{"type":"string"},"persona_id":{"type":"string"}},"required":["board_id","card_id"]}`),
		},
	}
}

// CallTool executes a kanban MCP tool.
func (m *MCPServer) CallTool(ctx context.Context, name string, args json.RawMessage) ToolResult {
	switch name {
	case "kanban_list_boards":
		return m.listBoards(ctx)
	case "kanban_get_board":
		return m.getBoard(ctx, args)
	case "kanban_list_cards":
		return m.listCards(ctx, args)
	case "kanban_get_card":
		return m.getCard(ctx, args)
	case "kanban_dispatch_card":
		return m.dispatchCard(ctx, args)
	default:
		return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: "unknown tool: " + name}}}
	}
}

func (m *MCPServer) listBoards(ctx context.Context) ToolResult {
	boards, err := m.store.Boards().List(ctx, datastore.BoardFilter{Limit: 100})
	if err != nil {
		return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: err.Error()}}}
	}
	b, _ := json.MarshalIndent(boards, "", "  ")
	return ToolResult{Content: []Content{{Type: "text", Text: string(b)}}}
}

func (m *MCPServer) getBoard(ctx context.Context, args json.RawMessage) ToolResult {
	var p struct{ BoardID string `json:"board_id"` }
	if err := json.Unmarshal(args, &p); err != nil {
		return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: err.Error()}}}
	}
	board, err := m.store.Boards().Get(ctx, p.BoardID)
	if err != nil {
		return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: err.Error()}}}
	}
	cols, _ := m.store.BoardColumns().ListByBoard(ctx, p.BoardID)
	cards, _ := m.store.BoardCards().List(ctx, datastore.BoardCardFilter{BoardID: p.BoardID, Limit: 1000})
	out := map[string]any{
		"board": board,
		"columns": cols,
		"cards":   cards,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return ToolResult{Content: []Content{{Type: "text", Text: string(b)}}}
}

func (m *MCPServer) listCards(ctx context.Context, args json.RawMessage) ToolResult {
	var p struct {
		BoardID  string `json:"board_id"`
		ColumnID string `json:"column_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: err.Error()}}}
	}
	f := datastore.BoardCardFilter{BoardID: p.BoardID, Limit: 1000}
	if p.ColumnID != "" {
		f.ColumnID = p.ColumnID
	}
	cards, err := m.store.BoardCards().List(ctx, f)
	if err != nil {
		return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: err.Error()}}}
	}
	b, _ := json.MarshalIndent(cards, "", "  ")
	return ToolResult{Content: []Content{{Type: "text", Text: string(b)}}}
}

func (m *MCPServer) getCard(ctx context.Context, args json.RawMessage) ToolResult {
	var p struct{ CardID string `json:"card_id"` }
	if err := json.Unmarshal(args, &p); err != nil {
		return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: err.Error()}}}
	}
	card, err := m.store.BoardCards().Get(ctx, p.CardID)
	if err != nil {
		return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: err.Error()}}}
	}
	runs, _ := m.store.CardRuns().List(ctx, datastore.CardRunFilter{CardID: p.CardID, Limit: 50})
	out := map[string]any{
		"card": card,
		"runs": runs,
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return ToolResult{Content: []Content{{Type: "text", Text: string(b)}}}
}

func (m *MCPServer) dispatchCard(ctx context.Context, args json.RawMessage) ToolResult {
	var p struct {
		BoardID   string `json:"board_id"`
		CardID    string `json:"card_id"`
		AgentID   string `json:"agent_id"`
		PersonaID string `json:"persona_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return ToolResult{IsError: true, Content: []Content{{Type: "text", Text: err.Error()}}}
	}
	return ToolResult{Content: []Content{{Type: "text", Text: fmt.Sprintf(
		"Dispatch requested for card %s on board %s. Use the dashboard or API to track progress.", p.CardID, p.BoardID)}}}
}
