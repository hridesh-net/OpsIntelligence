package gateway

// kanban_api.go — REST endpoints for the Kanban board system.
//
// Endpoints:
//   GET    /api/v1/boards                    List boards
//   POST   /api/v1/boards                    Create board
//   GET    /api/v1/boards/{id}               Get board + columns + cards
//   PUT    /api/v1/boards/{id}               Update board
//   DELETE /api/v1/boards/{id}               Delete board
//   GET    /api/v1/boards/{id}/columns       List columns
//   POST   /api/v1/boards/{id}/columns       Create column
//   PUT    /api/v1/boards/{id}/columns/{cid} Update column
//   DELETE /api/v1/boards/{id}/columns/{cid} Delete column
//   GET    /api/v1/boards/{id}/cards         List cards
//   POST   /api/v1/boards/{id}/cards         Create card
//   GET    /api/v1/boards/{id}/cards/{cid}   Get card detail
//   PUT    /api/v1/boards/{id}/cards/{cid}   Update card
//   DELETE /api/v1/boards/{id}/cards/{cid}   Delete card
//   POST   /api/v1/boards/{id}/cards/{cid}/move   Move card to column
//   GET    /api/v1/boards/{id}/cards/{cid}/runs   List card runs
//   POST   /api/v1/boards/{id}/cards/{cid}/dispatch  Dispatch agent on card
//   GET    /api/v1/runs/{rid}                Get run detail + events
//   POST   /api/v1/runs/{rid}/stop           Stop a run
//   POST   /api/v1/runs/{rid}/decisions/{did}  Answer a pending decision
//   GET    /api/v1/boards/{id}/agents        List board agents
//   POST   /api/v1/boards/{id}/agents        Register agent
//   GET    /api/v1/personas                 List personas
//   POST   /api/v1/personas                 Create persona

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/kanban"
	"github.com/opsintelligence/opsintelligence/internal/kanban/preview"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// ── Request / Response types ──────────────────────────────────────────

type createBoardRequest struct {
	Name     string `json:"name"`
	TeamID   string `json:"team_id,omitempty"`
	RepoURL  string `json:"repo_url,omitempty"`
	RepoPath string `json:"repo_path,omitempty"`
	Mode     string `json:"mode,omitempty"`
	// Scrun setup-wizard: atomic seed. When `preset` is set, the seed
	// columns are taken from the named preset (see workflowPresets).
	// When `agents` is non-empty, each entry is registered as a
	// BoardAgent in the same request so the wizard finishes in one shot.
	Preset string                 `json:"preset,omitempty"`
	Config map[string]any         `json:"config,omitempty"`
	Agents []createAgentRequest   `json:"agents,omitempty"`
	Columns []wizardColumn        `json:"columns,omitempty"`
}

type wizardColumn struct {
	Name       string         `json:"name"`
	Position   int            `json:"position"`
	Color      string         `json:"color,omitempty"`
	WIPLimit   *int           `json:"wip_limit,omitempty"`
	Gate       string         `json:"gate,omitempty"`       // none | human | auto-validate
	Automation map[string]any `json:"automation,omitempty"` // auto_assign / keep_agent / auto_validate
}

type createColumnRequest struct {
	Name       string         `json:"name"`
	Position   int            `json:"position"`
	Color      string         `json:"color,omitempty"`
	WIPLimit   *int           `json:"wip_limit,omitempty"`
	Gate       string         `json:"gate,omitempty"`
	Automation map[string]any `json:"automation,omitempty"`
}

type createCardRequest struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	CardType    string         `json:"card_type,omitempty"`
	Priority    string         `json:"priority,omitempty"`
	Effort      string         `json:"effort,omitempty"`
	ColumnID    string         `json:"column_id,omitempty"`
	Assignee    string         `json:"assignee,omitempty"`
	// Scrun extra fields ride on metadata: acceptance_criteria, labels,
	// confidence, eta_minutes, hitl, branch_hint, etc.
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type updateCardRequest struct {
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	CardType    string         `json:"card_type,omitempty"`
	Priority    string         `json:"priority,omitempty"`
	Effort      string         `json:"effort,omitempty"`
	Status      string         `json:"status,omitempty"`
	Assignee    string         `json:"assignee,omitempty"`
	ColumnID    string         `json:"column_id,omitempty"`
	// Metadata merges into the card's existing metadata. Pass null inside
	// a key to remove it.
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type moveCardRequest struct {
	ColumnID string `json:"column_id"`
}

type dispatchRequest struct {
	AgentID      string `json:"agent_id"`
	PersonaID    string `json:"persona_id,omitempty"`
	Model        string `json:"model,omitempty"`
	SlashCommand string `json:"slash_command,omitempty"`
	SlashArgs    string `json:"slash_args,omitempty"`
}

type autopilotRequest struct {
	CardID         string           `json:"card_id"`
	Mode           string           `json:"mode"` // "feature-dev" | "qa"
	PersonaIDs     []string         `json:"persona_ids,omitempty"`
	Parallelism    int              `json:"parallelism,omitempty"`
	BudgetUSD      float64          `json:"budget_usd,omitempty"`
	MaxCycles      int              `json:"max_cycles,omitempty"`
	Checks         []kanban.QACheck `json:"checks,omitempty"`
	FixAgentID     string           `json:"fix_agent_id,omitempty"`
	MaxFixAttempts int              `json:"max_fix_attempts,omitempty"`
}

type createAgentRequest struct {
	Name       string         `json:"name"`
	AgentType  string         `json:"agent_type"`
	ProviderID string         `json:"provider_id,omitempty"`
	Config     map[string]any `json:"config,omitempty"`
	IsDefault  bool           `json:"is_default,omitempty"`
}

// updateAgentRequest is a partial-update body. Top-level pointer fields
// are applied only when present (non-nil). Config is merged key-by-key
// into the agent's existing config map — passing a nil value for a key
// deletes it, matching the convention used by the card metadata path.
type updateAgentRequest struct {
	Name       *string        `json:"name,omitempty"`
	AgentType  *string        `json:"agent_type,omitempty"`
	ProviderID *string        `json:"provider_id,omitempty"`
	IsDefault  *bool          `json:"is_default,omitempty"`
	IsActive   *bool          `json:"is_active,omitempty"`
	Config     map[string]any `json:"config,omitempty"`
}

type createPersonaRequest struct {
	Name         string `json:"name"`
	Icon         string `json:"icon,omitempty"`
	Description  string `json:"description,omitempty"`
	SystemPrompt string `json:"system_prompt"`
}

type answerDecisionRequest struct {
	Answer string `json:"answer"`
}

// ── Route dispatcher ──────────────────────────────────────────────────

// HandleKanban dispatches /api/v1/boards, /api/v1/runs, and /api/v1/personas.
func (s *AuthService) HandleKanban(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	path = strings.TrimPrefix(path, "/")

	switch {
	case path == "boards" || path == "boards/":
		s.handleBoards(w, r)
	case strings.HasPrefix(path, "boards/"):
		// /boards/{id}/github/sync and /boards/{id}/sentry/import are
		// special endpoints that trigger an external pull. Route them
		// here before the generic detail handler so the {sub} dispatch
		// table doesn't shadow them.
		rest := strings.TrimPrefix(path, "boards/")
		if i := strings.Index(rest, "/github/sync"); i > 0 {
			boardID := rest[:i]
			s.handleGitHubSync(w, r, boardID)
			return
		}
		if i := strings.Index(rest, "/sentry/import"); i > 0 {
			boardID := rest[:i]
			s.handleSentryImport(w, r, boardID)
			return
		}
		s.handleBoardDetail(w, r, rest)
	case strings.HasPrefix(path, "runs/"):
		s.handleRunDetail(w, r, strings.TrimPrefix(path, "runs/"))
	case strings.HasPrefix(path, "attachments/"):
		s.handleAttachmentDetail(w, r, strings.TrimPrefix(path, "attachments/"))
	case path == "personas" || path == "personas/":
		s.handlePersonas(w, r)
	case path == "workflow-presets" || path == "workflow-presets/":
		s.handleWorkflowPresets(w, r)
	case path == "autopilot" || path == "autopilot/":
		s.handleAutopilotList(w, r)
	case strings.HasPrefix(path, "autopilot/"):
		s.handleAutopilotDetail(w, r, strings.TrimPrefix(path, "autopilot/"))
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

func (s *AuthService) handleGitHubSync(w http.ResponseWriter, r *http.Request, boardID string) {
	p := auth.PrincipalFrom(r.Context())
	if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.KanbanGitHub == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "github mode not configured")
		return
	}
	added, updated, err := s.KanbanGitHub.PullIssues(r.Context(), boardID)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"added":   added,
		"updated": updated,
	})
}

func (s *AuthService) handleSentryImport(w http.ResponseWriter, r *http.Request, boardID string) {
	p := auth.PrincipalFrom(r.Context())
	if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.KanbanSentry == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "sentry importer not configured")
		return
	}
	var req struct {
		Org     string `json:"org"`
		Project string `json:"project"`
		Query   string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Org == "" || req.Project == "" {
		writeJSONError(w, http.StatusBadRequest, "org and project are required")
		return
	}
	added, updated, err := s.KanbanSentry.Import(r.Context(), boardID, req.Org, req.Project, req.Query)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"added":   added,
		"updated": updated,
	})
}

// ── Boards ────────────────────────────────────────────────────────────

func (s *AuthService) handleBoards(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		boards, err := s.Store.Boards().List(r.Context(), datastore.BoardFilter{Limit: 100})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"boards": boards})
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createBoardRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "name required")
			return
		}
		mode := req.Mode
		if mode == "" {
			mode = "local"
		}
		board := &datastore.Board{
			Name:     req.Name,
			TeamID:   req.TeamID,
			RepoURL:  req.RepoURL,
			RepoPath: req.RepoPath,
			Mode:     mode,
			Config:   req.Config,
		}
		// Store per-column gate/automation overrides on board.Config so
		// the Scrun UI can render them without a schema migration. Keyed
		// by column position; resolved to column_id after columns insert.
		colSpecs := req.Columns
		if len(colSpecs) == 0 {
			colSpecs = presetColumns(req.Preset)
		}
		if board.Config == nil && hasColumnOverrides(colSpecs) {
			board.Config = map[string]any{}
		}
		if err := s.Store.Boards().Create(r.Context(), board); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		overrides := map[string]any{}
		for _, d := range colSpecs {
			col := &datastore.BoardColumn{
				BoardID:  board.ID,
				Name:     d.Name,
				Position: d.Position,
				Color:    d.Color,
				WIPLimit: d.WIPLimit,
			}
			if err := s.Store.BoardColumns().Create(r.Context(), col); err != nil {
				continue
			}
			if d.Gate != "" || len(d.Automation) > 0 {
				overrides[col.ID] = map[string]any{
					"gate":       d.Gate,
					"automation": d.Automation,
				}
			}
		}
		if len(overrides) > 0 {
			if board.Config == nil {
				board.Config = map[string]any{}
			}
			board.Config["column_overrides"] = overrides
			_ = s.Store.Boards().Update(r.Context(), board)
		}
		// Optional: register agents in one shot.
		for _, a := range req.Agents {
			if a.Name == "" || a.AgentType == "" {
				continue
			}
			agent := &datastore.BoardAgent{
				BoardID:    board.ID,
				Name:       a.Name,
				AgentType:  a.AgentType,
				ProviderID: a.ProviderID,
				Config:     a.Config,
				IsDefault:  a.IsDefault,
				IsActive:   true,
			}
			_ = s.Store.BoardAgents().Create(r.Context(), agent)
		}
		writeJSON(w, http.StatusCreated, board)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *AuthService) handleBoardDetail(w http.ResponseWriter, r *http.Request, suffix string) {
	parts := strings.SplitN(suffix, "/", 3)
	boardID := parts[0]
	if boardID == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	// No sub-resource: /api/v1/boards/{id}
	if len(parts) == 1 || parts[1] == "" {
		s.handleSingleBoard(w, r, boardID)
		return
	}

	sub := parts[1]
	switch sub {
	case "columns":
		if len(parts) == 2 || parts[2] == "" {
			s.handleBoardColumns(w, r, boardID)
		} else {
			s.handleSingleColumn(w, r, boardID, parts[2])
		}
	case "cards":
		if len(parts) == 2 || parts[2] == "" {
			s.handleBoardCards(w, r, boardID)
		} else {
			s.handleCardDetail(w, r, boardID, parts[2])
		}
	case "agents":
		if len(parts) == 2 || parts[2] == "" {
			s.handleBoardAgents(w, r, boardID)
		} else {
			s.handleSingleBoardAgent(w, r, boardID, parts[2])
		}
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

func (s *AuthService) handleSingleBoard(w http.ResponseWriter, r *http.Request, boardID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		board, err := s.Store.Boards().Get(r.Context(), boardID)
		if err != nil {
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "board not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		columns, _ := s.Store.BoardColumns().ListByBoard(r.Context(), boardID)
		cards, _ := s.Store.BoardCards().List(r.Context(), datastore.BoardCardFilter{BoardID: boardID, Limit: 1000})
		writeJSON(w, http.StatusOK, map[string]any{
			"board":   board,
			"columns": columns,
			"cards":   cards,
		})
	case http.MethodPut:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createBoardRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		board, err := s.Store.Boards().Get(r.Context(), boardID)
		if err != nil {
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "board not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if req.Name != "" {
			board.Name = req.Name
		}
		if req.RepoURL != "" {
			board.RepoURL = req.RepoURL
		}
		if req.RepoPath != "" {
			board.RepoPath = req.RepoPath
		}
		if req.Mode != "" {
			board.Mode = req.Mode
		}
		if err := s.Store.Boards().Update(r.Context(), board); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, board)
	case http.MethodDelete:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if err := s.Store.Boards().Delete(r.Context(), boardID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusNoContent, nil)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Columns ───────────────────────────────────────────────────────────

func (s *AuthService) handleBoardColumns(w http.ResponseWriter, r *http.Request, boardID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		cols, err := s.Store.BoardColumns().ListByBoard(r.Context(), boardID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"columns": cols})
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createColumnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" {
			writeJSONError(w, http.StatusBadRequest, "name required")
			return
		}
		col := &datastore.BoardColumn{
			BoardID:  boardID,
			Name:     req.Name,
			Position: req.Position,
			Color:    req.Color,
			WIPLimit: req.WIPLimit,
		}
		if err := s.Store.BoardColumns().Create(r.Context(), col); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if req.Gate != "" || len(req.Automation) > 0 {
			s.setColumnOverride(r.Context(), boardID, col.ID, req.Gate, req.Automation)
		}
		writeJSON(w, http.StatusCreated, col)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// setColumnOverride writes a per-column gate/automation override onto
// the board's Config map so the Scrun UI can read it back. Stored under
// board.config.column_overrides.<column_id> = {gate, automation}.
func (s *AuthService) setColumnOverride(ctx context.Context, boardID, columnID, gate string, automation map[string]any) {
	board, err := s.Store.Boards().Get(ctx, boardID)
	if err != nil || board == nil {
		return
	}
	if board.Config == nil {
		board.Config = map[string]any{}
	}
	ovRaw, _ := board.Config["column_overrides"].(map[string]any)
	if ovRaw == nil {
		ovRaw = map[string]any{}
	}
	ovRaw[columnID] = map[string]any{"gate": gate, "automation": automation}
	board.Config["column_overrides"] = ovRaw
	_ = s.Store.Boards().Update(ctx, board)
}

func (s *AuthService) handleSingleColumn(w http.ResponseWriter, r *http.Request, boardID, columnID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodPut:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createColumnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		col, err := s.Store.BoardColumns().Get(r.Context(), columnID)
		if err != nil {
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "column not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if col.BoardID != boardID {
			writeJSONError(w, http.StatusNotFound, "column not found")
			return
		}
		if req.Name != "" {
			col.Name = req.Name
		}
		col.Position = req.Position
		col.Color = req.Color
		col.WIPLimit = req.WIPLimit
		if req.Gate != "" || len(req.Automation) > 0 {
			s.setColumnOverride(r.Context(), boardID, col.ID, req.Gate, req.Automation)
		}
		if err := s.Store.BoardColumns().Update(r.Context(), col); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, col)
	case http.MethodDelete:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if err := s.Store.BoardColumns().Delete(r.Context(), columnID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusNoContent, nil)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Cards ─────────────────────────────────────────────────────────────

func (s *AuthService) handleBoardCards(w http.ResponseWriter, r *http.Request, boardID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		q := r.URL.Query()
		filter := datastore.BoardCardFilter{
			BoardID:  boardID,
			ColumnID: q.Get("column_id"),
			Status:   q.Get("status"),
			Limit:    parseLimit(q.Get("limit"), 100),
			Offset:   parseInt(q.Get("offset")),
		}
		cards, err := s.Store.BoardCards().List(r.Context(), filter)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"cards": cards})
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermCardsCreate); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createCardRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Title == "" {
			writeJSONError(w, http.StatusBadRequest, "title required")
			return
		}
		// Place new cards in the first column (Inbox, position 0) unless
		// the caller explicitly picked one (Scrun's task form does).
		cols, err := s.Store.BoardColumns().ListByBoard(r.Context(), boardID)
		if err != nil || len(cols) == 0 {
			writeJSONError(w, http.StatusInternalServerError, "no columns found")
			return
		}
		columnID := req.ColumnID
		if columnID == "" {
			columnID = cols[0].ID
		}
		cardType := req.CardType
		if cardType == "" {
			cardType = "feature"
		}
		priority := req.Priority
		if priority == "" {
			priority = "p2"
		}
		assigneeType := ""
		if req.Assignee != "" {
			assigneeType = "agent"
		}
		card := &datastore.BoardCard{
			BoardID:      boardID,
			ColumnID:     columnID,
			Title:        req.Title,
			Description:  req.Description,
			CardType:     cardType,
			Priority:     priority,
			Effort:       req.Effort,
			Assignee:     req.Assignee,
			AssigneeType: assigneeType,
			Metadata:     req.Metadata,
		}
		if err := s.Store.BoardCards().Create(r.Context(), card); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Mirror to GitHub for github-mode boards (best-effort; surface
		// the error in the response so the operator notices).
		if s.KanbanGitHub != nil {
			if err := s.KanbanGitHub.PushCardCreated(r.Context(), card); err != nil {
				// We've already persisted locally; report the partial
				// success so the operator can decide whether to retry.
				writeJSON(w, http.StatusCreated, map[string]any{
					"card":         card,
					"github_error": err.Error(),
				})
				return
			}
		}
		writeJSON(w, http.StatusCreated, card)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *AuthService) handleCardDetail(w http.ResponseWriter, r *http.Request, boardID, cardSuffix string) {
	parts := strings.SplitN(cardSuffix, "/", 2)
	cardID := parts[0]
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}

	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if sub == "" || sub == "/" {
			card, err := s.Store.BoardCards().Get(r.Context(), cardID)
			if err != nil {
				if isNotFound(err) {
					writeJSONError(w, http.StatusNotFound, "card not found")
					return
				}
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			runs, _ := s.Store.CardRuns().List(r.Context(), datastore.CardRunFilter{CardID: cardID, Limit: 50})
			writeJSON(w, http.StatusOK, map[string]any{"card": card, "runs": runs})
			return
		}
		if sub == "runs" || sub == "runs/" {
			runs, err := s.Store.CardRuns().List(r.Context(), datastore.CardRunFilter{CardID: cardID, Limit: 50})
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
			return
		}
		writeJSONError(w, http.StatusNotFound, "not found")
	case http.MethodPut:
		if err := rbac.Enforce(r.Context(), p, rbac.PermCardsEdit); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req updateCardRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		card, err := s.Store.BoardCards().Get(r.Context(), cardID)
		if err != nil {
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "card not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if req.Title != "" {
			card.Title = req.Title
		}
		if req.Description != "" {
			card.Description = req.Description
		}
		if req.CardType != "" {
			card.CardType = req.CardType
		}
		if req.Priority != "" {
			card.Priority = req.Priority
		}
		if req.Effort != "" {
			card.Effort = req.Effort
		}
		if req.Status != "" {
			card.Status = req.Status
		}
		if req.Assignee != "" {
			card.Assignee = req.Assignee
			if card.AssigneeType == "" {
				card.AssigneeType = "agent"
			}
		}
		if req.ColumnID != "" {
			card.ColumnID = req.ColumnID
		}
		if len(req.Metadata) > 0 {
			if card.Metadata == nil {
				card.Metadata = map[string]any{}
			}
			for k, v := range req.Metadata {
				if v == nil {
					delete(card.Metadata, k)
				} else {
					card.Metadata[k] = v
				}
			}
		}
		if err := s.Store.BoardCards().Update(r.Context(), card); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, card)
	case http.MethodDelete:
		if err := rbac.Enforce(r.Context(), p, rbac.PermCardsDelete); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if err := s.Store.BoardCards().Delete(r.Context(), cardID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusNoContent, nil)
	case http.MethodPost:
		if sub == "move" || sub == "move/" {
			if err := rbac.Enforce(r.Context(), p, rbac.PermCardsEdit); err != nil {
				writeJSONError(w, http.StatusForbidden, "permission denied")
				return
			}
			var req moveCardRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			if req.ColumnID == "" {
				writeJSONError(w, http.StatusBadRequest, "column_id required")
				return
			}
			// Enforce WIP limit
			col, err := s.Store.BoardColumns().Get(r.Context(), req.ColumnID)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid column")
				return
			}
			if col.WIPLimit != nil && *col.WIPLimit > 0 {
				cardsInCol, err := s.Store.BoardCards().List(r.Context(), datastore.BoardCardFilter{ColumnID: req.ColumnID})
				if err == nil && len(cardsInCol) >= *col.WIPLimit {
					writeJSONError(w, http.StatusConflict, fmt.Sprintf("WIP limit (%d) reached for column %q", *col.WIPLimit, col.Name))
					return
				}
			}
			if err := s.Store.BoardCards().Move(r.Context(), cardID, req.ColumnID); err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			// Mirror the column change to the GH issue's labels.
			// Best-effort: log the error to the response but don't fail
			// the local move that already succeeded.
			if s.KanbanGitHub != nil {
				if err := s.KanbanGitHub.PushCardMoved(r.Context(), cardID, req.ColumnID); err != nil {
					writeJSON(w, http.StatusOK, map[string]any{
						"moved":        true,
						"github_error": err.Error(),
					})
					return
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"moved": true})
			return
		}
		if sub == "dispatch" || sub == "dispatch/" {
			if err := rbac.Enforce(r.Context(), p, rbac.PermRunsDispatch); err != nil {
				writeJSONError(w, http.StatusForbidden, "permission denied")
				return
			}
			var req dispatchRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			if s.Kanban == nil {
				writeJSONError(w, http.StatusServiceUnavailable, "kanban dispatch service not configured")
				return
			}
			run, err := s.Kanban.Dispatch(r.Context(), cardID, kanban.DispatchOpts{
				AgentID:      req.AgentID,
				PersonaID:    req.PersonaID,
				Model:        req.Model,
				SlashCommand: req.SlashCommand,
				SlashArgs:    req.SlashArgs,
			})
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusAccepted, run)
			return
		}
		if sub == "attachments" || sub == "attachments/" {
			s.handleCardAttachments(w, r, cardID)
			return
		}
		if sub == "preview" || sub == "preview/" {
			s.handleCardPreview(w, r, cardID)
			return
		}
		writeJSONError(w, http.StatusNotFound, "not found")
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Runs ──────────────────────────────────────────────────────────────

func (s *AuthService) handleRunDetail(w http.ResponseWriter, r *http.Request, suffix string) {
	parts := strings.SplitN(suffix, "/", 3)
	if len(parts) < 1 || parts[0] == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	runID := parts[0]
	p := auth.PrincipalFrom(r.Context())

	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermRunsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		run, err := s.Store.CardRuns().Get(r.Context(), runID)
		if err != nil {
			if isNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "run not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		events, _ := s.Store.CardRunEvents().List(r.Context(), datastore.CardRunEventFilter{RunID: runID, Limit: 500})
		decisions, _ := s.Store.PendingDecisions().ListByRun(r.Context(), runID)
		writeJSON(w, http.StatusOK, map[string]any{
			"run":       run,
			"events":    events,
			"decisions": decisions,
		})
	case http.MethodPost:
		if len(parts) < 2 {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		sub := parts[1]
		if sub == "stop" || sub == "stop/" {
			if err := rbac.Enforce(r.Context(), p, rbac.PermRunsCancel); err != nil {
				writeJSONError(w, http.StatusForbidden, "permission denied")
				return
			}
			if s.Kanban != nil {
				if err := s.Kanban.StopRun(r.Context(), runID); err != nil {
					if isNotFound(err) {
						writeJSONError(w, http.StatusNotFound, "run not found")
						return
					}
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
			} else {
				// Fallback: just mark as stopped in DB.
				run, err := s.Store.CardRuns().Get(r.Context(), runID)
				if err != nil {
					if isNotFound(err) {
						writeJSONError(w, http.StatusNotFound, "run not found")
						return
					}
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
				run.Status = "stopped"
				run.CompletedAt = &[]time.Time{time.Now().UTC()}[0]
				if err := s.Store.CardRuns().Update(r.Context(), run); err != nil {
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"stopped": true})
			return
		}
		if sub == "decisions" && len(parts) >= 3 {
			decisionID := parts[2]
			if err := rbac.Enforce(r.Context(), p, rbac.PermRunsDispatch); err != nil {
				writeJSONError(w, http.StatusForbidden, "permission denied")
				return
			}
			var req answerDecisionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "invalid JSON")
				return
			}
			if s.Kanban != nil {
				if err := s.Kanban.AnswerDecision(r.Context(), decisionID, req.Answer); err != nil {
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
			} else {
				if err := s.Store.PendingDecisions().Answer(r.Context(), decisionID, req.Answer); err != nil {
					writeJSONError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"answered": true})
			return
		}
		writeJSONError(w, http.StatusNotFound, "not found")
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Board Agents ──────────────────────────────────────────────────────

func (s *AuthService) handleBoardAgents(w http.ResponseWriter, r *http.Request, boardID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		agents, err := s.Store.BoardAgents().ListByBoard(r.Context(), boardID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" || req.AgentType == "" {
			writeJSONError(w, http.StatusBadRequest, "name and agent_type required")
			return
		}
		agent := &datastore.BoardAgent{
			BoardID:    boardID,
			Name:       req.Name,
			AgentType:  req.AgentType,
			ProviderID: req.ProviderID,
			Config:     req.Config,
			IsDefault:  req.IsDefault,
			IsActive:   true,
		}
		if err := s.Store.BoardAgents().Create(r.Context(), agent); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, agent)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleSingleBoardAgent serves GET / PUT / DELETE on
// /api/v1/boards/{id}/agents/{aid}. PUT is a partial update: top-level
// pointer fields are honored when present and Config is merged key-by-key
// into the existing config map (nil-value deletes the key). DELETE is
// blocked when the agent still has non-terminal card_runs so we don't
// orphan an in-flight dispatch.
func (s *AuthService) handleSingleBoardAgent(w http.ResponseWriter, r *http.Request, boardID, agentID string) {
	p := auth.PrincipalFrom(r.Context())

	agent, err := s.Store.BoardAgents().Get(r.Context(), agentID)
	if err != nil {
		if isNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "agent not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if agent.BoardID != boardID {
		writeJSONError(w, http.StatusNotFound, "agent not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		writeJSON(w, http.StatusOK, agent)

	case http.MethodPut:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req updateAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name != nil {
			if *req.Name == "" {
				writeJSONError(w, http.StatusBadRequest, "name cannot be empty")
				return
			}
			agent.Name = *req.Name
		}
		if req.AgentType != nil {
			if *req.AgentType == "" {
				writeJSONError(w, http.StatusBadRequest, "agent_type cannot be empty")
				return
			}
			agent.AgentType = *req.AgentType
		}
		if req.ProviderID != nil {
			agent.ProviderID = *req.ProviderID
		}
		if req.IsDefault != nil {
			agent.IsDefault = *req.IsDefault
		}
		if req.IsActive != nil {
			agent.IsActive = *req.IsActive
		}
		if req.Config != nil {
			if agent.Config == nil {
				agent.Config = map[string]any{}
			}
			for k, v := range req.Config {
				if v == nil {
					delete(agent.Config, k)
				} else {
					agent.Config[k] = v
				}
			}
		}
		if err := s.Store.BoardAgents().Update(r.Context(), agent); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, agent)

	case http.MethodDelete:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		// Refuse to delete an agent that's mid-run. Terminal statuses
		// (completed / error / stopped) don't count.
		for _, st := range []string{"queued", "running", "awaiting", "paused"} {
			runs, err := s.Store.CardRuns().List(r.Context(), datastore.CardRunFilter{
				AgentID: agentID, Status: st, Limit: 1,
			})
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if len(runs) > 0 {
				writeJSONError(w, http.StatusConflict, "agent has active runs; stop them first")
				return
			}
		}
		if err := s.Store.BoardAgents().Delete(r.Context(), agentID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Personas ──────────────────────────────────────────────────────────

func (s *AuthService) handlePersonas(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermPersonasRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		personas, err := s.Store.Personas().List(r.Context(), datastore.PersonaFilter{Limit: 200})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"personas": personas})
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermPersonasManage); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req createPersonaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.Name == "" || req.SystemPrompt == "" {
			writeJSONError(w, http.StatusBadRequest, "name and system_prompt required")
			return
		}
		persona := &datastore.Persona{
			ID:           uuid.NewString(),
			Name:         req.Name,
			Icon:         req.Icon,
			Description:  req.Description,
			SystemPrompt: req.SystemPrompt,
			CreatedBy:    p.UserID,
		}
		if err := s.Store.Personas().Create(r.Context(), persona); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, persona)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Helpers ───────────────────────────────────────────────────────────

func isNotFound(err error) bool {
	return err != nil && err.Error() == datastore.ErrNotFound.Error()
}

func parseLimit(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return def
	}
	return v
}

func parseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

// ── Autopilot ─────────────────────────────────────────────────────────────
//
//	GET  /api/v1/autopilot             list all sessions
//	POST /api/v1/autopilot             start a session (mode=feature-dev|qa)
//	GET  /api/v1/autopilot/{id}        session detail
//	POST /api/v1/autopilot/{id}/stop   stop a running session
//
// Permissions: PermRunsDispatch for start/stop, PermRunsRead for list/detail.

func (s *AuthService) handleAutopilotList(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermRunsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if s.KanbanAutopilot == nil {
			writeJSON(w, http.StatusOK, []kanban.AutopilotSession{})
			return
		}
		writeJSON(w, http.StatusOK, s.KanbanAutopilot.List())
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermRunsDispatch); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if s.KanbanAutopilot == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "autopilot not configured")
			return
		}
		var req autopilotRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		if req.CardID == "" {
			writeJSONError(w, http.StatusBadRequest, "card_id is required")
			return
		}
		var sess *kanban.AutopilotSession
		var err error
		switch req.Mode {
		case "feature-dev", "":
			sess, err = s.KanbanAutopilot.StartFeatureDev(r.Context(), req.CardID, kanban.FeatureDevOpts{
				PersonaIDs:  req.PersonaIDs,
				Parallelism: req.Parallelism,
				BudgetUSD:   req.BudgetUSD,
				MaxCycles:   req.MaxCycles,
			})
		case "qa":
			sess, err = s.KanbanAutopilot.StartQA(r.Context(), req.CardID, kanban.QAOpts{
				Checks:         req.Checks,
				FixAgentID:     req.FixAgentID,
				MaxFixAttempts: req.MaxFixAttempts,
				BudgetUSD:      req.BudgetUSD,
			})
		default:
			writeJSONError(w, http.StatusBadRequest, "mode must be feature-dev or qa")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, sess)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Attachments ───────────────────────────────────────────────────────────
//
//	GET    /api/v1/boards/{bid}/cards/{cid}/attachments
//	POST   /api/v1/boards/{bid}/cards/{cid}/attachments   (multipart, field "file")
//	GET    /api/v1/attachments/{id}                        (download bytes)
//	DELETE /api/v1/attachments/{id}                        (remove row + file)
//
// Files live under <attachmentRoot>/<card_id>/<id>-<filename>. Set
// AuthService.AttachmentRoot at wire time (defaults to a temp dir).

func (s *AuthService) handleCardAttachments(w http.ResponseWriter, r *http.Request, cardID string) {
	p := auth.PrincipalFrom(r.Context())
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		list, err := s.Store.CardAttachments().ListByCard(r.Context(), cardID)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, list)
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermCardsEdit); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		// 32 MB cap — kanban attachments are screenshots / logs / small
		// diffs, not source trees. Crank the limit later if operators ask.
		const maxUpload = 32 << 20
		if err := r.ParseMultipartForm(maxUpload); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "form field 'file' missing")
			return
		}
		defer file.Close()
		att, err := s.persistAttachment(cardID, header.Filename, header.Header.Get("Content-Type"), file)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if p != nil {
			att.CreatedBy = p.Username
		}
		if err := s.Store.CardAttachments().Create(r.Context(), att); err != nil {
			_ = os.Remove(att.Path)
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, att)
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *AuthService) handleAttachmentDetail(w http.ResponseWriter, r *http.Request, id string) {
	p := auth.PrincipalFrom(r.Context())
	id = strings.TrimSuffix(id, "/")
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		att, err := s.Store.CardAttachments().Get(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "attachment not found")
			return
		}
		f, err := os.Open(att.Path)
		if err != nil {
			writeJSONError(w, http.StatusGone, "attachment file missing on disk")
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", att.MimeType)
		w.Header().Set("Content-Disposition", `attachment; filename="`+att.Filename+`"`)
		w.Header().Set("Content-Length", strconv.FormatInt(att.SizeBytes, 10))
		_, _ = io.Copy(w, f)
	case http.MethodDelete:
		if err := rbac.Enforce(r.Context(), p, rbac.PermCardsDelete); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		att, err := s.Store.CardAttachments().Get(r.Context(), id)
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "attachment not found")
			return
		}
		if err := s.Store.CardAttachments().Delete(r.Context(), id); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		_ = os.Remove(att.Path)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ── Branch preview ────────────────────────────────────────────────────────
//
//	GET    /api/v1/boards/{bid}/cards/{cid}/preview
//	POST   /api/v1/boards/{bid}/cards/{cid}/preview   {cmd, port}
//	DELETE /api/v1/boards/{bid}/cards/{cid}/preview
//
// The preview is bound to the card's current worktree (the run's branch);
// callers should dispatch a run first so a worktree exists. We don't
// auto-discover the dev-server command — the caller passes it explicitly
// ("npm run dev", "make dev-server", etc).

func (s *AuthService) handleCardPreview(w http.ResponseWriter, r *http.Request, cardID string) {
	p := auth.PrincipalFrom(r.Context())
	if s.KanbanPreview == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "preview manager not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		pv := s.KanbanPreview.GetByCard(cardID)
		if pv == nil {
			writeJSONError(w, http.StatusNotFound, "no preview running for this card")
			return
		}
		writeJSON(w, http.StatusOK, pv)
	case http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermRunsDispatch); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		var req struct {
			Cmd  string `json:"cmd"`
			Port int    `json:"port"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
		card, err := s.Store.BoardCards().Get(r.Context(), cardID)
		if err != nil || card == nil {
			writeJSONError(w, http.StatusNotFound, "card not found")
			return
		}
		if card.WorktreePath == "" {
			writeJSONError(w, http.StatusConflict, "card has no active worktree (dispatch a run first)")
			return
		}
		pv, err := s.KanbanPreview.Start(r.Context(), cardID, preview.StartOpts{
			WorktreePath: card.WorktreePath,
			Cmd:          req.Cmd,
			Port:         req.Port,
		})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, pv)
	case http.MethodDelete:
		if err := rbac.Enforce(r.Context(), p, rbac.PermRunsCancel); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		pv := s.KanbanPreview.GetByCard(cardID)
		if pv == nil {
			writeJSONError(w, http.StatusNotFound, "no preview running for this card")
			return
		}
		if err := s.KanbanPreview.Stop(pv.ID); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"stopped": true})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *AuthService) persistAttachment(cardID, filename, mime string, body io.Reader) (*datastore.CardAttachment, error) {
	root := s.AttachmentRoot
	if root == "" {
		root = filepath.Join(os.TempDir(), "opsintelligence-kanban-attachments")
	}
	dir := filepath.Join(root, cardID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("attachment dir: %w", err)
	}
	id := uuid.NewString()
	if mime == "" {
		mime = "application/octet-stream"
	}
	safe := strings.ReplaceAll(filename, string(os.PathSeparator), "_")
	full := filepath.Join(dir, id+"-"+safe)
	f, err := os.Create(full)
	if err != nil {
		return nil, fmt.Errorf("create attachment file: %w", err)
	}
	defer f.Close()
	n, err := io.Copy(f, body)
	if err != nil {
		_ = os.Remove(full)
		return nil, fmt.Errorf("write attachment: %w", err)
	}
	return &datastore.CardAttachment{
		ID:        id,
		CardID:    cardID,
		Filename:  safe,
		MimeType:  mime,
		SizeBytes: n,
		Path:      full,
	}, nil
}

func (s *AuthService) handleAutopilotDetail(w http.ResponseWriter, r *http.Request, path string) {
	p := auth.PrincipalFrom(r.Context())
	parts := strings.SplitN(path, "/", 2)
	sessID := parts[0]
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}
	if s.KanbanAutopilot == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "autopilot not configured")
		return
	}

	switch {
	case sub == "" && r.Method == http.MethodGet:
		if err := rbac.Enforce(r.Context(), p, rbac.PermRunsRead); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		sess := s.KanbanAutopilot.Get(sessID)
		if sess == nil {
			writeJSONError(w, http.StatusNotFound, "session not found")
			return
		}
		writeJSON(w, http.StatusOK, sess)
	case (sub == "stop" || sub == "stop/") && r.Method == http.MethodPost:
		if err := rbac.Enforce(r.Context(), p, rbac.PermRunsCancel); err != nil {
			writeJSONError(w, http.StatusForbidden, "permission denied")
			return
		}
		if err := s.KanbanAutopilot.Stop(sessID); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"stopped": true})
	default:
		writeJSONError(w, http.StatusNotFound, "not found")
	}
}

// ── Workflow presets ──────────────────────────────────────────────────
//
// Static templates that the Scrun setup wizard renders as one-click
// starting points. The "default" preset matches the legacy
// 6-column seed so old callers don't lose their muscle memory.

type workflowPresetDef struct {
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Columns     []wizardColumn `json:"columns"`
}

func intPtr(v int) *int { return &v }

var workflowPresets = []workflowPresetDef{
	{
		Slug: "default", Name: "Default", Description: "Inbox → Backlog → Todo → In Progress → Review → Done",
		Columns: []wizardColumn{
			{Name: "Inbox", Position: 0, Color: "#94a3b8"},
			{Name: "Backlog", Position: 1, Color: "#64748b"},
			{Name: "Todo", Position: 2, Color: "#3b82f6", WIPLimit: intPtr(8)},
			{Name: "In Progress", Position: 3, Color: "#f59e0b", WIPLimit: intPtr(4)},
			{Name: "Review", Position: 4, Color: "#a855f7", Gate: "human"},
			{Name: "Done", Position: 5, Color: "#10b981"},
		},
	},
	{
		Slug: "dev", Name: "Development", Description: "Standard agent dev flow with auto-validate gate before Review.",
		Columns: []wizardColumn{
			{Name: "Backlog", Position: 0, Color: "#64748b"},
			{Name: "Spec", Position: 1, Color: "#3b82f6", WIPLimit: intPtr(4)},
			{Name: "Build", Position: 2, Color: "#f59e0b", WIPLimit: intPtr(3), Automation: map[string]any{"auto_assign": true, "keep_agent": true}},
			{Name: "Validate", Position: 3, Color: "#06b6d4", Gate: "auto-validate"},
			{Name: "Review", Position: 4, Color: "#a855f7", Gate: "human"},
			{Name: "Done", Position: 5, Color: "#10b981"},
		},
	},
	{
		Slug: "research", Name: "Research", Description: "Lighter pipeline tuned for spikes and discovery work.",
		Columns: []wizardColumn{
			{Name: "Ideas", Position: 0, Color: "#94a3b8"},
			{Name: "Investigating", Position: 1, Color: "#3b82f6", WIPLimit: intPtr(3)},
			{Name: "Writing", Position: 2, Color: "#a855f7"},
			{Name: "Shared", Position: 3, Color: "#10b981"},
		},
	},
	{
		Slug: "support", Name: "Support", Description: "Triage queue for inbound bugs / Sentry imports.",
		Columns: []wizardColumn{
			{Name: "Triage", Position: 0, Color: "#ef4444"},
			{Name: "Diagnosing", Position: 1, Color: "#f59e0b", WIPLimit: intPtr(4)},
			{Name: "Fix", Position: 2, Color: "#3b82f6", WIPLimit: intPtr(3), Automation: map[string]any{"auto_assign": true}},
			{Name: "Verifying", Position: 3, Color: "#06b6d4", Gate: "auto-validate"},
			{Name: "Closed", Position: 4, Color: "#10b981"},
		},
	},
	{
		Slug: "ops", Name: "Operations", Description: "Change-management style flow with a human gate on rollout.",
		Columns: []wizardColumn{
			{Name: "Backlog", Position: 0, Color: "#64748b"},
			{Name: "Planned", Position: 1, Color: "#3b82f6"},
			{Name: "Executing", Position: 2, Color: "#f59e0b", WIPLimit: intPtr(2)},
			{Name: "Rollout", Position: 3, Color: "#a855f7", Gate: "human"},
			{Name: "Done", Position: 4, Color: "#10b981"},
		},
	},
}

func presetColumns(slug string) []wizardColumn {
	if slug == "" {
		slug = "default"
	}
	for _, p := range workflowPresets {
		if p.Slug == slug {
			out := make([]wizardColumn, len(p.Columns))
			copy(out, p.Columns)
			return out
		}
	}
	// Unknown preset → fall back to default so the board still seeds.
	return workflowPresets[0].Columns
}

func hasColumnOverrides(cols []wizardColumn) bool {
	for _, c := range cols {
		if c.Gate != "" || len(c.Automation) > 0 {
			return true
		}
	}
	return false
}

func (s *AuthService) handleWorkflowPresets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p := auth.PrincipalFrom(r.Context())
	if err := rbac.Enforce(r.Context(), p, rbac.PermBoardsRead); err != nil {
		writeJSONError(w, http.StatusForbidden, "permission denied")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"presets": workflowPresets})
}
